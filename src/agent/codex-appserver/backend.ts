import { log } from '../../core/logger';
import type {
  AgentBackend,
  AgentEvent,
  AgentInput,
  AgentRun,
  AgentThread,
  BackendProbe,
  CompactResult,
  HistoryTool,
  HistoryTurn,
  ModelInfo,
  PermissionMode,
  ReasoningEffort,
  ResumeThreadOptions,
  StartThreadOptions,
  ThreadHistory,
  ThreadSummary,
  TurnOptions,
} from '../types';
import { isGoalTerminal } from '../types';
import { BRIDGE_DEVELOPER_INSTRUCTIONS } from '../bridge-instructions';
import { AppServerClient } from './app-server-client';
import { mapNotification } from './event-map';
import { codexVersionAsync, resolveCodexBin } from './locate';
import type { Thread, ThreadItem, Turn } from './protocol';

const APPROVAL_POLICY = 'never';

/**
 * Map a permission tier to the thread/start|resume params that enforce it.
 * 'full' (or unset) keeps the historical danger-full-access. 'qa'/'write' send a
 * custom codex permissions profile ("feishu") whose filesystem rules confine
 * BOTH reads and writes to the workspace roots (cwd). The profile is platform-
 * agnostic config; codex translates it to whatever OS sandbox the host has:
 *   - macOS  → Seatbelt (verified: thread/start reports activePermissionProfile
 *     .id="feishu", reads outside cwd like ~/.ssh are denied).
 *   - Windows → WindowsRestrictedToken (the elevated backend enforces deny-read;
 *     an unelevated one that can't enforce it refuses to run — never leaks).
 * `:minimal` keeps the read access codex needs to run commands at all.
 *
 * fail-closed: on Linux / WSL codex's sandbox only ro-binds the disk (writes
 * blocked, READS still open — Landlock read-restriction is unimplemented) AND it
 * does NOT refuse, so a privacy tier there would silently run unconfined. We must
 * NEVER do that — so 'qa'/'write' are gated to macOS + Windows; on any other
 * platform we throw BEFORE spawn (a clear run error, never a downgrade).
 *
 * NOTE (Windows): enforcement is codex's, not ours — verify on a real Windows
 * host (ask the bot to read a file outside cwd → it must refuse) before trusting
 * the read-only tiers with an untrusted external group.
 */
/**
 * Auto-compact "off" sentinel. codex resolves its auto-compact threshold as
 * `config.model_auto_compact_token_limit → model default → i64::MAX` (codex-rs
 * core/session/turn.rs), so setting a limit no real session reaches disables it.
 * 1e9 is safely inside JS's integer range (i64::MAX would lose JSON precision)
 * and far past any model's context window. */
const AUTO_COMPACT_OFF_LIMIT = 1_000_000_000;

/** Merge codex's auto-compact disable into thread/start|resume params when the
 * project turned it off; ON (default/undefined) leaves codex's own default. */
export function withAutoCompact(
  params: Record<string, unknown>,
  autoCompact: boolean | undefined,
): Record<string, unknown> {
  if (autoCompact !== false) return params;
  const config = (params.config as Record<string, unknown> | undefined) ?? {};
  return { ...params, config: { ...config, model_auto_compact_token_limit: AUTO_COMPACT_OFF_LIMIT } };
}

export function sandboxParams(
  mode: PermissionMode | undefined,
  network: boolean | undefined,
): Record<string, unknown> {
  if ((mode ?? 'full') === 'full') return { sandbox: 'danger-full-access' };
  if (process.platform !== 'darwin' && process.platform !== 'win32') {
    throw new Error(
      '「项目内只读 / 项目内读写」靠操作系统沙箱把读写锁进项目文件夹，目前只有 macOS 与原生 Windows 能强制执行。当前平台（Linux / WSL 只挡写、不限制读取，无法保证不泄露隐私）已拒绝启动（绝不降级为完全访问）。请改用「完全访问」、把 Codex 跑进容器/隔离环境，或在 macOS / Windows 上运行。',
    );
  }
  return {
    config: {
      default_permissions: 'feishu',
      permissions: {
        feishu: {
          filesystem: {
            ':minimal': 'read',
            ':workspace_roots': { '.': mode === 'write' ? 'write' : 'read' },
          },
          network: { enabled: Boolean(network) },
        },
      },
    },
  };
}

/** Hard ceiling on a history read so a wedged codex can't hang the resume card. */
const READ_HISTORY_TIMEOUT_MS = 20_000;

/** Hard ceiling on a manual compaction (an LLM summarization turn) so a wedged
 * codex can't hang the "压缩中" card forever. */
const COMPACT_TIMEOUT_MS = 120_000;

/** Reject `p` if it hasn't settled within `ms` (the timer never keeps the event
 * loop alive past resolution). */
function withDeadline<T>(p: Promise<T>, ms: number, label: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const t = setTimeout(() => reject(new Error(`${label} timed out after ${ms}ms`)), ms);
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

function toUserInput(input: AgentInput): unknown[] {
  const out: unknown[] = [];
  if (input.text) out.push({ type: 'text', text: input.text, text_elements: [] });
  for (const path of input.images ?? []) out.push({ type: 'localImage', path });
  return out;
}

class CodexThread implements AgentThread {
  private currentTurnId: string | undefined;

  constructor(
    private readonly client: AppServerClient,
    readonly sessionId: string,
    private model: string | undefined,
    private effort: ReasoningEffort | undefined,
  ) {}

  runStreamed(input: AgentInput, turn?: TurnOptions): AgentRun {
    const self = this;
    this.currentTurnId = undefined;
    // Per-turn overrides persist for subsequent turns (matches turn/start semantics).
    if (turn?.model) this.model = turn.model;
    if (turn?.effort) this.effort = turn.effort;
    // Liveness clock for the idle watchdog: refreshed on EVERY raw notification
    // below (even ones mapNotification drops, like command output deltas), so a
    // long-running shell command doesn't read as "wedged".
    let lastActivityAt = Date.now();
    const params: Record<string, unknown> = {
      threadId: self.sessionId,
      input: toUserInput(input),
    };
    if (self.model) params.model = self.model;
    if (self.effort) params.effort = self.effort;

    // Fire turn/start NOW — at runStreamed() call time, NOT lazily on the first
    // next() — so model inference runs in parallel with the caller's card setup
    // (stream.create + adoptThreadId cost 2-3 RTTs before the for-await begins).
    // Early notifications buffer in the client's AsyncQueue, so nothing is lost.
    // The caller owns the new failure mode (card setup throws after the turn
    // started): launchRun aborts+closes the thread on that path.
    //
    // turn/start stays in flight for the whole turn (events arrive via
    // notifications), so we can't await it up front. But if it *rejects* —
    // bad params, thread gone, auth failure — codex emits no notification
    // that maps to done/error, so the stream loop below would block until the
    // idle watchdog fires and the user sees a bogus "已超时" instead of the
    // real cause. Race the rejection against the stream and surface it. (A
    // clean child exit closes the stream on its own, ending the loop.)
    let startError: Error | undefined;
    const startFailed: Promise<'start-failed'> = new Promise((resolve) => {
      self.client.request('turn/start', params).then(undefined, (err: unknown) => {
        startError = err instanceof Error ? err : new Error(String(err));
        log.fail('agent', startError, { phase: 'turn/start' });
        resolve('start-failed');
      });
    });
    async function* gen(): AsyncGenerator<AgentEvent> {
      const stream = self.client.stream()[Symbol.asyncIterator]();
      while (true) {
        const step = await Promise.race([stream.next(), startFailed]);
        if (step === 'start-failed') {
          yield { type: 'error', message: startError?.message ?? 'turn/start 请求失败', willRetry: false };
          return;
        }
        if (step.done) return;
        lastActivityAt = Date.now();
        const ev = mapNotification(step.value);
        if (!ev) continue;
        if (ev.type === 'turn_started') self.currentTurnId = ev.turnId;
        yield ev;
        if (ev.type === 'done') return;
        if (ev.type === 'error' && !ev.willRetry) return;
      }
    }
    return { events: gen(), turnId: () => self.currentTurnId, lastActivity: () => lastActivityAt };
  }

  runGoal(objective: string): AgentRun {
    const self = this;
    this.currentTurnId = undefined;
    // Same liveness clock as runStreamed — the goal's 30min idle backstop must
    // also see raw activity, not just mapped events.
    let lastActivityAt = Date.now();
    async function* gen(): AsyncGenerator<AgentEvent> {
      // Clear any leftover goal on this thread FIRST. codex keeps a goal attached
      // even after it completes and re-broadcasts it on every resume (verified);
      // worse, a thread/goal/set whose objective is IDENTICAL to an already-complete
      // goal is a NO-OP — so re-running the same goal would do nothing and report
      // stale stats. And a leftover ACTIVE goal (from a crashed/killed run, or
      // pre-fix dirty data) auto-continues on resume. runGoal only runs when STARTING
      // a fresh goal (a busy session is gated out upstream), so any goal currently on
      // the thread is leftover — clearing it guarantees the set below makes a fresh,
      // actually-running goal and self-heals every leftover case.
      await self.client.request('thread/goal/clear', { threadId: self.sessionId }).catch(() => undefined);

      // thread/goal/set registers the goal AND auto-starts the first turn (codex
      // idle-continuation) — verified on 0.139, so we never call turn/start; codex
      // drives every turn. Race the set rejection so a disabled-feature / bad-param
      // error surfaces instead of hanging (mirrors runStreamed's start-race).
      let setError: Error | undefined;
      const setFailed: Promise<'set-failed'> = new Promise((resolve) => {
        self.client
          .request('thread/goal/set', { threadId: self.sessionId, objective })
          .then(undefined, (err: unknown) => {
            setError = err instanceof Error ? err : new Error(String(err));
            log.fail('agent', setError, { phase: 'thread/goal/set' });
            resolve('set-failed');
          });
      });

      const stream = self.client.stream()[Symbol.asyncIterator]();
      // Guard against a STALE goal snapshot: resuming a thread that had a prior
      // goal re-emits a thread/goal/updated for THAT goal (often already complete)
      // around resume time — before ours runs. If we honored it we'd "complete"
      // instantly with the old goal's stats and never do the work. So: ignore
      // goal_updates whose objective isn't ours, and don't honor a terminal status
      // until our goal has actually started (a turn started, or it went active).
      let armed = false;
      let turnActive = false;
      let goalDone = false; // a terminal goal status was seen; drain the live turn, then stop
      while (true) {
        const step = await Promise.race([stream.next(), setFailed]);
        if (step === 'set-failed') {
          yield { type: 'error', message: setError?.message ?? 'thread/goal/set 请求失败', willRetry: false };
          return;
        }
        if (step.done) return;
        lastActivityAt = Date.now();
        const ev = mapNotification(step.value);
        if (!ev) continue;
        if (ev.type === 'turn_started') {
          self.currentTurnId = ev.turnId;
          armed = true; // a real turn for our goal is running
          turnActive = true;
          yield ev;
          continue;
        }
        if (ev.type === 'done') {
          turnActive = false;
          yield ev;
          // The goal is terminal AND its final turn just finished — now stop.
          if (goalDone) return;
          continue;
        }
        if (ev.type === 'goal_update') {
          if (ev.objective !== objective) continue; // stale snapshot for a different goal
          if (ev.status === 'active' || ev.status === 'paused') armed = true;
          yield ev;
          // A goal spans many auto-continued turns — a per-turn `done` is NOT the
          // end. On a terminal goal status: codex emits update_goal(complete) BEFORE
          // the model's closing answer (verified — the final agentMessage arrives a
          // couple seconds AFTER goal/complete), so returning here would cut the
          // result off. If a turn is in flight, keep consuming until its turn/completed
          // so the final answer renders; otherwise stop now.
          if (armed && isGoalTerminal(ev.status)) {
            if (turnActive) goalDone = true;
            else return;
          }
          continue;
        }
        yield ev;
        if (ev.type === 'error' && !ev.willRetry) return; // a fatal error kills the run
      }
    }
    return { events: gen(), turnId: () => self.currentTurnId, lastActivity: () => lastActivityAt };
  }

  async clearGoal(): Promise<void> {
    await this.client.request('thread/goal/clear', { threadId: this.sessionId });
  }

  async steer(input: AgentInput, expectedTurnId: string): Promise<void> {
    await this.client.request('turn/steer', {
      threadId: this.sessionId,
      expectedTurnId,
      input: toUserInput(input),
    });
  }

  async abort(turnId: string): Promise<void> {
    await this.client.request('turn/interrupt', { threadId: this.sessionId, turnId });
  }

  async compact(): Promise<CompactResult> {
    // thread/compact/start only ACKS the kickoff; compaction then runs as a
    // background turn and ends with turn/completed (done). We MUST drain the
    // stream to that terminal — both so the caller knows compaction truly
    // finished (its "压缩中" card can flip to "压缩完成"), and so a trailing
    // turn/completed doesn't leak into the NEXT real turn's stream (which would
    // read a premature `done` and reply "未返回内容"). Mirrors runStreamed's
    // start-race so an immediate rejection (e.g. unsupported on old codex)
    // surfaces instead of hanging.
    let startError: Error | undefined;
    const startFailed: Promise<'start-failed'> = new Promise((resolve) => {
      this.client.request('thread/compact/start', { threadId: this.sessionId }).then(undefined, (err: unknown) => {
        startError = err instanceof Error ? err : new Error(String(err));
        log.fail('agent', startError, { phase: 'thread/compact/start' });
        resolve('start-failed');
      });
    });
    let timer: ReturnType<typeof setTimeout> | undefined;
    const timeout: Promise<'timeout'> = new Promise((resolve) => {
      timer = setTimeout(() => resolve('timeout'), COMPACT_TIMEOUT_MS);
    });

    const stream = this.client.stream()[Symbol.asyncIterator]();
    let compacted = false;
    let usage: CompactResult['usage'] = null;
    try {
      while (true) {
        const step = await Promise.race([stream.next(), startFailed, timeout]);
        if (step === 'start-failed') throw startError ?? new Error('thread/compact/start 请求失败');
        if (step === 'timeout') throw new Error(`压缩超时（codex 未在 ${COMPACT_TIMEOUT_MS / 1000}s 内完成）`);
        if (step.done) break;
        const ev = mapNotification(step.value);
        if (!ev) continue;
        if (ev.type === 'context_usage') usage = { usedTokens: ev.usedTokens, contextWindow: ev.contextWindow };
        else if (ev.type === 'context_compacted') compacted = true;
        else if (ev.type === 'error' && !ev.willRetry) throw new Error(ev.message);
        else if (ev.type === 'done') break;
      }
    } finally {
      if (timer) clearTimeout(timer);
    }
    return { compacted, usage };
  }

  isAlive(): boolean {
    return !this.client.exited;
  }

  async close(): Promise<void> {
    await this.client.close();
  }
}

export class CodexAppServerBackend implements AgentBackend {
  readonly id = 'codex-appserver';
  readonly displayName = 'Codex (app-server)';
  private modelCache: ModelInfo[] | null = null;

  async isAvailable(): Promise<boolean> {
    return (await this.doctor()).ok;
  }

  async doctor(opts?: { force?: boolean }): Promise<BackendProbe> {
    // async 版本探测：DM 体检等卡片回调会 await 这里，同步 spawn 会冻结事件循环。
    // force 绕过 locate 模块缓存重新探测（体检要看「现在」的状态）。
    const probe = opts?.force ? { force: true as const } : undefined;
    const bin = resolveCodexBin(probe);
    if (!bin) {
      return {
        ok: false,
        version: null,
        hint: '未找到。设置 CODEX_BIN，或安装 @openai/codex，或装 Codex.app',
      };
    }
    const version = await codexVersionAsync(bin, probe);
    if (!version) {
      return { ok: false, version: null, location: bin, hint: `codex --version 执行失败（${bin}）` };
    }
    return { ok: true, version, location: bin };
  }

  async listModels(): Promise<ModelInfo[]> {
    if (this.modelCache) return this.modelCache;
    const bin = resolveCodexBin();
    if (!bin) return STATIC_MODELS;
    const client = new AppServerClient({ bin, cwd: process.cwd(), clientName: 'feishu-codex-bridge-models' });
    try {
      await client.connect();
      const res = await client.request<{ data?: RawModel[] }>('model/list', { limit: 50 });
      const models = (res.data ?? []).map(mapModel);
      this.modelCache = models.length ? models : STATIC_MODELS;
      return this.modelCache;
    } catch (err) {
      log.fail('agent', err, { phase: 'model/list' });
      return STATIC_MODELS;
    } finally {
      await client.close();
    }
  }

  async listThreads(cwd: string, limit = 15): Promise<ThreadSummary[]> {
    const bin = resolveCodexBin();
    if (!bin) return [];
    const client = new AppServerClient({ bin, cwd, clientName: 'feishu-codex-bridge-threads' });
    try {
      await client.connect();
      const res = await client.request<{ data?: RawThread[] }>('thread/list', {
        cwd,
        limit,
        sortKey: 'created_at',
        sortDirection: 'desc',
      });
      return (res.data ?? [])
        .filter((t) => !t.ephemeral)
        .map((t) => ({
          sessionId: t.id,
          preview: t.preview ?? '',
          createdAt: t.createdAt ?? 0,
          updatedAt: t.updatedAt ?? t.createdAt ?? 0,
          name: t.name ?? undefined,
        }));
    } catch (err) {
      log.fail('agent', err, { phase: 'thread/list' });
      return [];
    } finally {
      await client.close();
    }
  }

  async readHistory(cwd: string, sessionId: string, maxTurns = 10): Promise<ThreadHistory> {
    const empty: ThreadHistory = { turns: [], totalTurns: 0 };
    const bin = resolveCodexBin();
    if (!bin) return empty;
    // Short-lived client (same spawn→connect→request→close shape as listThreads).
    // thread/read does NOT start a turn or load the thread live — it just reads
    // the rollout, so there's no process to keep and no token cost. The session
    // is resumed lazily on the topic's first message via resolveThread.
    const client = new AppServerClient({ bin, cwd, clientName: 'feishu-codex-bridge-history' });
    try {
      // Bound the whole connect+read: if codex hangs, time out → catch → finally
      // close() (which SIGKILLs the child), so no orphan and the card resolves.
      const read = (async () => {
        await client.connect();
        return client.request<{ thread: Thread }>('thread/read', { threadId: sessionId, includeTurns: true });
      })();
      read.catch(() => undefined); // close() may reject this late; swallow it
      const res = await withDeadline(read, READ_HISTORY_TIMEOUT_MS, 'thread/read');
      const thread = res.thread;
      const all = (Array.isArray(thread?.turns) ? thread.turns : [])
        .map(mapTurn)
        .filter((t) => t.userText || t.assistantText || t.tools.length);
      const totalTurns = all.length;
      const turns = totalTurns > maxTurns ? all.slice(totalTurns - maxTurns) : all;
      return {
        turns,
        totalTurns,
        name: thread?.name ?? undefined,
        preview: thread?.preview ?? undefined,
        createdAt: thread?.createdAt,
        updatedAt: thread?.updatedAt,
      };
    } catch (err) {
      log.fail('agent', err, { phase: 'thread/read', sessionId });
      return empty;
    } finally {
      await client.close();
    }
  }

  async startThread(opts: StartThreadOptions): Promise<AgentThread> {
    // Build sandbox params first — the platform fail-closed guard throws here,
    // before we spawn, so a rejected tier leaves no orphan app-server process.
    const sandbox = withAutoCompact(sandboxParams(opts.mode, opts.network), opts.autoCompact);
    const client = await this.spawn(opts.cwd);
    const res = await client.request<{ thread: { id: string } }>('thread/start', {
      cwd: opts.cwd,
      approvalPolicy: APPROVAL_POLICY,
      ...sandbox,
      developerInstructions: BRIDGE_DEVELOPER_INSTRUCTIONS,
      ...(opts.model ? { model: opts.model } : {}),
    });
    return new CodexThread(client, res.thread.id, opts.model, opts.effort);
  }

  async resumeThread(opts: ResumeThreadOptions): Promise<AgentThread> {
    const sandbox = withAutoCompact(sandboxParams(opts.mode, opts.network), opts.autoCompact);
    const client = await this.spawn(opts.cwd);
    const res = await client.request<{ thread: { id: string } }>('thread/resume', {
      threadId: opts.sessionId,
      cwd: opts.cwd,
      approvalPolicy: APPROVAL_POLICY,
      ...sandbox,
      developerInstructions: BRIDGE_DEVELOPER_INSTRUCTIONS,
      ...(opts.model ? { model: opts.model } : {}),
    });
    return new CodexThread(client, res.thread.id, opts.model, opts.effort);
  }

  private async spawn(cwd: string): Promise<AppServerClient> {
    const bin = resolveCodexBin();
    if (!bin) throw new Error('codex CLI not found (set CODEX_BIN or install @openai/codex)');
    const client = new AppServerClient({ bin, cwd });
    await client.connect();
    return client;
  }
}

/** Skip codex's injected boilerplate so it never shows as a "user message". */
function isBoilerplateUserText(text: string): boolean {
  const t = text.trimStart();
  return t.startsWith('<environment_context>') || t.startsWith('# AGENTS.md instructions');
}

/**
 * Fold one codex {@link Turn}'s items into a renderable {@link HistoryTurn}.
 * Mirrors event-map.ts's item handling but also captures `userMessage` (the
 * stream path never emits the user's own input, so that case is unique here).
 */
function mapTurn(turn: Turn): HistoryTurn {
  const userParts: string[] = [];
  const assistantParts: string[] = [];
  const reasoningParts: string[] = [];
  const tools: HistoryTool[] = [];
  for (const item of (turn.items ?? []) as ThreadItem[]) {
    switch (item.type) {
      case 'userMessage': {
        const text = item.content
          .map((c) => (c.type === 'text' ? c.text : c.type === 'mention' ? `@${c.name}` : ''))
          .join('')
          .trim();
        if (text && !isBoilerplateUserText(text)) userParts.push(text);
        break;
      }
      case 'agentMessage':
        if (item.text.trim()) assistantParts.push(item.text);
        break;
      case 'reasoning': {
        const r = (item.content.length ? item.content : item.summary).join('\n').trim();
        if (r) reasoningParts.push(r);
        break;
      }
      case 'commandExecution':
        tools.push({
          title: item.command,
          output: item.aggregatedOutput ?? undefined,
          exitCode: item.exitCode,
          failed: item.status === 'failed' || item.status === 'declined' || (item.exitCode ?? 0) !== 0,
        });
        break;
      case 'fileChange':
        tools.push({ title: '编辑文件', failed: item.status === 'failed' || item.status === 'declined' });
        break;
      case 'webSearch':
        tools.push({ title: `联网搜索：${item.query}` });
        break;
      case 'mcpToolCall':
        tools.push({ title: `${item.server} / ${item.tool}`, failed: item.status === 'failed' || Boolean(item.error) });
        break;
      case 'dynamicToolCall':
        tools.push({ title: item.tool, failed: item.status === 'failed' || item.success === false });
        break;
      // plan / contextCompaction / review-mode / image* — omitted from the digest
      default:
        break;
    }
  }
  return {
    userText: userParts.join('\n\n'),
    assistantText: assistantParts.join('\n\n'),
    reasoning: reasoningParts.join('\n\n'),
    tools,
    startedAt: turn.startedAt ?? undefined,
  };
}

interface RawThread {
  id: string;
  preview?: string;
  createdAt?: number;
  updatedAt?: number;
  name?: string | null;
  ephemeral?: boolean;
}

interface RawModel {
  id: string;
  displayName?: string;
  description?: string;
  hidden?: boolean;
  isDefault?: boolean;
  supportedReasoningEfforts?: { reasoningEffort: ReasoningEffort }[];
  defaultReasoningEffort?: ReasoningEffort;
}

function mapModel(m: RawModel): ModelInfo {
  return {
    id: m.id,
    displayName: m.displayName ?? m.id,
    description: m.description ?? '',
    hidden: m.hidden ?? false,
    isDefault: m.isDefault ?? false,
    supportedEfforts: (m.supportedReasoningEfforts ?? []).map((e) => e.reasoningEffort),
    defaultEffort: m.defaultReasoningEffort ?? 'medium',
  };
}

const STATIC_MODELS: ModelInfo[] = [
  {
    id: 'gpt-5.5',
    displayName: 'GPT-5.5',
    description: '默认模型',
    hidden: false,
    isDefault: true,
    supportedEfforts: ['low', 'medium', 'high'],
    defaultEffort: 'medium',
  },
];
