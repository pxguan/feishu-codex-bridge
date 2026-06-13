import { randomUUID } from 'node:crypto';
import { existsSync } from 'node:fs';
import { Readable, Writable } from 'node:stream';
import type { ChildProcess } from 'node:child_process';
import type * as acp from '@agentclientprotocol/sdk';
import { spawnProcess } from '../../platform/spawn';
import { log } from '../../core/logger';
import { loadConfig } from '../../config/store';
import { getAcpCommand } from '../../config/schema';
import { bridgeVersion } from '../../core/version';
import { BRIDGE_DEVELOPER_INSTRUCTIONS } from '../bridge-instructions';
import type {
  AgentBackend,
  AgentCapabilities,
  AgentEvent,
  AgentInput,
  AgentRun,
  AgentThread,
  BackendProbe,
  CompactResult,
  ModelInfo,
  ResumeThreadOptions,
  StartThreadOptions,
  ThreadHistory,
  ThreadSummary,
  TurnOptions,
} from '../types';
import { AcpEventMapper, type AcpUpdateLike, type AcpUsageLike } from './event-map';

/**
 * Claude Code backend via ACP（Agent Client Protocol）—— bridge 作 ACP **client**，
 * spawn 一个 ACP server 子进程（默认 claude-pty-acp：把交互式 Claude Code 暴露成
 * ACP agent，用量走订阅而非 Agent SDK credit——这是本后端存在的全部理由，见
 * research/06+07）。一线程一 server 子进程（stdio 传输），与 codex/claude-sdk 的
 * 进程模型一致，supervisor/watchdog/⏹ 语义透明复用。
 *
 * SDK 版本 **精确 pin 0.25.0**（package.json 无 ^）：ACP SDK 0.2x 仍在快速迭代
 * （0.20→0.25 连续破坏性变化、mode 方法已预告废弃改 config options），与参考
 * 项目 claude-pty-acp（^0.25.0）同代。升级时整目录回归后再动。协议类型全部
 * 关在 src/agent/acp/ 内（event-map 用结构化视图），不泄漏到卡片层。
 *
 * MINIMAL SLICE（对齐 claude-sdk 的最小切片）：能力守卫 + 硬错误，无半实现——
 *   - goal/steer/compact → throw（ACP 无对应协议面；steer 失败 orchestrator 落排队）
 *   - resume 选择卡（listThreads/readHistory）→ throw / 空。resumeThread 本身已
 *     实现：ACP `session/load`（claude-pty-acp 宣告 loadSession 能力，参考项目
 *     test-resume.mjs 全链路验证过「杀进程→loadSession→记忆恢复」）。server 不
 *     支持 loadSession 时 connect 抛清晰错误 → resolveThread 落回「新会话 + 话题
 *     回织」的既有降级。
 *
 * 权限：仅 'full' 档（assertFullMode，qa/write fail-closed——bridge 隔着 ACP 无法
 * 保证对端的只读约束）。server 的 session/request_permission 在 full 档自动
 * allow 并记日志（见 onRequestPermission）；fs/terminal capabilities 全 false，
 * server 不能反向读写 bridge 侧文件系统。
 */

/** ACP server 的启动命令（解析结果 / 配置注入）。 */
export interface AcpServerCommand {
  command: string;
  args: string[];
}

/** PATH 上要找的默认 ACP server 命令名。 */
const ACP_SERVER_BIN = 'claude-pty-acp';

/** initialize 是纯协议握手（server 不 spawn agent），慢于此即视为不是 ACP server。 */
const INITIALIZE_TIMEOUT_MS = 10_000;
/** session/new・session/load 会真正拉起交互式 claude（参考实现自带 30s 就绪窗口
 * + resume 还要回放历史），给足余量。 */
const SESSION_TIMEOUT_MS = 90_000;
/** doctor 轻探活（spawn + initialize 握手）的窗口。 */
const DOCTOR_TIMEOUT_MS = 5_000;

const IS_WIN = process.platform === 'win32';

// goal/steer/compact：ACP 协议无对应面；resume 守卫的是 /resume 选择卡
// （listThreads/readHistory 未实现），resumeThread 本身不经此能力位。
// approvals：request_permission 在 full 档自动批准，不外抛 approval_request。
const CAPABILITIES: AgentCapabilities = {
  goal: false,
  steer: false,
  compact: false,
  resume: false,
  approvals: false,
};

/** ACP 没有模型切换面——模型由 server 背后的本机 Claude Code 配置决定。单条
 * 静态项只为满足 /model 选择卡的 pickDefault；TurnOptions.model 被忽略。 */
const STATIC_MODELS: ModelInfo[] = [
  {
    id: 'claude-acp-default',
    displayName: 'Claude Code（订阅）',
    description: '模型由 claude-pty-acp 背后的本机 Claude Code 配置决定（ACP 不支持切换模型）',
    hidden: false,
    isDefault: true,
    supportedEfforts: [],
    defaultEffort: 'medium',
  },
];

/** Simple async queue (push from producers, async-iterate once) — same shape
 * as claude-sdk/backend and app-server-client. */
class AsyncQueue<T> {
  private items: T[] = [];
  private waiters: ((v: IteratorResult<T>) => void)[] = [];
  private closed = false;

  push(item: T): void {
    if (this.closed) return;
    const w = this.waiters.shift();
    if (w) w({ value: item, done: false });
    else this.items.push(item);
  }

  close(): void {
    this.closed = true;
    while (this.waiters.length) this.waiters.shift()!({ value: undefined as never, done: true });
  }

  async *[Symbol.asyncIterator](): AsyncGenerator<T> {
    while (true) {
      if (this.items.length) {
        yield this.items.shift()!;
        continue;
      }
      if (this.closed) return;
      const next = await new Promise<IteratorResult<T>>((resolve) => this.waiters.push(resolve));
      if (next.done) return;
      yield next.value;
    }
  }
}

function notSupported(what: string): Error {
  return new Error(`ACP 后端暂不支持${what}（codex 专属能力，已按能力守卫拒绝）`);
}

function errMsg(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function withTimeout<T>(p: Promise<T>, ms: number, what: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const t = setTimeout(() => reject(new Error(`${what} 超时（${Math.round(ms / 1000)}s）`)), ms);
    p.then(
      (v) => {
        clearTimeout(t);
        resolve(v);
      },
      (e) => {
        clearTimeout(t);
        reject(e);
      },
    );
  });
}

// ── server 命令解析 ────────────────────────────────────────────────────
// 开源仓库不能写死任何本机路径。顺序：① per-bot config 的 preferences.acpCommand
// 覆盖 → ② PATH 上的 claude-pty-acp → ③ null（doctor 给装法提示）。

/** PATH 命中缓存（成功才缓存；existsSync 复验，卸载/移动自动失效——同 locate.ts）。 */
let pathBinCache: string | null = null;

export async function resolveAcpCommand(opts?: { force?: boolean }): Promise<AcpServerCommand | null> {
  const cfg = await loadConfig().catch(() => ({}));
  const override = getAcpCommand(cfg);
  if (override) return override;

  if (!opts?.force && pathBinCache && existsSync(pathBinCache)) {
    return { command: pathBinCache, args: [] };
  }
  pathBinCache = await whichAsync(ACP_SERVER_BIN);
  return pathBinCache ? { command: pathBinCache, args: [] } : null;
}

/** 异步 which/where（doctor 跑在卡片回调等事件循环上下文，绝不 spawnSync）。 */
function whichAsync(cmd: string): Promise<string | null> {
  return new Promise((resolve) => {
    let child: ChildProcess;
    try {
      child = spawnProcess(IS_WIN ? 'where' : 'which', [cmd], { stdio: ['ignore', 'pipe', 'ignore'] });
    } catch {
      resolve(null);
      return;
    }
    let stdout = '';
    child.stdout?.setEncoding('utf8');
    child.stdout?.on('data', (d: string) => {
      stdout += d;
    });
    child.on('error', () => resolve(null));
    child.on('close', (code) => {
      if (code !== 0) {
        resolve(null);
        return;
      }
      const first = stdout
        .split('\n')
        .map((l) => l.trim())
        .find(Boolean);
      resolve(first && existsSync(first) ? first : null);
    });
  });
}

interface ActiveTurn {
  turnId: string;
  queue: AsyncQueue<AgentEvent>;
  mapper: AcpEventMapper;
  /** ⏹ 已发 session/cancel —— 其后到达的 request_permission 必须回 cancelled（协议契约）。 */
  aborted: boolean;
  settled: boolean;
}

class AcpThread implements AgentThread {
  /** server 在 session/new 时分配（resume 则是既有 id），connect() 后才有值。 */
  private _sessionId = '';
  private readonly resumeSessionId: string | undefined;
  private conn!: acp.ClientSideConnection;
  private child: ChildProcess | undefined;
  private childExited = false;
  private closedByUs = false;
  private active: ActiveTurn | undefined;
  private turnSeq = 0;
  private currentTurnId: string | undefined;
  private lastActivityAt = Date.now();
  /** 桥接输出约定只随首条用户消息发一次（ACP 没有 developer/system 注入通道）；
   * resume 的会话在它新建的那次已经发过。 */
  private instructionsSent: boolean;

  constructor(
    private readonly opts: StartThreadOptions,
    private readonly server: AcpServerCommand,
    resumeSessionId?: string,
  ) {
    this.resumeSessionId = resumeSessionId;
    this.instructionsSent = resumeSessionId !== undefined;
  }

  get sessionId(): string {
    return this._sessionId;
  }

  /** Spawn the ACP server, handshake, and create/load the session. */
  async connect(): Promise<void> {
    const sdk = await import('@agentclientprotocol/sdk');
    const child = spawnProcess(this.server.command, this.server.args, {
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    this.child = child;
    log.info('agent', 'acp-spawn', {
      pid: child.pid ?? null,
      command: this.server.command,
      cwd: this.opts.cwd,
    });
    // stdout 是协议通道；server 的日志全在 stderr —— drain（不读会反压卡死）。
    child.stderr?.on('data', (d: Buffer) => {
      const line = d.toString('utf8').trim();
      if (line) log.warn('agent', 'acp-stderr', { line: line.slice(0, 200) });
    });
    child.on('exit', (code, signal) => {
      this.childExited = true;
      log.info('agent', 'acp-exit', { pid: child.pid ?? null, code, signal });
      this.failActive(`ACP server 进程已退出（code=${code ?? '-'} signal=${signal ?? '-'}）`);
    });
    child.on('error', (err) => {
      this.childExited = true;
      this.failActive(`ACP server 启动失败：${errMsg(err)}`);
    });
    if (!child.stdin || !child.stdout) {
      await this.close().catch(() => undefined);
      throw new Error('ACP server 子进程缺少 stdio 管道');
    }

    const stream = sdk.ndJsonStream(
      Writable.toWeb(child.stdin) as WritableStream<Uint8Array>,
      Readable.toWeb(child.stdout) as ReadableStream<Uint8Array>,
    );
    this.conn = new sdk.ClientSideConnection(() => this.clientHandler(), stream);

    try {
      const init = await withTimeout(
        this.conn.initialize({
          protocolVersion: sdk.PROTOCOL_VERSION,
          clientInfo: { name: 'feishu-codex-bridge', version: bridgeVersion() },
          // fs/terminal 全 false：server 不得反向读写 bridge 侧文件系统/起终端
          // （协议保证：能力 false 时 agent MUST NOT 调对应方法）。
          clientCapabilities: { fs: { readTextFile: false, writeTextFile: false }, terminal: false },
        }),
        INITIALIZE_TIMEOUT_MS,
        'ACP initialize',
      );
      if (this.resumeSessionId !== undefined) {
        if (init.agentCapabilities?.loadSession !== true) {
          // 能力缺失 → 清晰失败，resolveThread 的 catch 落回新会话+话题回织。
          throw new Error('该 ACP server 不支持会话恢复（loadSession 能力未宣告）');
        }
        // 回放的 session/update 流（user/agent_message_chunk…）会打到
        // onSessionUpdate，但此刻没有 active turn —— 自然丢弃（恢复路径不需要
        // 重渲历史，上下文在 server 侧已就位）。
        await withTimeout(
          this.conn.loadSession({ sessionId: this.resumeSessionId, cwd: this.opts.cwd, mcpServers: [] }),
          SESSION_TIMEOUT_MS,
          'ACP session/load',
        );
        this._sessionId = this.resumeSessionId;
      } else {
        const res = await withTimeout(
          this.conn.newSession({ cwd: this.opts.cwd, mcpServers: [] }),
          SESSION_TIMEOUT_MS,
          'ACP session/new',
        );
        this._sessionId = res.sessionId;
      }
    } catch (err) {
      await this.close().catch(() => undefined);
      throw new Error(
        `ACP 后端启动失败：${errMsg(err)}${this.resumeSessionId !== undefined ? '（待恢复的会话可能已不存在）' : ''}`,
      );
    }
  }

  /** The ACP Client the server calls back into（必选两方法；fs/terminal 不实现）。 */
  private clientHandler(): acp.Client {
    return {
      sessionUpdate: async (n: acp.SessionNotification) => this.onSessionUpdate(n),
      requestPermission: async (req: acp.RequestPermissionRequest) => this.onRequestPermission(req),
    };
  }

  private onSessionUpdate(n: acp.SessionNotification): void {
    // 每条原始通知都算活动（含被映射器丢弃的），喂 idle watchdog。
    this.lastActivityAt = Date.now();
    const a = this.active;
    if (!a || a.settled) return; // load 回放 / 迟到通知（取消后仍可能补发）→ 丢弃
    for (const ev of a.mapper.map(n.update as AcpUpdateLike)) a.queue.push(ev);
  }

  private onRequestPermission(req: acp.RequestPermissionRequest): acp.RequestPermissionResponse {
    const a = this.active;
    const title = req.toolCall?.title || req.toolCall?.toolCallId || '(unknown tool)';
    // 协议契约：turn 被 cancel 后，pending 的权限请求必须回 cancelled。
    if (!a || a.settled || a.aborted) {
      return { outcome: { outcome: 'cancelled' } };
    }
    // full 档（startThread 已 assertFullMode）→ 自动 allow，与 codex 的
    // danger-full-access / claude-sdk 的 bypassPermissions 同语义；记日志留痕。
    const allow =
      req.options.find((o) => o.kind === 'allow_once') ?? req.options.find((o) => o.kind?.startsWith('allow'));
    if (allow) {
      log.warn('agent', 'acp-auto-approve', { note: `⚠️ ACP 后端自动批准了 ${title}`, optionId: allow.optionId });
      return { outcome: { outcome: 'selected', optionId: allow.optionId } };
    }
    // server 没给任何 allow 选项 → fail-closed 拒绝（绝不瞎选未知语义的选项）。
    const reject = req.options.find((o) => o.kind?.startsWith('reject'));
    log.warn('agent', 'acp-auto-reject', { note: `ACP 权限请求无 allow 选项，已拒绝：${title}` });
    if (reject) return { outcome: { outcome: 'selected', optionId: reject.optionId } };
    return { outcome: { outcome: 'cancelled' } };
  }

  runStreamed(input: AgentInput, _turn?: TurnOptions): AgentRun {
    // TurnOptions.model/effort 被忽略：ACP 无模型/效率参数（见 STATIC_MODELS）。
    const turnId = `acp-turn-${++this.turnSeq}-${randomUUID().slice(0, 8)}`;
    const mapper = new AcpEventMapper(turnId);
    const queue = new AsyncQueue<AgentEvent>();
    const active: ActiveTurn = { turnId, queue, mapper, aborted: false, settled: false };
    this.active = active;
    this.currentTurnId = turnId;
    this.lastActivityAt = Date.now();

    // Push the prompt NOW (runStreamed call time, not first next()) so model
    // inference overlaps the caller's card setup — codex's eager-start parity.
    this.conn
      .prompt({ sessionId: this._sessionId, prompt: [{ type: 'text', text: this.buildPromptText(input) }] })
      .then(
        (res) => this.settle(active, mapper.finish(res.stopReason, (res.usage ?? undefined) as AcpUsageLike | undefined)),
        (err) => this.settle(active, [{ type: 'error', message: `ACP 后端运行失败：${errMsg(err)}`, willRetry: false }]),
      );

    async function* gen(): AsyncGenerator<AgentEvent> {
      yield { type: 'turn_started', turnId };
      for await (const ev of queue) yield ev;
    }
    return { events: gen(), turnId: () => this.currentTurnId, lastActivity: () => this.lastActivityAt };
  }

  /** Settle a turn exactly once: flush terminal events, close the stream. */
  private settle(active: ActiveTurn, events: AgentEvent[]): void {
    if (active.settled) return;
    active.settled = true;
    this.lastActivityAt = Date.now();
    for (const ev of events) active.queue.push(ev);
    active.queue.close();
    if (this.active === active) this.active = undefined;
  }

  /** server 进程死亡/启动失败时把在飞 turn 立即收尾（turn 永不悬挂）。 */
  private failActive(message: string): void {
    const a = this.active;
    if (a) this.settle(a, [{ type: 'error', message, willRetry: false }]);
  }

  private buildPromptText(input: AgentInput): string {
    let text = input.text ?? '';
    // 入站图片已被桥落盘为本地文件；交互式 Claude Code 可用 Read 工具读图——把
    // 路径织进文本（与 claude-sdk 同策略；ACP image block 需 base64 物化，后续再说）。
    if (input.images?.length) {
      const lines = input.images.map((p) => `- ${p}`).join('\n');
      text = `${text}\n\n[用户随消息发来 ${input.images.length} 张图片，已保存为本地文件，请用 Read 工具查看：]\n${lines}`;
    }
    if (!this.instructionsSent) {
      this.instructionsSent = true;
      // ACP 没有 developerInstructions/systemPrompt 注入通道——唯一通道是用户
      // 消息本身：新会话首条消息前置带标记的桥接输出约定（仅一次）。
      text = `[飞书桥系统约定（非用户消息）]\n${BRIDGE_DEVELOPER_INSTRUCTIONS}\n[/飞书桥系统约定]\n\n${text}`;
    }
    return text;
  }

  runGoal(_objective: string): AgentRun {
    throw notSupported(' /goal 自治目标');
  }

  async clearGoal(): Promise<void> {
    // ACP 没有 goal 概念——「没有目标可清」即正确的完整实现（调用方 best-effort）。
  }

  async steer(_input: AgentInput, _expectedTurnId: string): Promise<void> {
    // ACP 的 turn 内不能注入输入；orchestrator 的失败路径自动落回排队。
    throw notSupported('运行中引导（steer），消息将排队为下一轮');
  }

  async abort(turnId: string): Promise<void> {
    const a = this.active;
    if (a && a.turnId === turnId) a.aborted = true;
    try {
      // session/cancel 是通知；确认点 = prompt 响应 stopReason 'cancelled' → done。
      await this.conn.cancel({ sessionId: this._sessionId });
    } catch {
      // server 已死 → exit handler 会把 turn 收尾
    }
  }

  async compact(): Promise<CompactResult> {
    throw notSupported(' /compact 手动压缩');
  }

  isAlive(): boolean {
    return !this.childExited && !this.closedByUs;
  }

  async close(): Promise<void> {
    this.closedByUs = true;
    try {
      this.child?.stdin?.end(); // stdio EOF → server 的优雅退出窗口
    } catch {
      // already closed
    }
    try {
      // claude-pty-acp 的 SIGTERM handler 会连带杀掉子 claude + 清理 socket。
      this.child?.kill('SIGTERM');
    } catch {
      // already dead
    }
  }
}

export class AcpBackend implements AgentBackend {
  readonly id = 'claude-acp';
  readonly displayName = 'Claude（订阅·ACP）';
  readonly capabilities = CAPABILITIES;
  /** 仅「完全访问」档：bridge 隔着 ACP 无法保证对端的只读/写界约束（qa/write 的
   * 读限制 codex 靠内核沙箱兜底，ACP server 给不了等价保证）。声明给切换 UI 提前
   * 拦截；硬守卫是 {@link assertFullMode}（startThread/resumeThread，fail-closed）。 */
  readonly supportedModes = ['full'] as const;

  /** doctor 握手探活缓存（成功才缓存；force 重探）。key = 完整命令行。 */
  private doctorCache = new Map<string, BackendProbe>();

  /** `serverCommand`：undefined → 自动解析（配置覆盖 → PATH）；显式对象 → 直接
   * 使用（单测 mock server / probe 脚本注入）；显式 null → 视为未找到（单测）。 */
  constructor(private readonly serverCommand?: AcpServerCommand | null) {}

  private async resolveServer(force?: boolean): Promise<AcpServerCommand | null> {
    if (this.serverCommand !== undefined) return this.serverCommand;
    return resolveAcpCommand({ force });
  }

  async isAvailable(): Promise<boolean> {
    return (await this.doctor()).ok;
  }

  async doctor(opts?: { force?: boolean }): Promise<BackendProbe> {
    const server = await this.resolveServer(opts?.force).catch(() => null);
    if (!server) {
      return {
        ok: false,
        version: null,
        hint: '未检测到 claude-pty-acp：npm i -g claude-pty-acp，或在配置 preferences.acpCommand 指定启动命令（如 {"command":"node","args":["/path/claude-pty-acp/dist/index.js"]}）',
      };
    }
    const key = [server.command, ...server.args].join(' ');
    if (!opts?.force) {
      const hit = this.doctorCache.get(key);
      if (hit) return hit;
    }
    const probe = await handshakeProbe(server, key);
    if (probe.ok) this.doctorCache.set(key, probe);
    return probe;
  }

  async listModels(): Promise<ModelInfo[]> {
    return STATIC_MODELS;
  }

  async listThreads(_cwd: string, _limit?: number): Promise<ThreadSummary[]> {
    throw notSupported(' /resume 历史会话');
  }

  async readHistory(_cwd: string, _sessionId: string, _maxTurns?: number): Promise<ThreadHistory> {
    // 接口契约：never throws（resume 卡兜底）。能力守卫下不可达，仍按契约返回空。
    return { turns: [], totalTurns: 0 };
  }

  async startThread(opts: StartThreadOptions): Promise<AgentThread> {
    assertFullMode(opts); // 守卫在命令解析/spawn 之前
    const server = await this.resolveServer();
    if (!server) throw new Error(noServerError());
    const thread = new AcpThread(opts, server);
    await thread.connect();
    return thread;
  }

  async resumeThread(opts: ResumeThreadOptions): Promise<AgentThread> {
    // ACP session/load（重启恢复路径）。server 不支持/会话不存在时 connect() 抛
    // 清晰错误，resolveThread 的 catch 落回「新线程 + 全量话题回织」。
    assertFullMode(opts);
    const server = await this.resolveServer();
    if (!server) throw new Error(noServerError());
    const thread = new AcpThread(opts, server, opts.sessionId);
    await thread.connect();
    return thread;
  }
}

function noServerError(): string {
  return '未检测到 claude-pty-acp（ACP server）。请 npm i -g claude-pty-acp，或在配置 preferences.acpCommand 指定启动命令。';
}

/** 轻探活：spawn server → ACP initialize 握手 → 读 agentInfo 版本 → 杀掉。
 * 全程异步、绝不抛错（doctor 契约）。 */
async function handshakeProbe(server: AcpServerCommand, location: string): Promise<BackendProbe> {
  let child: ChildProcess | undefined;
  try {
    const sdk = await import('@agentclientprotocol/sdk');
    child = spawnProcess(server.command, server.args, { stdio: ['pipe', 'pipe', 'ignore'] });
    child.on('error', () => undefined); // ENOENT 等 → initialize 超时归一处理
    if (!child.stdin || !child.stdout) throw new Error('子进程缺少 stdio 管道');
    const stream = sdk.ndJsonStream(
      Writable.toWeb(child.stdin) as WritableStream<Uint8Array>,
      Readable.toWeb(child.stdout) as ReadableStream<Uint8Array>,
    );
    const conn = new sdk.ClientSideConnection(
      () => ({
        sessionUpdate: async () => undefined,
        requestPermission: async () => ({ outcome: { outcome: 'cancelled' as const } }),
      }),
      stream,
    );
    const init = await withTimeout(
      conn.initialize({
        protocolVersion: sdk.PROTOCOL_VERSION,
        clientInfo: { name: 'feishu-codex-bridge', version: bridgeVersion() },
        clientCapabilities: { fs: { readTextFile: false, writeTextFile: false }, terminal: false },
      }),
      DOCTOR_TIMEOUT_MS,
      'ACP initialize',
    );
    return { ok: true, version: init.agentInfo?.version ?? null, location };
  } catch (err) {
    return {
      ok: false,
      version: null,
      location,
      hint: `命令已找到但 ACP 握手失败：${errMsg(err)}（确认它是一个 ACP server；必要时在配置 preferences.acpCommand 修正）`,
    };
  } finally {
    try {
      child?.kill('SIGTERM');
    } catch {
      // already dead
    }
  }
}

/** fail-closed（contract: StartThreadOptions.mode）：qa/write 档绝不降级为完全
 * 访问——直接拒绝启动并说明原因（与 claude-sdk 同款守卫）。 */
function assertFullMode(opts: StartThreadOptions): void {
  if ((opts.mode ?? 'full') !== 'full') {
    throw new Error(
      'ACP 后端目前仅支持「完全访问」权限档：bridge 无法保证 ACP server 侧的只读约束，已拒绝启动（绝不静默降级）。请把项目权限档切回「完全访问」或改用 codex 后端。',
    );
  }
}
