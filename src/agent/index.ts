import type { AgentBackend } from './types';
import { CodexAppServerBackend } from './codex-appserver/backend';
import { ClaudeSdkBackend } from './claude-sdk/backend';

/** The backend used when a project doesn't pick one (`Project.backend` unset) —
 * the historical codex app-server path, which must stay behavior-identical. */
export const DEFAULT_BACKEND_ID = 'codex-appserver';

/**
 * Backend registry. Construction is lazy (a factory per id) so merely having a
 * backend registered costs nothing until some project actually selects it.
 */
const REGISTRY = new Map<string, () => AgentBackend>([
  ['codex-appserver', () => new CodexAppServerBackend()],
  // Claude Code via the official Agent SDK — minimal slice (capability-guarded:
  // goal/steer/compact/resume off). The SDK itself loads lazily inside the
  // backend, so registering it here costs nothing for codex-only deployments.
  ['claude-sdk', () => new ClaudeSdkBackend()],
]);

/** Registered backend ids (for config validation / error messages). */
export function backendIds(): string[] {
  return [...REGISTRY.keys()];
}

/** Construct an agent backend by id. No id → the codex app-server default. */
export function createBackend(id: string = DEFAULT_BACKEND_ID): AgentBackend {
  const make = REGISTRY.get(id);
  if (!make) {
    throw new Error(`未知 agent 后端「${id}」（可用：${backendIds().join('、')}）`);
  }
  return make();
}

export type * from './types';
