export type TenantBrand = 'feishu' | 'lark';

/**
 * SecretRef points at a secret stored outside config.json (keeps the App
 * Secret out of backups/git/log dumps). Mirrors lark-cli's SecretRef shape.
 */
export interface SecretRef {
  source: 'env' | 'file' | 'exec';
  provider?: string;
  id: string;
}

export type SecretInput = string | SecretRef;

export interface AppCredentials {
  id: string;
  secret: SecretInput;
  tenant: TenantBrand;
}

export interface ProviderConfig {
  source: 'env' | 'file' | 'exec';
  allowlist?: string[];
  path?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  passEnv?: string[];
  noOutputTimeoutMs?: number;
  maxOutputBytes?: number;
}

export interface SecretsConfig {
  providers?: Record<string, ProviderConfig>;
  defaults?: { env?: string; file?: string; exec?: string };
}

/** card: full interactive card · markdown: lightweight streaming · text: one-shot */
export type MessageReplyMode = 'card' | 'markdown' | 'text';

/** Behavior when a new message arrives mid-turn: steer(引导) or queue(排队). */
export type PendingPolicy = 'steer' | 'queue';

/**
 * Access control (see design §5):
 *   ownerOpenId   — 扫码注册者(owner)。恒为 admin、不可删；即使 admins 为空也仍是 admin。
 *   admins        — open_ids that may DM the bot (create project / global config /
 *                   destructive ops). 空 = 仅 owner。
 *   allowedUsers  — @deprecated 全局响应白名单，已由项目级 Project.allowedUsers 取代。
 *   allowedChats  — chat_ids the bot responds in. Empty = all.
 */
export interface AppAccess {
  /** 扫码注册者的 open_id；恒为 admin，不可删；admins 增删不影响它。 */
  ownerOpenId?: string;
  admins?: string[];
  /** @deprecated 已由项目级 Project.allowedUsers 取代；保留仅为兼容旧 config 解析，不再读取。 */
  allowedUsers?: string[];
  allowedChats?: string[];
}

export interface AppPreferences {
  /** reply rendering for IM messages. Default 'card'. */
  messageReply?: MessageReplyMode;
  /** render tool-call blocks in output. Default true. */
  showToolCalls?: boolean;
  /** cap concurrent codex turns across all threads. Default 10. */
  maxConcurrentRuns?: number;
  /** per-turn idle watchdog (seconds). 0 = off. Default 120 (on). */
  runIdleTimeoutSeconds?: number;
  /** new-message-mid-turn behavior. Default 'steer'. */
  pendingPolicy?: PendingPolicy;
  /** groups require @bot to respond. Default true. */
  requireMentionInGroup?: boolean;
  /** access control — see AppAccess. */
  access?: AppAccess;
  /** SIGTERM→SIGKILL grace (ms) for the app-server child. Default 5000. */
  agentStopGraceMs?: number;
  /** ACP 后端（claude-acp）的 server 启动命令覆盖——开源安装不能写死任何本机路径，
   * 默认在 PATH 上找 `claude-code-acp`；装在别处/要走 `node dist/index.js` 时在
   * 这里指定。形态对齐 SecretsConfig ProviderConfig 的 command/args。 */
  acpCommand?: { command: string; args?: string[] };
}

export interface AppConfig {
  accounts: {
    app: AppCredentials;
  };
  secrets?: SecretsConfig;
  preferences?: AppPreferences;
}

export function isComplete(cfg: Partial<AppConfig>): cfg is AppConfig {
  const app = cfg.accounts?.app;
  return Boolean(app?.id && hasSecret(app?.secret) && app?.tenant);
}

function hasSecret(s: SecretInput | undefined): boolean {
  if (!s) return false;
  if (typeof s === 'string') return s.length > 0;
  return Boolean(s.source && s.id);
}

export function isSecretRef(s: SecretInput): s is SecretRef {
  return typeof s === 'object' && s !== null;
}

/** keystore key for the bot's App Secret. */
export function secretKeyForApp(appId: string): string {
  return `app-${appId}`;
}

export function getMessageReplyMode(cfg: AppConfig): MessageReplyMode {
  const raw = cfg.preferences?.messageReply;
  if (raw === 'card' || raw === 'markdown' || raw === 'text') return raw;
  return 'card';
}

export function getShowToolCalls(cfg: AppConfig): boolean {
  return cfg.preferences?.showToolCalls !== false;
}

export function getMaxConcurrentRuns(cfg: AppConfig): number {
  const raw = cfg.preferences?.maxConcurrentRuns;
  if (typeof raw !== 'number' || !Number.isFinite(raw) || raw < 1) return 10;
  return Math.min(Math.floor(raw), 50);
}

export function getRequireMentionInGroup(cfg: AppConfig): boolean {
  return cfg.preferences?.requireMentionInGroup !== false;
}

export function getPendingPolicy(cfg: AppConfig): PendingPolicy {
  return cfg.preferences?.pendingPolicy === 'queue' ? 'queue' : 'steer';
}

/** claude-acp 后端的 server 命令覆盖（preferences.acpCommand）。未配置或形态
 * 不对 → undefined（后端落回 PATH 查找 `claude-code-acp`）。 */
export function getAcpCommand(cfg: Partial<AppConfig>): { command: string; args: string[] } | undefined {
  const raw = cfg.preferences?.acpCommand;
  if (!raw || typeof raw !== 'object' || typeof raw.command !== 'string' || !raw.command.trim()) {
    return undefined;
  }
  const args = Array.isArray(raw.args) ? raw.args.filter((a): a is string => typeof a === 'string') : [];
  return { command: raw.command, args };
}

export function getAgentStopGraceMs(cfg: AppConfig): number {
  const raw = cfg.preferences?.agentStopGraceMs;
  if (typeof raw !== 'number' || !Number.isFinite(raw)) return 5000;
  return Math.min(30_000, Math.max(100, Math.floor(raw)));
}

/** Watchdog clamp bounds in seconds — shared by the read path and the settings
 * card's custom-input validation so stored value == effective value. */
export const RUN_IDLE_TIMEOUT_MIN_SEC = 10;
export const RUN_IDLE_TIMEOUT_MAX_SEC = 3600;

/**
 * Per-turn idle watchdog in ms. Default 120s, ON. `0` disables. Clamps to
 * [10, 3600] seconds when set.
 */
export function getRunIdleTimeoutMs(cfg: AppConfig): number | undefined {
  const raw = cfg.preferences?.runIdleTimeoutSeconds;
  if (raw === 0) return undefined;
  if (typeof raw !== 'number' || !Number.isFinite(raw) || raw < 0) return 120_000;
  const clamped = Math.min(Math.max(Math.floor(raw), RUN_IDLE_TIMEOUT_MIN_SEC), RUN_IDLE_TIMEOUT_MAX_SEC);
  return clamped * 1000;
}

/**
 * @deprecated 全局响应白名单已由项目级 {@link isUserAllowedInProject} 取代，不再使用。
 * 保留以防外部引用；新代码请用 isUserAllowedInProject。
 */
export function isUserAllowed(cfg: AppConfig, senderId: string): boolean {
  const list = cfg.preferences?.access?.allowedUsers;
  if (!list || list.length === 0) return true;
  return list.includes(senderId);
}

/** True when `chatId` is one the bot responds in. Empty list = all. */
export function isChatAllowed(cfg: AppConfig, chatId: string): boolean {
  const list = cfg.preferences?.access?.allowedChats;
  if (!list || list.length === 0) return true;
  return list.includes(chatId);
}

/** The bot owner's open_id: explicit `ownerOpenId`, else the first admin (惰性
 * 兼容老 config——无 ownerOpenId 时回退首个 admin，不写盘). undefined 仅当从未注册。 */
export function resolveOwner(cfg: AppConfig): string | undefined {
  const access = cfg.preferences?.access;
  return access?.ownerOpenId ?? access?.admins?.[0];
}

/** True when `senderId` is the owner or an admin (may DM / create project /
 * destructive). Owner 恒为 admin、不受 admins 增删影响；admins 为空时仅 owner 是 admin。 */
export function isAdmin(cfg: AppConfig, senderId: string): boolean {
  if (!senderId) return false;
  if (senderId === resolveOwner(cfg)) return true;
  return Boolean(cfg.preferences?.access?.admins?.includes(senderId));
}

/** True when `senderId` may make the bot respond in a project group. admin/owner
 * 恒豁免；项目白名单空/缺省 = 所有人。project 用结构化 Pick 类型避免 schema→registry
 * 循环依赖。 */
export function isUserAllowedInProject(
  cfg: AppConfig,
  project: { allowedUsers?: string[] } | undefined,
  senderId: string,
): boolean {
  if (isAdmin(cfg, senderId)) return true;
  const list = project?.allowedUsers;
  if (!list || list.length === 0) return true;
  return list.includes(senderId);
}
