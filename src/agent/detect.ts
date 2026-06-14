import type { BackendProbe, PermissionMode } from './types';
import { DEFAULT_BACKEND_ID } from './types';
import { BACKEND_CATALOG, catalogById, type AgentFamily } from './catalog';
import { isBackendDepInstalled } from './backend-loader';
import { resolveCodexBin, codexVersionAsync } from './codex-appserver/locate';
import { resolveAcpCommand } from './acp/backend';

/**
 * 按 agent 维度检测（backend-detection.md §1）：探 2 个底层 agent（codex / claude）
 * → 推导每个后端的可用性，并据此算「有效默认后端」（智能默认）。后端只是 agent 的
 * 「接入方式」——本机能力面按 agent 组织，与运行时工厂表（REGISTRY）解耦。
 *
 * 智能默认（§3.1）：有 codex → codex-appserver；否则 claude-sdk（dep 装了的话）；
 * 否则 claude-acp（适配器在的话）；都无 → 回退 codex-appserver 占位（doctor 会报需
 * 安装）。新机器几乎总落 codex（轻核心）；只有用户主动下载过 SDK，claude 才进候选。
 */

export type AgentId = 'codex' | 'claude';

/** 一个后端在本机的可用性（按 agent 推导，挂 catalog 元数据）。 */
export interface BackendAvailability {
  backendId: string;
  available: boolean;
  /** 不可用原因（探测 hint / 推导出的缺失项）。 */
  reason?: string;
  version: string | null;
  supportedModes?: readonly PermissionMode[];
  /** 可一键按需下载（npm-ondemand 且当前未装）。 */
  installable: boolean;
}

/** 一个底层 agent 的运行时探测结果 + 它衍生的后端可用性。 */
export interface AgentRuntime {
  id: AgentId;
  displayName: string;
  installed: boolean;
  version: string | null;
  backends: BackendAvailability[];
  /** installed=false 时的安装提示。 */
  installHint?: string;
}

/** 探 codex agent（复用 resolveCodexBin + codexVersionAsync）。 */
async function probeCodexAgent(): Promise<AgentRuntime> {
  const entry = BACKEND_CATALOG.find((e) => e.id === 'codex-appserver')!;
  const bin = resolveCodexBin({ force: true });
  const version = bin ? await codexVersionAsync(bin, { force: true }) : null;
  const installed = !!bin && !!version;
  return {
    id: 'codex',
    displayName: 'Codex',
    installed,
    version,
    installHint: installed ? undefined : entry.dep.detectHint,
    backends: [
      {
        backendId: 'codex-appserver',
        available: installed,
        reason: !bin ? entry.dep.detectHint : !version ? 'codex --version 失败' : undefined,
        version,
        supportedModes: entry.supportedModes,
        installable: false,
      },
    ],
  };
}

/** 探 claude agent：claude-sdk dep 装了吗 + claude-acp 适配器在吗。任一可用即视为 claude 装了。 */
async function probeClaudeAgent(): Promise<AgentRuntime> {
  const sdkEntry = BACKEND_CATALOG.find((e) => e.id === 'claude-sdk')!;
  const acpEntry = BACKEND_CATALOG.find((e) => e.id === 'claude-acp')!;
  // SDK：解析得到包即「装了」（与 PATH 上的 claude CLI 解耦——SDK 自带二进制）。
  const sdkInstalled = sdkEntry.dep.pkg ? isBackendDepInstalled(sdkEntry.dep.pkg) : false;
  // ACP：适配器命令解析到即「装了」（不在此真握手——detectAgents 是轻探测，
  // 真握手探活留给 backend.doctor / probeBackends 那条更重的路径）。
  const acpCmd = await resolveAcpCommand({ force: true }).catch(() => null);
  const acpInstalled = !!acpCmd;
  const installed = sdkInstalled || acpInstalled;
  return {
    id: 'claude',
    displayName: 'Claude',
    installed,
    version: null,
    installHint: installed ? undefined : sdkEntry.dep.detectHint,
    backends: [
      {
        backendId: 'claude-sdk',
        available: sdkInstalled,
        reason: sdkInstalled ? undefined : sdkEntry.dep.detectHint,
        version: null,
        supportedModes: sdkEntry.supportedModes,
        installable: !sdkInstalled, // npm-ondemand 未装 → 可一键下载
      },
      {
        backendId: 'claude-acp',
        available: acpInstalled,
        reason: acpInstalled ? undefined : acpEntry.dep.detectHint,
        version: null,
        supportedModes: acpEntry.supportedModes,
        installable: !acpInstalled, // npm-ondemand（bin 类）未装 → 可一键下载
      },
    ],
  };
}

/** 探全部 agent（codex / claude），并行。绝不抛错（各 probe 自身降级）。 */
export async function detectAgents(): Promise<AgentRuntime[]> {
  return Promise.all([probeCodexAgent(), probeClaudeAgent()]);
}

/** 从一次 detectAgents 结果挑默认后端（智能默认规则，§3.1）。 */
export function pickDefaultBackend(agents: AgentRuntime[]): string {
  const find = (id: string): BackendAvailability | undefined =>
    agents.flatMap((a) => a.backends).find((b) => b.backendId === id);
  // 用户不可见闸：隐藏后端绝不当默认（否则没装 codex 的机器会自动路由到 claude）。
  const pickable = (id: string) => !catalogById(id)?.hidden && find(id)?.available;
  if (pickable('codex-appserver')) return 'codex-appserver';
  if (pickable('claude-sdk')) return 'claude-sdk';
  if (pickable('claude-acp')) return 'claude-acp';
  return DEFAULT_BACKEND_ID; // 都无 → codex 占位（doctor 会报需安装）
}

/**
 * 有效默认后端：缓存一次 detectAgents → pickDefaultBackend。散落的
 * `?? DEFAULT_BACKEND_ID` 应收敛到这里 / {@link backendForProject}，避免硬编码
 * 常量回退绕过智能默认。探测失败回退 DEFAULT_BACKEND_ID（保持现状语义）。
 *
 * 缓存：detect 要 spawn（codex --version），daemon 生命周期内本机装配几乎不变；
 * `force` 绕过缓存（体检 / 用户刚装完后要看「现在」的默认）。
 */
let defaultCache: string | undefined;
export async function effectiveDefaultBackend(opts?: { force?: boolean }): Promise<string> {
  if (!opts?.force && defaultCache !== undefined) return defaultCache;
  try {
    defaultCache = pickDefaultBackend(await detectAgents());
  } catch {
    defaultCache = DEFAULT_BACKEND_ID;
  }
  return defaultCache;
}

/**
 * 一个项目实际用哪个后端：显式选择优先，否则用有效默认（智能默认）。
 * 收敛全部 `p.backend ?? DEFAULT_BACKEND_ID` 的回退点 —— 未显式设的项目运行时
 * 按「有效默认」路由（design backend-detection.md §3.2 方案B：全局默认 + 项目继承）。
 */
export async function backendForProject(
  p: { backend?: string },
  opts?: { force?: boolean },
): Promise<string> {
  return p.backend ?? (await effectiveDefaultBackend(opts));
}

/** 一个 agent 家族下的全部后端可用性（picker 分组用）。 */
export function familyOf(agents: AgentRuntime[], family: AgentFamily): AgentRuntime | undefined {
  // family 与 AgentId 在本设计里同名（codex / claude），直接按 id 匹配。
  return agents.find((a) => a.id === family);
}

/** 一个后端探测态归一成 BackendProbe（detect → probe 适配，供 catalog/doctor 复用）。 */
export function availabilityToProbe(a: BackendAvailability): BackendProbe {
  return {
    ok: a.available,
    version: a.version,
    hint: a.available ? undefined : a.reason,
    installable: a.installable || undefined,
    depState: a.available ? 'installed' : a.installable ? 'not-installed' : 'external-missing',
  };
}
