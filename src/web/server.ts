import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { randomUUID, timingSafeEqual } from 'node:crypto';
import { mkdirSync, watch, type FSWatcher } from 'node:fs';
import { open, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { paths } from '../config/paths';
import { bridgeVersion } from '../core/version';
import { NotWiredYetError, type AdminService } from '../admin/service';
import { UI_HTML } from './ui';

/**
 * 本机 Web 控制台 HTTP 面（node:http，零新依赖）。
 *
 * 安全清单（设计文档 §5，逐条落实）：
 *   1. 仅绑定 127.0.0.1，绝不 0.0.0.0；不提供任何「远程访问」配置项。
 *   2. token 鉴权：启动生成随机 token（crypto.randomUUID）；所有请求校验
 *      `Authorization: Bearer` / cookie；`?token=` 仅用于首跳换 cookie（随后
 *      302 去掉 URL 里的 token，防日志/历史记录泄漏长期凭据）。
 *   3. Host/Origin 校验防 DNS rebinding（只认 127.0.0.1 / localhost / [::1]）。
 *   4. 端点最小化：只读（state / diagnosis / logs / sessions）+「DM 卡片已有
 *      等价操作」的写入占位（backend / permission / no-mention / auto-compact，
 *      第一棒一律 501）。绝不暴露「执行命令」「读任意文件」类端点。
 *   5. 日志流只透传现有文件日志行（已是尾 6 位脱敏风格），token 绝不进日志。
 *
 * TODO(第二棒): daemon（run 进程）内嵌同一 createWebServer —— service 换成接住
 * 在跑 orchestrator/bridge 实例的实现（真实 WS 连接状态 + 写操作落盘并驱逐
 * 活跃会话），写路由的 501 即自动消失；DM dm.* 回调与这里共享该 AdminService。
 */
export interface WebServerOptions {
  service: AdminService;
  /** 测试注入：日志目录（默认 ~/.feishu-codex-bridge/logs，与 core/logger 同址）。 */
  logDir?: string;
  /** 测试注入：固定 token（默认 crypto.randomUUID()）。 */
  token?: string;
  /** 测试注入：页面 HTML（默认内嵌 UI）。 */
  html?: string;
}

export interface WebServer {
  server: Server;
  token: string;
  /** 监听 127.0.0.1:port（port=0 取临时端口）；返回实际端口与含 token 的可点击 URL。 */
  listen(port: number): Promise<{ port: number; url: string }>;
  close(): Promise<void>;
}

const COOKIE_NAME = 'fcb_console_token';
/** SSE 初始回放的日志尾部字节数。 */
const SSE_INITIAL_TAIL_BYTES = 16 * 1024;

export function createWebServer(opts: WebServerOptions): WebServer {
  const token = opts.token ?? randomUUID();
  const html = opts.html ?? UI_HTML;
  const logDir = opts.logDir ?? join(paths.appDir, 'logs');
  const sseCleanups = new Set<() => void>();

  const server = createServer((req, res) => {
    void handle(req, res).catch((err) => {
      if (!res.headersSent) {
        sendJson(res, 500, { error: 'internal', message: err instanceof Error ? err.message : String(err) });
      } else {
        res.end();
      }
    });
  });

  async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    // ── Host/Origin 校验（防 DNS rebinding）────────────────────────────────
    if (!isLoopbackHost(req.headers.host)) {
      sendJson(res, 403, { error: 'forbidden_host', message: '仅允许 127.0.0.1 / localhost 访问' });
      return;
    }
    const origin = req.headers.origin;
    if (origin !== undefined && !isLoopbackOrigin(origin)) {
      sendJson(res, 403, { error: 'forbidden_origin', message: '跨站请求被拒绝' });
      return;
    }

    const url = new URL(req.url ?? '/', 'http://127.0.0.1');
    const pathName = url.pathname;

    // ── token 鉴权（Bearer / cookie / ?token= 首跳换 cookie）──────────────
    const queryToken = url.searchParams.get('token');
    const presented = bearerToken(req) ?? cookieToken(req) ?? queryToken;
    if (presented === null || !safeEqual(presented, token)) {
      sendJson(res, 401, { error: 'unauthorized', message: '缺少或错误的 token；请用启动日志里打印的完整 URL 打开' });
      return;
    }
    // ?token= 合法 → 种 cookie；对页面请求再 302 清掉 URL 上的 token。
    const setCookie =
      queryToken !== null && safeEqual(queryToken, token)
        ? `${COOKIE_NAME}=${token}; HttpOnly; SameSite=Strict; Path=/`
        : undefined;
    if (setCookie && pathName === '/' && req.method === 'GET') {
      res.writeHead(302, { 'Set-Cookie': setCookie, Location: '/' });
      res.end();
      return;
    }
    if (setCookie) res.setHeader('Set-Cookie', setCookie);

    // ── 路由 ──────────────────────────────────────────────────────────────
    if (req.method === 'GET' && pathName === '/') {
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
      res.end(html);
      return;
    }

    if (req.method === 'GET' && pathName === '/api/state') {
      await handleState(res);
      return;
    }

    if (req.method === 'GET' && pathName === '/api/diagnosis') {
      await handleDiagnosis(res, url.searchParams.get('bot'));
      return;
    }

    if (req.method === 'GET' && pathName === '/api/logs') {
      const maxBytes = clampInt(url.searchParams.get('maxBytes'), 1024, 256 * 1024, 64 * 1024);
      const text = await opts.service.tailLogs({ maxBytes });
      res.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8' });
      res.end(text);
      return;
    }

    if (req.method === 'GET' && pathName === '/api/logs/stream') {
      handleLogStream(req, res);
      return;
    }

    // GET /api/project/:name/sessions —— 🧵 话题钻取
    const sessionsMatch = /^\/api\/project\/([^/]+)\/sessions$/.exec(pathName);
    if (req.method === 'GET' && sessionsMatch) {
      const botId = url.searchParams.get('bot') ?? (await defaultBotId());
      if (!botId) {
        sendJson(res, 404, { error: 'no_bot', message: '没有已注册的机器人' });
        return;
      }
      const sessions = await opts.service.listSessions(botId, decodeURIComponent(sessionsMatch[1]!));
      sendJson(res, 200, { sessions });
      return;
    }

    // POST 写操作占位 —— 第一棒一律 501（NotWiredYetError → 501 映射）。
    // TODO(第二棒): daemon 内 service 实现落地后，这些路由原样变成真实写入。
    const writeMatch = /^\/api\/project\/([^/]+)\/(backend|permission|no-mention|auto-compact)$/.exec(pathName);
    if (req.method === 'POST' && writeMatch) {
      const project = decodeURIComponent(writeMatch[1]!);
      const action = writeMatch[2]!;
      const botId = url.searchParams.get('bot') ?? (await defaultBotId());
      if (!botId) {
        sendJson(res, 404, { error: 'no_bot', message: '没有已注册的机器人' });
        return;
      }
      let body: Record<string, unknown>;
      try {
        body = await readJsonBody(req);
      } catch {
        sendJson(res, 400, { error: 'bad_body', message: '请求体必须是 JSON' });
        return;
      }
      try {
        if (action === 'backend') {
          await opts.service.switchBackend(botId, project, String(body.backend ?? ''));
        } else if (action === 'permission') {
          await opts.service.setPermissionMode(botId, project, {
            mode: body.mode as never,
            guestMode: body.guestMode as never,
            network: typeof body.network === 'boolean' ? body.network : undefined,
          });
        } else if (action === 'no-mention') {
          await opts.service.setNoMention(botId, project, body.on === true);
        } else {
          await opts.service.setAutoCompact(botId, project, body.on === true);
        }
        sendJson(res, 200, { ok: true });
      } catch (err) {
        if (err instanceof NotWiredYetError) {
          // 占位响应：第二棒（daemon 进程内集成）接上真实现后自动变 200。
          sendJson(res, 501, { error: 'not_wired_yet', message: err.message });
          return;
        }
        throw err;
      }
      return;
    }

    sendJson(res, 404, { error: 'not_found' });
  }

  async function defaultBotId(): Promise<string | undefined> {
    const bots = await opts.service.listBots();
    return (bots.find((b) => b.current) ?? bots[0])?.appId;
  }

  /** GET /api/state —— bots + projects(+话题数) 聚合快照，前端 5s 轮询。 */
  async function handleState(res: ServerResponse): Promise<void> {
    const bots = await opts.service.listBots();
    const out = [];
    for (const b of bots) {
      const projects = await opts.service.listProjects(b.appId).catch(() => []);
      out.push({ ...b, projects });
    }
    sendJson(res, 200, { version: bridgeVersion(), generatedAt: Date.now(), bots: out });
  }

  /** GET /api/diagnosis —— 事件订阅三态 + 各后端环境体检（按需，较慢）。 */
  async function handleDiagnosis(res: ServerResponse, botParam: string | null): Promise<void> {
    const botId = botParam ?? (await defaultBotId());
    const [backends, event] = await Promise.all([
      opts.service.doctorBackends(),
      botId ? opts.service.eventDiagnosis(botId) : Promise.resolve(undefined),
    ]);
    sendJson(res, 200, { bot: botId, backends, event });
  }

  /**
   * GET /api/logs/stream —— SSE 实时跟随当日文件日志。
   * 初始回放今日文件尾部，之后 fs.watch 目录 + 按 offset 增量读；跨天自动切到
   * 新的 YYYY-MM-DD.log。EventSource 不能带 header，鉴权靠 cookie（首跳已种）。
   */
  function handleLogStream(req: IncomingMessage, res: ServerResponse): void {
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      Connection: 'keep-alive',
      'X-Accel-Buffering': 'no',
    });
    res.write(': connected\n\n');

    let currentFile = todayLogFile(logDir);
    let offset = 0;
    let reading = false;
    let pending = false;
    let closed = false;

    const sendLines = (text: string): void => {
      for (const line of text.split('\n')) {
        if (line.trim() === '') continue;
        res.write(`data: ${line}\n\n`);
      }
    };

    /** 从 offset 起把新增字节读出来推送；并发触发时合并成一次补读。 */
    const pump = async (): Promise<void> => {
      if (closed) return;
      if (reading) {
        pending = true;
        return;
      }
      reading = true;
      try {
        const today = todayLogFile(logDir);
        if (today !== currentFile) {
          currentFile = today; // 跨天：切新文件从头读
          offset = 0;
        }
        const st = await stat(currentFile).catch(() => null);
        if (!st) return;
        if (st.size < offset) offset = 0; // 文件被轮转/截断
        if (st.size === offset) return;
        const fh = await open(currentFile, 'r');
        try {
          const buf = Buffer.alloc(st.size - offset);
          await fh.read(buf, 0, buf.length, offset);
          offset = st.size;
          sendLines(buf.toString('utf8'));
        } finally {
          await fh.close();
        }
      } finally {
        reading = false;
        if (pending && !closed) {
          pending = false;
          void pump();
        }
      }
    };

    // 初始回放：今日文件最后 SSE_INITIAL_TAIL_BYTES 字节（丢掉可能的半行）。
    void (async () => {
      const st = await stat(currentFile).catch(() => null);
      if (st && st.size > 0) {
        const start = Math.max(0, st.size - SSE_INITIAL_TAIL_BYTES);
        const fh = await open(currentFile, 'r');
        try {
          const buf = Buffer.alloc(st.size - start);
          await fh.read(buf, 0, buf.length, start);
          let text = buf.toString('utf8');
          if (start > 0) {
            const nl = text.indexOf('\n');
            text = nl === -1 ? '' : text.slice(nl + 1);
          }
          offset = st.size;
          sendLines(text);
        } finally {
          await fh.close();
        }
      }
    })().catch(() => undefined);

    // 监听目录而不是单个文件：今日文件可能尚不存在（首条日志才创建）。
    let watcher: FSWatcher | undefined;
    try {
      mkdirSync(logDir, { recursive: true });
      watcher = watch(logDir, () => void pump());
    } catch {
      /* 监听失败 → 退化为只有心跳；下一棒可换轮询 */
    }
    const heartbeat = setInterval(() => res.write(': ka\n\n'), 15_000);

    const cleanup = (): void => {
      if (closed) return;
      closed = true;
      clearInterval(heartbeat);
      try {
        watcher?.close();
      } catch {
        /* noop */
      }
      sseCleanups.delete(cleanup);
      res.end();
    };
    sseCleanups.add(cleanup);
    req.on('close', cleanup);
  }

  return {
    server,
    token,
    listen(port: number): Promise<{ port: number; url: string }> {
      return new Promise((resolve, reject) => {
        server.once('error', reject);
        // 安全清单 #1：仅 loopback，绝不 0.0.0.0。
        server.listen(port, '127.0.0.1', () => {
          const addr = server.address();
          const actual = typeof addr === 'object' && addr ? addr.port : port;
          resolve({ port: actual, url: `http://127.0.0.1:${actual}/?token=${token}` });
        });
      });
    },
    close(): Promise<void> {
      for (const cleanup of [...sseCleanups]) cleanup();
      return new Promise((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
      });
    },
  };
}

// ── helpers ─────────────────────────────────────────────────────────────────

function sendJson(res: ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' });
  res.end(JSON.stringify(body));
}

function bearerToken(req: IncomingMessage): string | null {
  const h = req.headers.authorization;
  if (!h?.startsWith('Bearer ')) return null;
  return h.slice('Bearer '.length).trim();
}

function cookieToken(req: IncomingMessage): string | null {
  const raw = req.headers.cookie;
  if (!raw) return null;
  for (const part of raw.split(';')) {
    const eq = part.indexOf('=');
    if (eq === -1) continue;
    if (part.slice(0, eq).trim() === COOKIE_NAME) return part.slice(eq + 1).trim();
  }
  return null;
}

/** 常数时间比较（长度不同直接 false，不泄漏前缀匹配进度）。 */
function safeEqual(a: string, b: string): boolean {
  const ba = Buffer.from(a);
  const bb = Buffer.from(b);
  return ba.length === bb.length && timingSafeEqual(ba, bb);
}

function isLoopbackHost(host: string | undefined): boolean {
  if (!host) return false;
  return /^(127\.0\.0\.1|localhost|\[::1\])(:\d+)?$/i.test(host);
}

function isLoopbackOrigin(origin: string): boolean {
  try {
    const u = new URL(origin);
    return u.protocol === 'http:' && /^(127\.0\.0\.1|localhost|\[::1\])$/i.test(u.hostname);
  } catch {
    return false;
  }
}

function todayLogFile(dir: string): string {
  return join(dir, `${new Date().toISOString().slice(0, 10)}.log`);
}

function clampInt(raw: string | null, min: number, max: number, fallback: number): number {
  const n = Number(raw);
  if (!Number.isFinite(n)) return fallback;
  return Math.min(max, Math.max(min, Math.trunc(n)));
}

async function readJsonBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of req) {
    size += (chunk as Buffer).length;
    if (size > 64 * 1024) throw new Error('body too large');
    chunks.push(chunk as Buffer);
  }
  const text = Buffer.concat(chunks).toString('utf8');
  if (text.trim() === '') return {};
  const parsed = JSON.parse(text) as unknown;
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) throw new Error('not an object');
  return parsed as Record<string, unknown>;
}
