import type { PermissionMode } from './types';

/**
 * 后端 catalog —— 后端的「元数据 + 管理面驱动」单一真源（依赖类型/体积/检测/安装/
 * 分组/文案）。运行时工厂表（REGISTRY，见 index.ts）只负责构造能跑的实例；catalog
 * 负责「Web / DM 怎么展示、怎么装、怎么探」。两者职责正交、不合并、靠 id 配对
 * （单测强制 catalog.id 集合 == REGISTRY id 集合，新后端漏一处即红——防回归）。
 *
 * 「加一个新 agent CLI 后端」= 实现 AgentBackend + REGISTRY 加工厂 + 这里加一条
 * catalog（设计 §3.5）。Web 后端页 / DM picker / doctor / 按需下载按钮全自动出现。
 */

/** 底层 agent 家族（picker 按此分组：Codex 组 / Claude 组；string 为未来 agent 预留）。 */
export type AgentFamily = 'codex' | 'claude' | (string & {});

/** 后端进程的接入方式（仅描述/分组用，不参与运行路由）。 */
export type BackendAccess = 'app-server' | 'sdk' | 'acp';

/**
 * 依赖类型 —— 决定「装哪 / 怎么检测 / 能不能一键按需装」。
 *   'external-cli'  外部 CLI（codex / 未来 gemini-cli），bridge 不负责装，doctor 探 PATH。
 *   'npm-ondemand'  重 npm 包，按需装到用户私装目录（claude-agent-sdk）。**唯一可一键下载的类型。**
 *   'npm-external'  外部 npm 包 + 可能 native（claude-code-acp 适配器），用户自管。
 */
export type DepKind = 'external-cli' | 'npm-ondemand' | 'npm-external';

export interface BackendDep {
  kind: DepKind;
  /** npm 包名（npm-ondemand / npm-external 时）。 */
  pkg?: string;
  /** pin 版本（npm-ondemand，避免漂移）；undefined ⇒ latest。 */
  version?: string;
  /** 体积提示 MB（Web 下载确认用，给用户预期）。 */
  approxSizeMB?: number;
  /** 检测/装法的一句话提示（external-cli 探不到、npm-external 未装时给用户）。 */
  detectHint: string;
  /** 安装命令的一句话说明（installable=false 的手动装法 / installable=true 的旁注）。 */
  installCmd?: string;
}

export interface BackendCatalogEntry {
  /** 与 REGISTRY id 一一对应（单测强制配对）。 */
  id: string;
  agentFamily: AgentFamily;
  displayName: string;
  access: BackendAccess;
  dep: BackendDep;
  /** 本后端支持的权限档（= backend.supportedModes）；undefined ⇒ 全档（codex）。 */
  supportedModes?: readonly PermissionMode[];
  /** picker 副标题（一句话接入说明）。 */
  blurb?: string;
}

/**
 * 一条 catalog 是否可一键按需下载：npm-ondemand 才可（external-cli / npm-external
 * 要用户手动装）。Web 据此决定「未安装」时出「下载」按钮还是「手动装法提示」。
 */
export function isInstallable(entry: BackendCatalogEntry): boolean {
  return entry.dep.kind === 'npm-ondemand';
}

export const BACKEND_CATALOG: readonly BackendCatalogEntry[] = [
  {
    id: 'codex-appserver',
    agentFamily: 'codex',
    displayName: 'Codex',
    access: 'app-server',
    dep: {
      kind: 'external-cli',
      pkg: 'codex',
      detectHint: '未找到 codex CLI（设 CODEX_BIN、装 Codex.app，或 npm i -g @openai/codex）',
      installCmd: 'npm i -g @openai/codex（或装 Codex.app / 设 CODEX_BIN），然后 codex login',
    },
    // supportedModes undefined ⇒ 全档（qa/write/full）。
    blurb: '能力最全（goal/steer/compact/resume + 真沙箱只读档）',
  },
  {
    id: 'claude-sdk',
    agentFamily: 'claude',
    displayName: 'Claude（Agent SDK）',
    access: 'sdk',
    dep: {
      kind: 'npm-ondemand',
      pkg: '@anthropic-ai/claude-agent-sdk',
      version: '0.3.175',
      approxSizeMB: 224,
      detectHint: '未安装 @anthropic-ai/claude-agent-sdk（在控制台点「下载」即按需装到用户目录）',
      installCmd: '在 Web 控制台点「下载 Claude SDK」（约 224M，按需装到用户目录）',
    },
    supportedModes: ['full'],
    blurb: '开箱即用（SDK 自带 Claude Code 二进制，约 224M，按需下载）',
  },
  {
    id: 'claude-acp',
    agentFamily: 'claude',
    displayName: 'Claude（订阅·ACP）',
    access: 'acp',
    dep: {
      kind: 'npm-external',
      pkg: 'claude-code-acp',
      // 适配器含 node-pty native + npm 名被占（见 backend-detection.md §2），短期不做一键装。
      detectHint: '未找到 claude-code-acp 适配器（需 node-pty 平台编译 + 本机 claude CLI）',
      installCmd: 'npm i -g claude-code-acp（需 node-pty 平台编译）+ 本机 claude CLI；或配 preferences.acpCommand',
    },
    supportedModes: ['full'],
    blurb: '走订阅计费（不烧 SDK credit），需额外适配器，高级用户',
  },
];

/** 按 id 取一条 catalog（未注册返回 undefined）。 */
export function catalogById(id: string): BackendCatalogEntry | undefined {
  return BACKEND_CATALOG.find((e) => e.id === id);
}

/** 按 agent 家族取 catalog（picker 分组用）。 */
export function catalogByFamily(family: AgentFamily): BackendCatalogEntry[] {
  return BACKEND_CATALOG.filter((e) => e.agentFamily === family);
}

/** catalog 声明的全部后端 id（index.ts 的 backendIds 从此派生 → catalog 是单一注册入口）。 */
export function catalogBackendIds(): string[] {
  return BACKEND_CATALOG.map((e) => e.id);
}
