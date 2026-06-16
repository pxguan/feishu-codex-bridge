import path from 'node:path';
import type { CanUseTool, Options, PermissionResult, SandboxSettings } from '@anthropic-ai/claude-agent-sdk';
import type { PermissionMode } from '../types';

/**
 * Map a bridge permission tier (qa / write / full) to the Claude Agent SDK
 * options that enforce it WITHOUT ever prompting a human (the codex backend runs
 * approvalPolicy 'never' — we match that with a deterministic `canUseTool` that
 * only ever returns allow/deny, never "ask").
 *
 *   full  → permissionMode 'bypassPermissions' (= codex danger-full-access).
 *   write → OS sandbox (command isolation) + canUseTool confines file-WRITE tools
 *           to cwd; reads/Bash allowed; network gated by the `network` toggle.
 *   qa    → OS sandbox + ALL file-write tools denied (read-only); network gated.
 *
 * ── Honest security delta vs codex (keep this comment truthful) ───────────────
 * codex's qa/write use an OS sandbox (macOS Seatbelt / Windows restricted token)
 * that confines BOTH reads and writes to cwd at the kernel level, and fail-closed
 * on Linux. Here:
 *  - WRITE confinement is enforced at TWO layers: the `canUseTool` path check
 *    (blocks Write/Edit/NotebookEdit outside cwd) AND the OS sandbox (blocks Bash
 *    writes outside the workspace). Solid.
 *  - READ confinement is NOT yet as strict as codex's qa: we enable the sandbox
 *    (which restricts Bash) but do not hard-deny all reads outside cwd, so a read
 *    via the Read tool can still reach outside cwd. This is a KNOWN, documented
 *    gap (see CLAUDE_AGENT_PROGRESS.md). Until verified+hardened, treat qa here as
 *    "no writes + sandboxed commands", not "kernel-confined reads".
 *  - `failIfUnavailable: true` makes qa/write FAIL (visible error) rather than run
 *    unsandboxed if the OS sandbox can't start — fail-closed, never a silent
 *    downgrade. (Claude's sandbox supports macOS and Linux/bubblewrap.)
 */

/** File-writing built-in tools (denied in qa; cwd-confined in write). */
const WRITE_TOOLS = new Set(['Write', 'Edit', 'MultiEdit', 'NotebookEdit']);
/** Network-reaching built-in tools (denied when `network` is off). */
const NETWORK_TOOLS = new Set(['WebFetch', 'WebSearch']);

/** The subset of SDK options this module produces. Spread into query() options. */
export type ClaudePermissionOptions = Pick<
  Options,
  'permissionMode' | 'allowDangerouslySkipPermissions' | 'sandbox' | 'canUseTool' | 'disallowedTools'
>;

function isWithin(cwd: string, p: unknown): boolean {
  if (typeof p !== 'string' || !p) return false;
  const root = path.resolve(cwd);
  const abs = path.resolve(cwd, p);
  return abs === root || abs.startsWith(root + path.sep);
}

export function permissionOptions(
  mode: PermissionMode | undefined,
  network: boolean | undefined,
  cwd: string,
): ClaudePermissionOptions {
  const tier = mode ?? 'full';

  // full: historical danger-full-access — no sandbox, no prompts, network open.
  if (tier === 'full') {
    return { permissionMode: 'bypassPermissions', allowDangerouslySkipPermissions: true };
  }

  const allowNetwork = Boolean(network);
  const sandbox: SandboxSettings = {
    enabled: true,
    // fail-closed: error out instead of running unsandboxed if the OS sandbox
    // (Seatbelt / bubblewrap) can't start.
    failIfUnavailable: true,
    // sandboxed Bash shouldn't pester for per-command approval — the sandbox is
    // the boundary, and canUseTool already gates write/network tools.
    autoAllowBashIfSandboxed: true,
  } as SandboxSettings;

  const allow = (): PermissionResult => ({ behavior: 'allow' });
  const deny = (message: string): PermissionResult => ({ behavior: 'deny', message });

  const canUseTool: CanUseTool = async (toolName, input) => {
    if (NETWORK_TOOLS.has(toolName) && !allowNetwork) {
      return deny('当前为离线模式（未开启联网），已拒绝联网工具调用。');
    }
    if (WRITE_TOOLS.has(toolName)) {
      if (tier === 'qa') return deny('当前为只读模式（QA），不允许修改文件。');
      // write tier: confine writes to the project directory.
      const target = (input as Record<string, unknown>).file_path ?? (input as Record<string, unknown>).notebook_path;
      if (!isWithin(cwd, target)) return deny('写入路径超出项目目录，已拒绝（项目内读写档）。');
      return allow();
    }
    return allow();
  };

  const opts: ClaudePermissionOptions = {
    // default + a deterministic canUseTool = "decide programmatically, never ask".
    permissionMode: 'default',
    sandbox,
    canUseTool,
  };
  // qa also strips write tools from the model's context entirely (defense in depth).
  if (tier === 'qa') opts.disallowedTools = [...WRITE_TOOLS];
  return opts;
}
