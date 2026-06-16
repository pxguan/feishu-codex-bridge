import { query, type Options, type Query, type SDKMessage, type SDKUserMessage } from '@anthropic-ai/claude-agent-sdk';
import { log } from '../../core/logger';
import type {
  AgentEvent,
  AgentInput,
  AgentRun,
  AgentThread,
  CompactResult,
  ReasoningEffort,
  TurnOptions,
} from '../types';
import { createTurnMapper, resultErrorText } from './event-map';
import type { ClaudePermissionOptions } from './permission';

/** Config the backend hands the thread to stand up its persistent query(). */
export interface ClaudeThreadConfig {
  /** self-assigned UUID (start: options.sessionId) or the id to resume. */
  sessionId: string;
  /** true → resume an existing session; false → a fresh one. */
  resume: boolean;
  cwd: string;
  model?: string;
  effort?: ReasoningEffort;
  permission: ClaudePermissionOptions;
  /** appended to Claude Code's default system prompt (bridge developer guidance). */
  systemPromptAppend?: string;
}

type TurnItem =
  | { kind: 'msg'; msg: SDKMessage }
  | { kind: 'end' }
  | { kind: 'error'; err: unknown };

/** A minimal single-consumer async queue (push now, await later). */
class Inbox<T> {
  private readonly buf: T[] = [];
  private readonly waiters: ((v: T) => void)[] = [];
  push(v: T): void {
    const w = this.waiters.shift();
    if (w) w(v);
    else this.buf.push(v);
  }
  next(): Promise<T> {
    const v = this.buf.shift();
    if (v !== undefined) return Promise.resolve(v);
    return new Promise((resolve) => this.waiters.push(resolve));
  }
}

/** A push-able AsyncIterable used as the query's streaming `prompt` source. */
class PushablePrompt {
  private readonly buf: SDKUserMessage[] = [];
  private readonly waiters: ((v: SDKUserMessage | null) => void)[] = [];
  private closed = false;
  push(msg: SDKUserMessage): void {
    if (this.closed) return;
    const w = this.waiters.shift();
    if (w) w(msg);
    else this.buf.push(msg);
  }
  close(): void {
    this.closed = true;
    while (this.waiters.length) this.waiters.shift()!(null);
  }
  async *[Symbol.asyncIterator](): AsyncGenerator<SDKUserMessage> {
    while (true) {
      const v = this.buf.shift();
      if (v !== undefined) {
        yield v;
        continue;
      }
      if (this.closed) return;
      const next = await new Promise<SDKUserMessage | null>((resolve) => this.waiters.push(resolve));
      if (next == null) return;
      yield next;
    }
  }
}

/** Our ReasoningEffort → the SDK's EffortLevel ('low'|'medium'|'high'|'xhigh'|'max'). */
function toSdkEffort(e: ReasoningEffort | undefined): Options['effort'] | undefined {
  if (!e) return undefined;
  if (e === 'none' || e === 'minimal') return 'low';
  return e; // low/medium/high/xhigh pass through
}

function toUserMessage(input: AgentInput): SDKUserMessage {
  // Text-only for now (image passthrough is a documented gap — see PROGRESS).
  const text = input.text ?? '';
  return {
    type: 'user',
    message: { role: 'user', content: text },
    parent_tool_use_id: null,
  } as SDKUserMessage;
}

/**
 * A Claude Agent SDK session as one persistent streaming-input `query()`.
 *
 * Design (verified by spike, SDK 0.3.178):
 *  - ONE query() per thread, prompt = a push-able AsyncIterable. Each user turn
 *    pushes one message; a single background `pump()` reads the query's message
 *    stream and routes each message to the CURRENT turn's inbox. Turns are strictly
 *    sequential (the orchestrator never starts a turn on a busy thread), so one
 *    "current sink" suffices. Reusing the warm process keeps 2nd+ turns fast
 *    (~2s to first token vs ~14s cold) — the reason we don't do per-turn query().
 *  - sessionId is self-assigned (options.sessionId on start / options.resume on
 *    resume), so it's known immediately without waiting on the init message.
 *  - interrupt() ends the in-flight turn (result subtype 'error_during_execution');
 *    the same query stays usable for the next turn (verified). We tell apart an
 *    interrupt from a real failure via `interruptRequested`.
 */
export class ClaudeAgentThread implements AgentThread {
  readonly sessionId: string;
  private readonly cwd: string;
  private model: string | undefined;
  private effort: ReasoningEffort | undefined;
  private readonly input = new PushablePrompt();
  private readonly query: Query;
  private readonly abortController = new AbortController();
  private dead = false;
  private interruptRequested = false;
  private turnSeq = 0;
  private currentTurnId: string | undefined;
  private lastActivityAt = Date.now();
  private sink: ((item: TurnItem) => void) | undefined;

  constructor(cfg: ClaudeThreadConfig) {
    this.sessionId = cfg.sessionId;
    this.cwd = cfg.cwd;
    this.model = cfg.model;
    this.effort = cfg.effort;

    const options: Options = {
      cwd: cfg.cwd,
      abortController: this.abortController,
      includePartialMessages: true,
      ...(cfg.model ? { model: cfg.model } : {}),
      ...(toSdkEffort(cfg.effort) ? { effort: toSdkEffort(cfg.effort) } : {}),
      ...(cfg.resume ? { resume: cfg.sessionId } : { sessionId: cfg.sessionId }),
      ...(cfg.systemPromptAppend
        ? { systemPrompt: { type: 'preset', preset: 'claude_code', append: cfg.systemPromptAppend } }
        : {}),
      // permission tier (permissionMode / sandbox / canUseTool / disallowedTools)
      ...cfg.permission,
      stderr: (d: string) => {
        const s = String(d).trim();
        // file-only (not in stdout allowlist) — handy for diagnosing auth/sandbox.
        if (s) log.info('agent', 'sdk-stderr', { backend: 'claude-agent', line: s.slice(0, 300) });
      },
    };

    this.query = query({ prompt: this.input, options });
    void this.pump();
  }

  /** Single background consumer: route every query message to the current turn. */
  private async pump(): Promise<void> {
    try {
      for await (const msg of this.query) {
        this.lastActivityAt = Date.now();
        this.sink?.({ kind: 'msg', msg });
      }
      this.dead = true;
      this.sink?.({ kind: 'end' });
    } catch (err) {
      this.dead = true;
      log.fail('agent', err, { backend: 'claude-agent', phase: 'pump' });
      this.sink?.({ kind: 'error', err });
    }
  }

  runStreamed(input: AgentInput, turn?: TurnOptions): AgentRun {
    const turnId = `t${++this.turnSeq}`;
    this.currentTurnId = turnId;
    this.interruptRequested = false;

    // Per-turn model override persists (mirrors codex). Effort can only be set at
    // query creation in the SDK, so a mid-session effort change is recorded but
    // takes effect on the next thread (documented limitation).
    if (turn?.model && turn.model !== this.model) {
      this.model = turn.model;
      this.query.setModel(turn.model).catch((err) => log.fail('agent', err, { phase: 'setModel' }));
    }
    if (turn?.effort) this.effort = turn.effort;

    const mapper = createTurnMapper({ cwd: this.cwd });
    const inbox = new Inbox<TurnItem>();
    const mySink = (item: TurnItem): void => inbox.push(item);
    this.sink = mySink;

    // Fire the turn NOW (push the user message) so inference overlaps the caller's
    // card setup; messages buffer in `inbox` until the for-await below drains them.
    this.input.push(toUserMessage(input));

    const self = this;
    async function* gen(): AsyncGenerator<AgentEvent> {
      yield { type: 'turn_started', turnId };
      try {
        while (true) {
          const item = await inbox.next();
          if (item.kind === 'end') {
            yield { type: 'error', message: 'Claude 会话进程已退出', willRetry: false };
            return;
          }
          if (item.kind === 'error') {
            const msg = item.err instanceof Error ? item.err.message : String(item.err);
            yield { type: 'error', message: msg || 'Claude 会话出错', willRetry: false };
            return;
          }
          const msg = item.msg as unknown as { type?: string; subtype?: string };
          self.lastActivityAt = Date.now();
          for (const ev of mapper.map(item.msg)) yield ev;
          if (msg.type === 'result') {
            if (msg.subtype === 'success' || self.interruptRequested) {
              yield { type: 'done', turnId };
            } else {
              yield { type: 'error', message: resultErrorText(msg as Record<string, unknown>), willRetry: false };
            }
            return;
          }
        }
      } finally {
        if (self.sink === mySink) self.sink = undefined;
      }
    }

    return {
      events: gen(),
      turnId: () => self.currentTurnId,
      lastActivity: () => self.lastActivityAt,
    };
  }

  // ── unsupported codex-only affordances (capabilities flag them false) ────────
  runGoal(): AgentRun {
    throw new Error('claude-agent 后端暂不支持 /goal 自治多轮');
  }
  async clearGoal(): Promise<void> {
    /* no goal concept; best-effort no-op */
  }
  async steer(): Promise<void> {
    // capabilities.steer=false → orchestrator queues the steer as the next turn.
    throw new Error('claude-agent 后端暂不支持飞行中引导（steer），将自动改为下一轮发送');
  }
  async compact(): Promise<CompactResult> {
    throw new Error('claude-agent 后端暂不支持手动 /compact（SDK 自带自动压缩）');
  }

  async abort(_turnId: string): Promise<void> {
    this.interruptRequested = true;
    try {
      await this.query.interrupt();
    } catch (err) {
      log.fail('agent', err, { backend: 'claude-agent', phase: 'interrupt' });
    }
  }

  isAlive(): boolean {
    return !this.dead;
  }

  async close(): Promise<void> {
    this.dead = true;
    try {
      this.input.close();
    } catch {
      /* ignore */
    }
    try {
      this.abortController.abort();
    } catch {
      /* ignore */
    }
    try {
      this.query.close?.();
    } catch {
      /* ignore */
    }
  }
}
