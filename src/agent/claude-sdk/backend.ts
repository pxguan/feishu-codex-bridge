import { randomUUID } from 'node:crypto';
import type { ChildProcess } from 'node:child_process';
import type { Options, Query, SDKMessage, SDKUserMessage, SpawnedProcess } from '@anthropic-ai/claude-agent-sdk';
import { spawnProcess } from '../../platform/spawn';
import { log } from '../../core/logger';
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
import { BRIDGE_DEVELOPER_INSTRUCTIONS } from '../bridge-instructions';
import { ClaudeEventMapper, type SdkMessageLike } from './event-map';

/**
 * Claude Code backend via the official Agent SDK (@anthropic-ai/claude-agent-sdk).
 *
 * MINIMAL SLICE (multi-backend step 1, see research/05 + synthesis L-1): only
 * isAvailable / listModels / startThread / resumeThread / runStreamed / abort
 * are real. Every codex-only capability is hard-guarded — `capabilities` flags
 * them false AND the methods throw a clear "not supported" error (never a
 * silent half-implementation):
 *   - goal/steer/compact → throw (orchestrator surfaces ❌ / falls back to queue)
 *   - resume PICKER (listThreads/readHistory) → throw / empty. resumeThread
 *     itself IS implemented (M-8): SessionRecord carries the backend id, so a
 *     daemon restart routes back here and the SDK-native `resume:` continues
 *     the SAME session id with prior context (probed on-machine 2026-06; no
 *     fork → no id drift to persist). A missing/invalid session id surfaces as
 *     a clear connect failure → resolveThread falls back to a fresh thread +
 *     full topic re-weave (graceful degrade).
 *
 * Process model mirrors codex: ONE long-lived CLI child per thread (streaming-
 * input query), so supervisor/watchdog/⏹ semantics stay transparent. The SDK
 * spawns its bundled binary through OUR cross-spawn wrapper
 * (`spawnClaudeCodeProcess` option) — same Windows `.cmd`/EINVAL hardening as
 * every other spawn in this repo (platform/spawn).
 *
 * TODO(权限映射): only the 'full' tier is supported (bypassPermissions). The
 * qa→dontAsk+readonly-allowedTools / write→acceptEdits mapping is future work;
 * until then qa/write FAIL CLOSED below (never downgrade a confined project to
 * full access). Note even the future mapping is approval-layer enforcement,
 * weaker than codex's Seatbelt kernel sandbox — document honestly when built.
 */

/**
 * Startup probe window. In stream-json input mode the CLI emits NOTHING until
 * the first user message (probed 2026-06: no `system/init` before input), so
 * "did it start ok" is decided by racing the first read against this timer:
 * startup failures (bad flag / bad binary / instant exit) reject the read
 * within ~0.5s; silence for the whole window ⇒ the CLI is up awaiting input.
 */
const STARTUP_PROBE_MS = 1_500;

// `resume: false` 守卫的是 /resume 选择卡（listThreads/readHistory 未实现）；
// resumeThread 本身已实现 —— 重启恢复路径（resolveThread）不经此能力位。
// `approvals: false`：审批转发（canUseTool → approval_request）是后续切片，
// 当前 bypassPermissions 下不会发审批。
const CAPABILITIES: AgentCapabilities = {
  goal: false,
  steer: false,
  compact: false,
  resume: false,
  approvals: false,
};

/** Model aliases the claude CLI resolves itself (`--model sonnet|opus|haiku`).
 * Claude has no codex-style reasoning-effort axis → supportedEfforts empty;
 * the 'medium' defaultEffort only satisfies pickDefault and is ignored here. */
const STATIC_MODELS: ModelInfo[] = [
  {
    id: 'sonnet',
    displayName: 'Claude Sonnet',
    description: 'Claude Code 默认模型（别名，由 CLI 解析到当前最新 Sonnet）',
    hidden: false,
    isDefault: true,
    supportedEfforts: [],
    defaultEffort: 'medium',
  },
  {
    id: 'opus',
    displayName: 'Claude Opus',
    description: '更强推理（订阅用量消耗更快）',
    hidden: false,
    isDefault: false,
    supportedEfforts: [],
    defaultEffort: 'medium',
  },
];

/** Simple async queue (push from callers, async-iterate once) — the SDK's
 * streaming-input `prompt`. Same shape as app-server-client's AsyncQueue. */
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
  return new Error(`Claude 后端暂不支持${what}（codex 专属能力，已按能力守卫拒绝）`);
}

class ClaudeSdkThread implements AgentThread {
  /** New thread: bridge-assigned session UUID, forced onto the CLI via
   * `--session-id` so the handle exists BEFORE any turn runs (stream-json mode
   * emits no init until the first input — see STARTUP_PROBE_MS). Resumed
   * thread: the PRIOR session's id — the SDK `resume:` continues the SAME id
   * (probed: no fork), so the persisted SessionRecord stays valid as-is. */
  readonly sessionId: string;
  /** resume mode (connect passes `resume:` instead of pre-assigning the id) */
  private readonly resuming: boolean;
  private readonly input = new AsyncQueue<SDKUserMessage>();
  private q!: Query;
  private iter!: AsyncIterator<SDKMessage>;
  /** the startup probe's in-flight read — the FIRST message of turn 1 arrives
   * on this promise; nextMessage() consumes it exactly once. */
  private pendingNext: Promise<IteratorResult<SDKMessage>> | undefined;
  private child: ChildProcess | undefined;
  private childExited = false;
  private closedByUs = false;
  private turnSeq = 0;
  private currentTurnId: string | undefined;
  /** Turns that ended locally (⏹ / idle watchdog) WITHOUT consuming their
   * terminal `result` leave it in the shared stream; the next turn must eat
   * exactly that many stale results (and everything before them) or it would
   * read a premature `done`. Mirrors the codex compact()-drain rationale. */
  private staleResults = 0;

  constructor(
    private readonly opts: StartThreadOptions,
    resumeSessionId?: string,
  ) {
    this.sessionId = resumeSessionId ?? randomUUID();
    this.resuming = resumeSessionId !== undefined;
  }

  /** Spawn the CLI (via query()) and probe that it came up. */
  async connect(): Promise<void> {
    const { query } = await import('@anthropic-ai/claude-agent-sdk');
    const options: Options = {
      cwd: this.opts.cwd,
      ...(this.opts.model ? { model: this.opts.model } : {}),
      // New thread: pre-assign the session id (CLI `--session-id`, probed
      // accepted) so the AgentThread handle is real from t0 without waiting for
      // any message. Resume: SDK-native `resume:` loads the prior conversation
      // and CONTINUES the same session id (probed on-machine: id stable across
      // resumes, context recalled — no forkSession, so nothing drifts).
      ...(this.resuming ? { resume: this.sessionId } : { extraArgs: { 'session-id': this.sessionId } }),
      // TODO(权限映射): full 档专用 — see the module doc. qa/write were already
      // rejected in startThread(); 'full' matches codex's danger-full-access.
      permissionMode: 'bypassPermissions',
      allowDangerouslySkipPermissions: true,
      // token 级增量（SDKPartialAssistantMessage）→ 飞书卡片打字机
      includePartialMessages: true,
      // Claude Code 本体行为（工具/代理循环）+ 桥的两条输出约定（与 codex 的
      // developerInstructions 同一段文案，见 ../bridge-instructions）。
      systemPrompt: { type: 'preset', preset: 'claude_code', append: BRIDGE_DEVELOPER_INSTRUCTIONS },
      // 项目级配置（CLAUDE.md / .claude/settings.json）照常生效 —— 与 codex 读
      // AGENTS.md 的行为对齐；用户/全局配置不读，避免把 owner 个人配置带进群聊。
      settingSources: ['project'],
      // 所有子进程统一走 cross-spawn 封装（Windows .cmd shim / EINVAL 修复），
      // 这是仓库的硬约束（platform/spawn）。child 引用同时喂 isAlive()。
      spawnClaudeCodeProcess: (spawnOpts) => {
        const child = spawnProcess(spawnOpts.command, spawnOpts.args, {
          cwd: spawnOpts.cwd,
          env: spawnOpts.env,
          stdio: ['pipe', 'pipe', 'pipe'],
          // forwarded signal: fires AFTER the SDK's graceful stdin-EOF window
          signal: spawnOpts.signal,
        });
        this.child = child;
        log.info('agent', 'claude-spawn', { pid: child.pid ?? null, cwd: this.opts.cwd });
        // Drain stderr (an unread pipe would backpressure-block the CLI).
        child.stderr?.on('data', (d: Buffer) => {
          const line = d.toString('utf8').trim();
          if (line) log.warn('agent', 'claude-stderr', { line: line.slice(0, 200) });
        });
        child.on('exit', (code, signal) => {
          this.childExited = true;
          log.info('agent', 'claude-exit', { pid: child.pid ?? null, code, signal });
        });
        child.on('error', () => {
          this.childExited = true;
        });
        // ChildProcess satisfies SpawnedProcess (SDK docs), modulo stdin/stdout
        // nullability — ours are non-null ('pipe' stdio above).
        return child as SpawnedProcess;
      },
    };
    this.q = query({ prompt: this.input[Symbol.asyncIterator](), options });
    this.iter = this.q[Symbol.asyncIterator]();

    // Startup probe: in stream-json input mode the CLI is silent until the
    // first user message, so a read that REJECTS (or ends) inside the window
    // means startup failed (bad binary / bad flag / instant exit — probed to
    // surface within ~0.5s); silence for the whole window means it's up. The
    // pending read is kept — it will deliver turn 1's first message.
    const first = this.iter.next();
    this.pendingNext = first;
    const verdict = await Promise.race([
      first.then(
        (step) => (step.done ? ('exited' as const) : step),
        () => 'failed' as const,
      ),
      new Promise<'silent'>((resolve) => {
        const t = setTimeout(() => resolve('silent'), STARTUP_PROBE_MS);
        first.then(
          () => clearTimeout(t),
          () => clearTimeout(t),
        );
      }),
    ]);
    if (verdict === 'failed' || verdict === 'exited') {
      const reason = await first.then(
        () => 'Claude CLI 启动后立即退出',
        (e: unknown) => (e instanceof Error ? e.message : String(e)),
      );
      await this.close().catch(() => undefined);
      throw new Error(`Claude 后端启动失败：${reason}（请检查 claude 登录态 / ANTHROPIC_API_KEY）`);
    }
    // A pre-input MESSAGE is normally turn 1's first event (left pending for the
    // run loop) — EXCEPT an error result, which is how startup failures that the
    // CLI survives long enough to report surface in stream-json mode. Probed: a
    // missing/invalid `resume` session id yields an immediate
    // result{subtype:'error_during_execution', is_error:true} before any input.
    // Consume it and fail connect so resolveThread's catch can fall back.
    if (verdict !== 'silent') {
      const m = verdict.value as SdkMessageLike & { is_error?: boolean; subtype?: string; result?: string };
      if (m.type === 'result' && m.is_error) {
        this.pendingNext = undefined; // it IS the failure, not turn data
        await this.close().catch(() => undefined);
        const what = m.result || m.subtype || 'error result';
        throw new Error(
          `Claude 后端启动失败：${what}${this.resuming ? '（待恢复的会话可能已不存在）' : ''}`,
        );
      }
    }
    // 'silent'（正常等待输入）或其他先到消息（留给第一轮消费）都算成功。
  }

  /** Read the next SDK message, consuming the startup probe's pending read first. */
  private nextMessage(): Promise<IteratorResult<SDKMessage>> {
    const p = this.pendingNext ?? this.iter.next();
    this.pendingNext = undefined;
    return p;
  }

  runStreamed(input: AgentInput, _turn?: TurnOptions): AgentRun {
    // TODO: per-turn model/effort overrides are ignored — the /model picker
    // still lists codex models only; honoring a codex model id here would 400.
    // Model is fixed at thread start until the picker is backend-aware.
    const self = this;
    let lastActivityAt = Date.now();
    const turnId = `claude-turn-${++this.turnSeq}-${randomUUID().slice(0, 8)}`;
    const mapper = new ClaudeEventMapper(turnId);
    this.currentTurnId = turnId;
    // Push the prompt NOW (runStreamed call time, not first next()) so model
    // inference overlaps the caller's card setup — codex's eager-start parity.
    this.pushUser(input);
    async function* gen(): AsyncGenerator<AgentEvent> {
      yield { type: 'turn_started', turnId };
      let sawTerminal = false;
      try {
        while (true) {
          let step: IteratorResult<SDKMessage>;
          try {
            step = await self.nextMessage();
          } catch (err) {
            // the SDK surfaces a died child as a rejected read — normalize to a
            // fatal stream event（与 codex 的 AsyncQueue close→done 行为对齐）.
            const m = err instanceof Error ? err.message : String(err);
            yield { type: 'error', message: `Claude 进程异常：${m}`, willRetry: false };
            sawTerminal = true;
            return;
          }
          if (step.done) {
            yield { type: 'error', message: 'Claude 进程已退出（崩溃或被关闭）', willRetry: false };
            sawTerminal = true;
            return;
          }
          lastActivityAt = Date.now();
          const raw = step.value as unknown as SdkMessageLike;
          // Eat a prior locally-terminated turn's leftovers up to its result.
          if (self.staleResults > 0) {
            if (raw.type === 'result') self.staleResults--;
            continue;
          }
          for (const ev of mapper.map(raw)) {
            yield ev;
            if (ev.type === 'done' || (ev.type === 'error' && !ev.willRetry)) {
              sawTerminal = true;
              return;
            }
          }
        }
      } finally {
        // Loop ended without this turn's terminal (⏹ stopSignal / watchdog /
        // consumer break): its result is still coming — mark it stale.
        if (!sawTerminal && self.isAlive()) self.staleResults++;
      }
    }
    return { events: gen(), turnId: () => self.currentTurnId, lastActivity: () => lastActivityAt };
  }

  private pushUser(input: AgentInput): void {
    let text = input.text ?? '';
    // 入站图片已被桥落盘为本地文件；Claude Code 的 Read 工具可直接读图，把路径
    // 织进文本即可“看图”。TODO: 改为 base64 image content block（原生视觉输入）。
    if (input.images?.length) {
      const lines = input.images.map((p) => `- ${p}`).join('\n');
      text = `${text}\n\n[用户随消息发来 ${input.images.length} 张图片，已保存为本地文件，请用 Read 工具查看：]\n${lines}`;
    }
    this.input.push({
      type: 'user',
      message: { role: 'user', content: text },
      parent_tool_use_id: null,
      session_id: this.sessionId,
    });
  }

  runGoal(_objective: string): AgentRun {
    throw notSupported(' /goal 自治目标');
  }

  async clearGoal(): Promise<void> {
    // claude 没有 goal 概念 —— “没有目标可清”本身就是正确的完整实现（调用方
    // 全部 best-effort .catch，此处空操作避免共享清理路径上的噪音异常）。
  }

  async steer(_input: AgentInput, _expectedTurnId: string): Promise<void> {
    // orchestrator 的 steer 失败路径会自动落回排队（handle-message try/catch）。
    throw notSupported('运行中引导（steer），消息将排队为下一轮');
  }

  async abort(_turnId: string): Promise<void> {
    // Query.interrupt() 是一等控制方法（streaming 模式），不必杀进程。
    await this.q.interrupt();
  }

  async compact(): Promise<CompactResult> {
    throw notSupported(' /compact 手动压缩（Claude Code 会自行 auto-compact）');
  }

  isAlive(): boolean {
    return !this.childExited && !this.closedByUs;
  }

  async close(): Promise<void> {
    this.closedByUs = true;
    this.input.close(); // stdin EOF → CLI 的优雅退出窗口
    try {
      this.q.close(); // 兜底强杀（SDK 内部 kill child）
    } catch {
      // already dead
    }
  }
}

export class ClaudeSdkBackend implements AgentBackend {
  readonly id = 'claude-sdk';
  readonly displayName = 'Claude Code (Agent SDK)';
  readonly capabilities = CAPABILITIES;

  async isAvailable(): Promise<boolean> {
    return (await this.doctor()).ok;
  }

  async doctor(): Promise<BackendProbe> {
    // SDK 自带平台二进制——能 import 即可跑；不真探活（登录态/网络问题在
    // startThread 的启动探针处报清晰错误）。无版本可探（SDK 不导出），留 null。
    try {
      await import('@anthropic-ai/claude-agent-sdk');
      return { ok: true, version: null, location: '@anthropic-ai/claude-agent-sdk' };
    } catch {
      return { ok: false, version: null, hint: '未安装 @anthropic-ai/claude-agent-sdk（在 bridge 目录 npm i 后重试）' };
    }
  }

  async listModels(): Promise<ModelInfo[]> {
    return STATIC_MODELS;
  }

  async listThreads(_cwd: string, _limit?: number): Promise<ThreadSummary[]> {
    throw notSupported(' /resume 历史会话');
  }

  async readHistory(_cwd: string, _sessionId: string, _maxTurns?: number): Promise<ThreadHistory> {
    // 接口契约：never throws（resume 卡兜底）。能力守卫下这里不可达，仍按契约返回空。
    return { turns: [], totalTurns: 0 };
  }

  async startThread(opts: StartThreadOptions): Promise<AgentThread> {
    assertFullMode(opts);
    const thread = new ClaudeSdkThread(sanitizeClaudeModel(opts));
    await thread.connect();
    return thread;
  }

  async resumeThread(opts: ResumeThreadOptions): Promise<AgentThread> {
    // SDK-native resume（重启恢复路径，见模块注释）。会话不存在/已损坏时
    // connect() 抛清晰错误，resolveThread 的 catch 落回「新线程 + 全量话题回织」。
    assertFullMode(opts);
    const thread = new ClaudeSdkThread(sanitizeClaudeModel(opts), opts.sessionId);
    await thread.connect();
    return thread;
  }
}

/** 本后端认识的 model id（与 STATIC_MODELS 同源）。 */
const KNOWN_MODEL_IDS = new Set(STATIC_MODELS.map((m) => m.id));

/** 防御自愈（exported for tests）：跨后端污染的持久化 model id（旧版 /model 卡
 * 未按后端路由，可能把 codex 的 'gpt-5.5' 写进 claude 会话记录）一旦传给 claude
 * CLI 的 --model，每轮/每次 resume 都报 invalid model 且群内无 UI 可修——非本
 * 后端的模型一律忽略并告警，落回 CLI 默认模型，让存量坏记录自愈。 */
export function sanitizeClaudeModel<T extends { model?: string }>(opts: T): T {
  if (!opts.model || KNOWN_MODEL_IDS.has(opts.model)) return opts;
  log.warn('agent', 'claude-model-ignored', { model: opts.model });
  return { ...opts, model: undefined };
}

/** fail-closed（contract: StartThreadOptions.mode）：在权限映射做出来之前，
 * qa/write 档绝不降级为完全访问 —— 直接拒绝启动并说明原因。 */
function assertFullMode(opts: StartThreadOptions): void {
  if ((opts.mode ?? 'full') !== 'full') {
    throw new Error(
      'Claude 后端目前仅支持「完全访问」权限档：qa/write 档到 Claude 权限模式的映射尚未实现，已拒绝启动（绝不静默降级）。请把项目权限档切回「完全访问」或改用 codex 后端。',
    );
  }
}
