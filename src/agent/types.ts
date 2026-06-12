/**
 * Backend-agnostic agent interface. The codex app-server implementation lives
 * in ./codex-appserver; this layer lets the bot orchestrator stay decoupled
 * from codex internals (and lets us swap to exec / SDK / remote later).
 */

/** The backend used when nothing picked one (`Project.backend` / 旧
 * SessionRecord 缺省)——the historical codex app-server path, which must stay
 * behavior-identical. Declared here (not index.ts) so pure-type consumers like
 * session-store can backfill old records without importing the registry. */
export const DEFAULT_BACKEND_ID = 'codex-appserver';

export type ReasoningEffort = 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh';

/**
 * Permission tier for a project's codex sandbox:
 *   'qa'    — read-only, confined to the project folder (cwd). External-group Q&A.
 *   'write' — read/write, confined to the project folder.
 *   'full'  — danger-full-access (whole machine + network); the historical default.
 * 'qa'/'write' are enforced via a custom codex permissions profile (see backend),
 * whose read-confinement codex can enforce on macOS (Seatbelt) and native Windows
 * (restricted token); on Linux/WSL it can't, so those tiers fail-closed there.
 */
export type PermissionMode = 'qa' | 'write' | 'full';

export interface AgentInput {
  text?: string;
  /** absolute local image paths (codex reads them directly) */
  images?: string[];
}

export interface ModelInfo {
  id: string;
  displayName: string;
  description: string;
  supportedEfforts: ReasoningEffort[];
  defaultEffort: ReasoningEffort;
  isDefault: boolean;
  hidden: boolean;
}

/** A past agent session, for the "恢复历史会话" picker (codex: thread/list). */
export interface ThreadSummary {
  /** backend session id（codex 的 thread id / claude 的 session UUID），传给 resumeThread */
  sessionId: string;
  /** first user message preview */
  preview: string;
  /** unix seconds */
  createdAt: number;
  updatedAt: number;
  /** optional user-facing title */
  name?: string;
}

/** One past tool/command/file/web call in a resumed session's transcript. */
export interface HistoryTool {
  /** short header — the shell command, or a label like '编辑文件' / '联网搜索' */
  title: string;
  /** aggregated stdout/stderr, if any (renderer truncates) */
  output?: string;
  /** process exit code for command executions */
  exitCode?: number | null;
  /** the call errored / was declined */
  failed?: boolean;
}

/** One user→assistant exchange in a resumed session (a codex Turn). */
export interface HistoryTurn {
  /** the user's prompt for this turn ('' for a tool-only / boilerplate turn) */
  userText: string;
  /** the assistant's reply (agent messages concatenated) */
  assistantText: string;
  /** the assistant's reasoning, if surfaced ('' if none) */
  reasoning: string;
  /** tool/command/file/web calls in this turn, in arrival order */
  tools: HistoryTool[];
  /** unix seconds when the turn started, if known */
  startedAt?: number;
}

/**
 * A resumed codex thread's transcript, for the "恢复历史会话" history card —
 * normalized off the app-server `thread/read` (includeTurns) turns so the card
 * layer never touches codex protocol shapes.
 */
export interface ThreadHistory {
  /** turns kept for display, oldest→newest (the most recent `turns.length`) */
  turns: HistoryTurn[];
  /** total non-empty turns in the thread before truncation (>= turns.length) */
  totalTurns: number;
  /** user-facing thread title, if set */
  name?: string;
  /** first user message preview */
  preview?: string;
  /** unix seconds */
  createdAt?: number;
  updatedAt?: number;
}

/** Normalized stream events, mapped from app-server notifications. */
export type AgentEvent =
  | { type: 'system'; threadId: string }
  | { type: 'turn_started'; turnId: string }
  | { type: 'text_delta'; itemId: string; delta: string }
  | { type: 'text'; itemId: string; text: string }
  | { type: 'thinking_delta'; itemId: string; delta: string }
  | { type: 'thinking'; itemId: string; text: string }
  | { type: 'tool_use'; itemId: string; title: string; detail?: string }
  | { type: 'tool_result'; itemId: string; output?: string; exitCode?: number | null }
  | { type: 'usage'; inputTokens?: number; outputTokens?: number }
  // Context-window usage for this thread (from thread/tokenUsage/updated). Drives
  // the run card's threshold gauge + the /context command. `contextWindow` is the
  // model's total window (null when codex doesn't report one → percent unknown).
  | { type: 'context_usage'; usedTokens: number; contextWindow: number | null }
  // codex compacted the thread's history (thread/compacted). Auto-compaction
  // surfaces a notice; a manual /compact is suppressed (see handle-message).
  | { type: 'context_compacted' }
  | { type: 'done'; turnId: string }
  // A codex goal's state changed (thread/goal/updated). Only emitted during a
  // goal run (runGoal); the normal turn loop ignores it. `status` is an open
  // string — see {@link isGoalTerminal}.
  | {
      type: 'goal_update';
      status: string;
      objective: string;
      tokensUsed: number;
      timeUsedSeconds: number;
      tokenBudget: number | null;
    }
  | { type: 'error'; message: string; willRetry: boolean };

/**
 * A codex goal's lifecycle status (thread/goal/updated). The vendored protocol
 * type lists only active|paused|budgetLimited|complete, but codex 0.139 also
 * emits `usageLimited` and `blocked` at runtime (observed) — so treat it as an
 * open string and decide terminality defensively here, never by exhaustive enum.
 */
export type GoalStatus = string;

/** Terminal goal states — the goal run is over (no more auto-continuation). */
export function isGoalTerminal(status: string): boolean {
  return (
    status === 'complete' ||
    status === 'budgetLimited' ||
    status === 'usageLimited' ||
    status === 'blocked'
  );
}

/** The goal finished successfully (vs an abnormal stop). */
export function isGoalSuccess(status: string): boolean {
  return status === 'complete';
}

export interface AgentRun {
  events: AsyncIterable<AgentEvent>;
  /** current turn id, available after `turn_started` */
  turnId(): string | undefined;
  /** epoch ms of the last RAW backend activity — refreshed on EVERY server
   * notification, including ones the event map drops (e.g. command output
   * deltas). Lets the idle watchdog tell a busy-but-quiet turn (long shell
   * command, >120s npm install) from a truly wedged one. */
  lastActivity?(): number;
}

/** Outcome of a manual {@link AgentThread.compact}. `compacted` is true iff codex
 * emitted a thread/compacted notice (false ⇒ nothing to compact). `usage` is the
 * post-compaction token usage when codex reported one, else null. */
export interface CompactResult {
  compacted: boolean;
  usage: { usedTokens: number; contextWindow: number | null } | null;
}

/** Per-turn overrides (apply to this turn and persist for subsequent turns). */
export interface TurnOptions {
  model?: string;
  effort?: ReasoningEffort;
}

export interface AgentThread {
  /** backend session id（codex 的 thread id / claude 的 session UUID）——持久化进
   * SessionRecord.sessionId，重启后经 resumeThread 找回同一会话。 */
  readonly sessionId: string;
  /** start a turn, streaming events until turn completion/error */
  runStreamed(input: AgentInput, turn?: TurnOptions): AgentRun;
  /**
   * Set a persistent goal on this thread (thread/goal/set) and stream its
   * autonomous, multi-turn execution until the goal reaches a terminal status.
   * codex auto-starts AND auto-continues every turn — we do NOT call turn/start;
   * we set the goal and consume. Emits the normal turn events PLUS `goal_update`
   * on each state change; the stream ends on a terminal goal status or a fatal
   * error. (Verified on codex 0.139: set alone kicks off turn 1.)
   */
  runGoal(objective: string): AgentRun;
  /** Clear this thread's goal (thread/goal/clear) so a non-complete goal won't
   * reactivate when the thread is later resumed. Best-effort. */
  clearGoal(): Promise<void>;
  /** inject input into the in-flight turn (引导) */
  steer(input: AgentInput, expectedTurnId: string): Promise<void>;
  /** interrupt the in-flight turn (watchdog 中止) */
  abort(turnId: string): Promise<void>;
  /** Summarize the thread's history to free context (thread/compact/start) and
   * resolve only once it actually finishes — compaction runs as a background
   * turn, so this drains the event stream to turn/completed. */
  compact(): Promise<CompactResult>;
  /** false once the underlying agent process has died (crash / kill / close) —
   * the thread is unusable and must be evicted from the live cache so
   * resolveThread's resume fallback can take over. */
  isAlive(): boolean;
  /** terminate the underlying app-server process */
  close(): Promise<void>;
}

export interface StartThreadOptions {
  cwd: string;
  model?: string;
  effort?: ReasoningEffort;
  /** permission tier; undefined → 'full' (preserves legacy danger-full-access) */
  mode?: PermissionMode;
  /** let the sandboxed agent's shell reach the network (qa/write only; full is
   * always networked). Default false. */
  network?: boolean;
  /** codex's built-in auto-compaction (on by default). `false` disables it by
   * pushing the auto-compact token limit past any real usage. Undefined → leave
   * codex's default (on). */
  autoCompact?: boolean;
}

export interface ResumeThreadOptions extends StartThreadOptions {
  /** the session to resume (a prior {@link AgentThread.sessionId}) */
  sessionId: string;
}

/**
 * Feature flags a backend declares so the orchestrator can guard codex-only
 * affordances instead of calling into methods a backend can't honor. Undefined
 * (the codex app-server backend) ⇒ every capability is true — the historical
 * full feature set. A backend that flags `false` MUST also make the
 * corresponding method(s) throw a clear "not supported" error (capability
 * guard + hard error, never a silent half-implementation).
 */
export interface AgentCapabilities {
  /** /goal 自治多轮（runGoal/clearGoal） */
  goal: boolean;
  /** 在飞行 turn 注入引导（steer）。false ⇒ orchestrator 的 steer try/catch 落入排队。 */
  steer: boolean;
  /** /compact 手动压缩（compact） */
  compact: boolean;
  /** /resume 历史会话选择卡（listThreads/readHistory）。注意 resumeThread 本身
   * 不在此能力位之下 —— 重启恢复路径（resolveThread）对所有后端直接调用它。 */
  resume: boolean;
}

export interface AgentBackend {
  readonly id: string;
  readonly displayName: string;
  /** undefined ⇒ all true (codex 的完整能力面)；见 {@link AgentCapabilities}。 */
  readonly capabilities?: AgentCapabilities;
  isAvailable(): Promise<boolean>;
  listModels(): Promise<ModelInfo[]>;
  /** recent codex threads under `cwd`, newest first (for resume picker) */
  listThreads(cwd: string, limit?: number): Promise<ThreadSummary[]>;
  /**
   * A past thread's transcript for the resume history card — reads it via
   * `thread/read` (includeTurns) WITHOUT starting a turn or holding the session
   * live. Keeps the last `maxTurns` turns; never throws (returns empty on fail).
   */
  readHistory(cwd: string, sessionId: string, maxTurns?: number): Promise<ThreadHistory>;
  startThread(opts: StartThreadOptions): Promise<AgentThread>;
  resumeThread(opts: ResumeThreadOptions): Promise<AgentThread>;
}
