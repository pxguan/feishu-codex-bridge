import { mkdtempSync, rmSync, writeFileSync, appendFileSync } from 'node:fs';
import { request as httpRequest } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { createWebServer, type WebServer } from '../src/web/server';
import { NotWiredYetError, type AdminService } from '../src/admin/service';

// 内存 stub：server 层单测不碰真实文件/注册表（service 自有专门的集成测试）。
function stubService(): AdminService {
  return {
    async listBots() {
      return [
        {
          name: 'alpha',
          appId: 'cli_a',
          tenant: 'feishu' as const,
          botName: '阿尔法',
          active: true,
          current: true,
          running: true,
          pid: 4242,
        },
      ];
    },
    async listProjects() {
      return [
        {
          name: 'demo',
          chatId: 'oc_x',
          cwd: '/tmp/demo',
          blank: false,
          kind: 'multi' as const,
          origin: 'created' as const,
          noMention: true,
          autoCompact: true,
          mode: 'full' as const,
          guestMode: 'full' as const,
          network: false,
          backend: 'codex-appserver',
          allowedUsersCount: 0,
          sessionCount: 3,
          createdAt: 1,
        },
      ];
    },
    async getProject() {
      return undefined;
    },
    async switchBackend() {
      throw new NotWiredYetError('🧠 切换后端');
    },
    async setPermissionMode() {
      throw new NotWiredYetError('🔐 设置权限档');
    },
    async setNoMention() {
      throw new NotWiredYetError('✋ 免@ 开关');
    },
    async setAutoCompact() {
      throw new NotWiredYetError('🗜️ 自动压缩开关');
    },
    async doctorBackends() {
      return [{ id: 'codex-appserver', name: 'Codex', ok: true, version: '1.0.0', isDefault: true }];
    },
    async eventDiagnosis() {
      return { state: 'unchecked' as const, reason: 'stub' };
    },
    async listSessions() {
      return [
        {
          threadId: 't1',
          chatId: 'oc_x',
          summary: '修复登录 bug',
          backend: 'codex-appserver',
          createdAt: 1,
          updatedAt: 2,
        },
      ];
    },
    async tailLogs() {
      return '{"ts":"2026-06-13T00:00:00Z","level":"info","phase":"ws","event":"connected"}\n';
    },
  };
}

const TOKEN = 'test-token-1234';
let web: WebServer;
let base: string;
let logDir: string;

function get(path: string, init: RequestInit = {}): Promise<Response> {
  return fetch(`${base}${path}`, { redirect: 'manual', ...init });
}

function authed(path: string, init: RequestInit = {}): Promise<Response> {
  return get(path, { ...init, headers: { Authorization: `Bearer ${TOKEN}`, ...(init.headers ?? {}) } });
}

// node 的 fetch 类型把 json() 标成 unknown；测试里按宽松 JSON 读。
// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function jsonOf(res: Response): Promise<any> {
  return (await res.json()) as any;
}

beforeAll(async () => {
  logDir = mkdtempSync(join(tmpdir(), 'web-server-test-logs-'));
  web = createWebServer({ service: stubService(), token: TOKEN, logDir });
  const { port, url } = await web.listen(0); // 临时端口，起了就关，绝不占固定口
  base = `http://127.0.0.1:${port}`;
  expect(url).toBe(`${base}/?token=${TOKEN}`);
});

afterAll(async () => {
  await web.close();
  rmSync(logDir, { recursive: true, force: true });
});

describe('web server · 安全（loopback + token + Host 校验）', () => {
  it('仅绑定 127.0.0.1（绝不 0.0.0.0）', () => {
    const addr = web.server.address();
    expect(typeof addr).toBe('object');
    expect((addr as { address: string }).address).toBe('127.0.0.1');
  });

  it('无 token → 401', async () => {
    const res = await get('/api/state');
    expect(res.status).toBe(401);
    expect((await jsonOf(res)).error).toBe('unauthorized');
  });

  it('错误 token → 401（Bearer / query 都不行）', async () => {
    expect((await get('/api/state', { headers: { Authorization: 'Bearer wrong' } })).status).toBe(401);
    expect((await get('/api/state?token=wrong')).status).toBe(401);
  });

  it('Authorization: Bearer 正确 token → 200', async () => {
    expect((await authed('/api/state')).status).toBe(200);
  });

  it('?token= 首跳：页面 302 去掉 URL token 并种 cookie；cookie 可继续鉴权', async () => {
    const first = await get(`/?token=${TOKEN}`);
    expect(first.status).toBe(302);
    expect(first.headers.get('location')).toBe('/');
    const cookie = first.headers.get('set-cookie') ?? '';
    expect(cookie).toContain('fcb_console_token=');
    expect(cookie).toContain('HttpOnly');
    const cookiePair = cookie.split(';')[0]!;
    const page = await get('/', { headers: { Cookie: cookiePair } });
    expect(page.status).toBe(200);
    expect(await page.text()).toContain('Codex Bridge 管理台');
    const api = await get('/api/state', { headers: { Cookie: cookiePair } });
    expect(api.status).toBe(200);
  });

  // fetch(undici) 会静默丢弃 Host/Origin 这类 forbidden header，用裸 http.request 才能真发出去。
  function rawStatus(headers: Record<string, string>): Promise<number> {
    const port = Number(new URL(base).port);
    return new Promise((resolve, reject) => {
      const req = httpRequest(
        { host: '127.0.0.1', port, path: '/api/state', headers: { Authorization: `Bearer ${TOKEN}`, ...headers } },
        (res) => {
          res.resume();
          resolve(res.statusCode ?? 0);
        },
      );
      req.on('error', reject);
      req.end();
    });
  }

  it('非 loopback Host → 403（防 DNS rebinding）', async () => {
    expect(await rawStatus({ Host: 'evil.example.com' })).toBe(403);
    expect(await rawStatus({ Host: 'evil.example.com:7866' })).toBe(403);
  });

  it('跨站 Origin → 403', async () => {
    expect(await rawStatus({ Origin: 'http://evil.example.com' })).toBe(403);
  });
});

describe('web server · 只读 API', () => {
  it('/api/state 快照形状：version/generatedAt + bots[].projects[]', async () => {
    const body = await jsonOf(await authed('/api/state'));
    expect(typeof body.version).toBe('string');
    expect(typeof body.generatedAt).toBe('number');
    expect(body.bots).toHaveLength(1);
    const bot = body.bots[0];
    expect(bot.appId).toBe('cli_a');
    expect(bot.running).toBe(true);
    expect(bot.projects).toHaveLength(1);
    const p = bot.projects[0];
    expect(p).toMatchObject({
      name: 'demo',
      kind: 'multi',
      mode: 'full',
      backend: 'codex-appserver',
      sessionCount: 3,
      noMention: true,
    });
  });

  it('/api/diagnosis：backends + event 三态', async () => {
    const body = await jsonOf(await authed('/api/diagnosis'));
    expect(body.bot).toBe('cli_a');
    expect(body.backends[0]).toMatchObject({ id: 'codex-appserver', ok: true });
    expect(body.event.state).toBe('unchecked');
  });

  it('/api/project/:name/sessions：话题列表', async () => {
    const body = await jsonOf(await authed('/api/project/demo/sessions'));
    expect(body.sessions).toHaveLength(1);
    expect(body.sessions[0].summary).toBe('修复登录 bug');
  });

  it('未知路径 → 404', async () => {
    expect((await authed('/api/nope')).status).toBe(404);
  });
});

describe('web server · 写操作占位（第二棒接线）', () => {
  it.each(['backend', 'permission', 'no-mention', 'auto-compact'])('POST /api/project/demo/%s → 501', async (action) => {
    const res = await authed(`/api/project/demo/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ backend: 'claude-sdk', mode: 'qa', on: true }),
    });
    expect(res.status).toBe(501);
    const body = await jsonOf(res);
    expect(body.error).toBe('not_wired_yet');
    expect(body.message).toContain('第二棒');
  });

  it('写操作同样要鉴权：无 token → 401（不是 501）', async () => {
    const res = await get('/api/project/demo/backend', { method: 'POST', body: '{}' });
    expect(res.status).toBe(401);
  });
});

describe('web server · 日志', () => {
  it('GET /api/logs：tail 文本', async () => {
    const res = await authed('/api/logs');
    expect(res.status).toBe(200);
    expect(await res.text()).toContain('ws');
  });

  it('GET /api/logs/stream：SSE 推送已有日志尾部 + 追加增量', async () => {
    const today = new Date().toISOString().slice(0, 10);
    const file = join(logDir, `${today}.log`);
    writeFileSync(file, '{"event":"first-line"}\n');

    const ac = new AbortController();
    const res = await authed('/api/logs/stream', { signal: ac.signal });
    expect(res.status).toBe(200);
    expect(res.headers.get('content-type')).toContain('text/event-stream');

    const reader = res.body!.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    const readUntil = async (needle: string, timeoutMs: number): Promise<string> => {
      const deadline = Date.now() + timeoutMs;
      while (!buf.includes(needle)) {
        if (Date.now() > deadline) throw new Error(`SSE 超时未等到 ${needle}；已收到：${buf}`);
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
      }
      return buf;
    };

    await readUntil('first-line', 3000);
    // 追加一行 → fs.watch 增量推送
    appendFileSync(file, '{"event":"second-line"}\n');
    await readUntil('second-line', 3000);
    expect(buf).toContain('data: {"event":"first-line"}');
    expect(buf).toContain('data: {"event":"second-line"}');
    ac.abort();
  }, 10_000);
});
