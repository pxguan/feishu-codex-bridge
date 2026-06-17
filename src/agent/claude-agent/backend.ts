import { randomUUID } from 'node:crypto';
import { log } from '../../core/logger';
import type {
  AgentBackend,
  AgentCapabilities,
  AgentThread,
  BackendProbe,
  ModelInfo,
  PermissionMode,
  ReasoningEffort,
  ResumeThreadOptions,
  StartThreadOptions,
  ThreadHistory,
  ThreadSummary,
} from '../types';
import { BRIDGE_DEVELOPER_INSTRUCTIONS } from '../bridge-instructions';
import { isBackendDepInstalled, installedBackendVersion, loadBackendDep } from '../backend-loader';
import { permissionOptions } from './permission';
import { foldSessionMessages, mapSessionSummary } from './history';
import { ClaudeAgentThread } from './thread';

/** The on-demand npm package backing this backend. */
const SDK_PKG = '@anthropic-ai/claude-agent-sdk';

/** The SDK's runtime surface we use (typed off the package, erased at build). */
type ClaudeSdk = typeof import('@anthropic-ai/claude-agent-sdk');

let sdkPromise: Promise<ClaudeSdk> | undefined;
/**
 * Lazy-load the SDK via the on-demand loader (bridge/global node_modules → user
 * private install dir). Cached after first success. Throws BackendNotInstalledError
 * when absent — so a fresh install that hasn't downloaded Claude yet fails with a
 * clear "未安装" instead of crashing at module import. If the user already has the
 * package anywhere on the resolve path (e.g. a global `npm i -g`), this finds it
 * and never re-downloads.
 */
function loadSdk(): Promise<ClaudeSdk> {
  sdkPromise ??= loadBackendDep<ClaudeSdk>(SDK_PKG);
  return sdkPromise;
}

/**
 * Claude Agent SDK backend — drives Claude Code in-process via
 * `@anthropic-ai/claude-agent-sdk`'s `query()`. Mirrors the codex app-server
 * backend's contract (see ../codex-appserver/backend.ts) so the bot orchestrator,
 * card streamer, watchdog and session store work unchanged.
 *
 * Packaging: the SDK is an ON-DEMAND dependency (not bundled with the bridge) —
 * loaded lazily via {@link loadSdk}. When absent, the Web 后端页 shows a「下载」
 * button (catalog marks it npm-ondemand); an already-installed copy (bridge /
 * global / user dir) is detected and reused, never re-downloaded.
 *
 * Auth: the SDK's bundled CLI reuses the host's Claude login (verified: a query
 * runs with apiKeySource=none when the machine is logged in), else
 * ANTHROPIC_API_KEY. No separate login step in the bridge.
 */
export class ClaudeAgentBackend implements AgentBackend {
  readonly id = 'claude-agent';
  readonly displayName = 'Claude';

  readonly capabilities: AgentCapabilities = {
    // /goal：goal-like —— 一个自主轮跑完目标 + 合成状态 + abort 硬停可终止续聊
    // （非 codex 的多轮目标引擎，差异见 thread.runGoal）。
    goal: true,
    steer: false,
    // /compact：Claude Code 原生斜杠命令（发 "/compact" 即触发，见 thread.compact）。
    compact: true,
    // resume 历史卡：读 ~/.claude/projects 会话存储（与 `claude -r` 同源，双向可见）。
    resume: true,
    approvals: false,
  };

  // Claude's sandbox supports macOS (Seatbelt) and Linux (bubblewrap), so all
  // three tiers are offered; qa/write fail-closed at runtime if the sandbox can't
  // start (permission.ts sets sandbox.failIfUnavailable). See the security delta
  // documented in permission.ts / CLAUDE_AGENT_PROGRESS.md.
  readonly supportedModes: readonly PermissionMode[] = ['qa', 'write', 'full'];

  async isAvailable(): Promise<boolean> {
    return (await this.doctor()).ok;
  }

  async doctor(): Promise<BackendProbe> {
    // "available" = the on-demand SDK is installed (bridge/global/user dir). When
    // not, report installable so the Web shows a「下载」button. Auth is verified
    // lazily at first turn (a missing login surfaces as a friendly run error).
    if (!isBackendDepInstalled(SDK_PKG)) {
      return {
        ok: false,
        version: null,
        installable: true,
        depState: 'not-installed',
        hint: '点「下载」安装 Claude Agent SDK（零 sudo，装到用户目录）',
      };
    }
    return {
      ok: true,
      version: installedBackendVersion(SDK_PKG),
      location: SDK_PKG,
      depState: 'installed',
      hint: '复用本机 Claude 登录态（未登录请先 `claude` 登录，或设置 ANTHROPIC_API_KEY）',
    };
  }

  async listModels(): Promise<ModelInfo[]> {
    return STATIC_MODELS;
  }

  /** 最近会话（newest first），读 ~/.claude/projects/<cwd-hash> 的 JSONL 存储——
   * 与 `claude -r` 同源，故能列出本机用 `claude` 手开的会话。绝不抛错（契约）。 */
  async listThreads(cwd: string, limit = 15): Promise<ThreadSummary[]> {
    try {
      const sdk = await loadSdk();
      const sessions = await sdk.listSessions({ dir: cwd, limit });
      return sessions
        .map(mapSessionSummary)
        .sort((a, b) => b.updatedAt - a.updatedAt);
    } catch (err) {
      log.fail('agent', err, { backend: 'claude-agent', phase: 'listSessions' });
      return [];
    }
  }

  /** 某会话的转写摘要（resume 历史卡）——读 getSessionMessages 折叠成 turns，不起会话、
   * 无 token 成本。绝不抛错（返回空）。 */
  async readHistory(cwd: string, sessionId: string, maxTurns = 10): Promise<ThreadHistory> {
    try {
      const sdk = await loadSdk();
      const messages = await sdk.getSessionMessages(sessionId, { dir: cwd });
      return foldSessionMessages(messages, maxTurns, cwd);
    } catch (err) {
      log.fail('agent', err, { backend: 'claude-agent', phase: 'getSessionMessages', sessionId });
      return { turns: [], totalTurns: 0 };
    }
  }

  async startThread(opts: StartThreadOptions): Promise<AgentThread> {
    const sdk = await loadSdk();
    return new ClaudeAgentThread({
      sessionId: randomUUID(),
      resume: false,
      cwd: opts.cwd,
      model: opts.model,
      effort: opts.effort,
      permission: permissionOptions(opts.mode, opts.network, opts.cwd),
      systemPromptAppend: BRIDGE_DEVELOPER_INSTRUCTIONS,
      query: sdk.query,
    });
  }

  async resumeThread(opts: ResumeThreadOptions): Promise<AgentThread> {
    const sdk = await loadSdk();
    return new ClaudeAgentThread({
      sessionId: opts.sessionId,
      resume: true,
      cwd: opts.cwd,
      model: opts.model,
      effort: opts.effort,
      permission: permissionOptions(opts.mode, opts.network, opts.cwd),
      systemPromptAppend: BRIDGE_DEVELOPER_INSTRUCTIONS,
      query: sdk.query,
    });
  }
}

const EFFORTS: ReasoningEffort[] = ['low', 'medium', 'high', 'xhigh'];

/** Static Claude model catalog for the model picker. The SDK accepts these ids
 * (Options.model). Effort is applied at thread creation via Options.effort. */
const STATIC_MODELS: ModelInfo[] = [
  {
    id: 'claude-opus-4-8',
    displayName: 'Claude Opus 4.8',
    description: '最强，复杂推理 / 长程 agentic',
    supportedEfforts: EFFORTS,
    defaultEffort: 'high',
    isDefault: true,
    hidden: false,
  },
  {
    id: 'claude-sonnet-4-6',
    displayName: 'Claude Sonnet 4.6',
    description: '均衡，日常编码',
    supportedEfforts: EFFORTS,
    defaultEffort: 'medium',
    isDefault: false,
    hidden: false,
  },
  {
    id: 'claude-haiku-4-5',
    displayName: 'Claude Haiku 4.5',
    description: '最快，轻量任务',
    supportedEfforts: ['low', 'medium', 'high'],
    defaultEffort: 'low',
    isDefault: false,
    hidden: false,
  },
];
