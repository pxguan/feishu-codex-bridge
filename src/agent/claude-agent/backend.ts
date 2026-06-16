import { randomUUID } from 'node:crypto';
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
import { permissionOptions } from './permission';
import { ClaudeAgentThread } from './thread';

/**
 * Claude Agent SDK backend — drives Claude Code in-process via
 * `@anthropic-ai/claude-agent-sdk`'s `query()`. Mirrors the codex app-server
 * backend's contract (see ../codex-appserver/backend.ts) so the bot orchestrator,
 * card streamer, watchdog and session store work unchanged.
 *
 * Capability deltas vs codex (declared below so the orchestrator guards them):
 *   goal/steer/compact/resume(history picker) — not (yet) supported; the SDK has
 *   no codex-style goal or turn-steer, and manual /compact isn't wired (the SDK
 *   auto-compacts). Cross-restart resume STILL works (resumeThread is always
 *   callable); only the /resume HISTORY card is gated off by `resume:false`.
 *
 * Auth: the bundled CLI reuses the host's Claude Code login (verified: a query
 * runs with apiKeySource=none when the machine is logged in), else
 * ANTHROPIC_API_KEY. No separate login step in the bridge.
 */
export class ClaudeAgentBackend implements AgentBackend {
  readonly id = 'claude-agent';
  readonly displayName = 'Claude';

  readonly capabilities: AgentCapabilities = {
    goal: false,
    steer: false,
    compact: false,
    resume: false,
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
    // The SDK bundles its own Claude Code CLI (statically imported by ./thread),
    // so "available" = the package is present (it's a hard dependency). Auth is
    // verified lazily at first turn — surfacing a missing login as a run error is
    // friendlier than a costly probe on every doctor call.
    return {
      ok: true,
      version: null,
      location: '@anthropic-ai/claude-agent-sdk',
      hint: '复用本机 Claude Code 登录态（未登录请先运行 `claude` 登录，或设置 ANTHROPIC_API_KEY）',
    };
  }

  async listModels(): Promise<ModelInfo[]> {
    return STATIC_MODELS;
  }

  // The /resume history picker is gated off (capabilities.resume=false), so these
  // are never called by the orchestrator; return empty per the never-throw contract.
  async listThreads(): Promise<ThreadSummary[]> {
    return [];
  }
  async readHistory(): Promise<ThreadHistory> {
    return { turns: [], totalTurns: 0 };
  }

  async startThread(opts: StartThreadOptions): Promise<AgentThread> {
    const sessionId = randomUUID();
    return new ClaudeAgentThread({
      sessionId,
      resume: false,
      cwd: opts.cwd,
      model: opts.model,
      effort: opts.effort,
      permission: permissionOptions(opts.mode, opts.network, opts.cwd),
      systemPromptAppend: BRIDGE_DEVELOPER_INSTRUCTIONS,
    });
  }

  async resumeThread(opts: ResumeThreadOptions): Promise<AgentThread> {
    return new ClaudeAgentThread({
      sessionId: opts.sessionId,
      resume: true,
      cwd: opts.cwd,
      model: opts.model,
      effort: opts.effort,
      permission: permissionOptions(opts.mode, opts.network, opts.cwd),
      systemPromptAppend: BRIDGE_DEVELOPER_INSTRUCTIONS,
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
