import { readFile, rm } from 'node:fs/promises';
import { loadBots, setActiveBots, removeBot } from '../config/bots';
import { botPaths, botDir } from '../config/paths';
import { removeSecret } from '../config/keystore';
import { secretKeyForApp } from '../config/schema';
import {
  defaultNoMention,
  effectiveGuestMode,
  effectiveMode,
  listProjectsIn,
} from '../project/registry';
import { listSessionsIn } from '../bot/session-store';
import { loadConfig } from '../config/store';
import { isComplete } from '../config/schema';
import { resolveAppSecret } from '../config/secret-resolver';
import { diagnoseEventSubscription, type EventDiagnosis } from '../utils/event-diagnosis';
import { validateAppCredentials } from '../utils/feishu-auth';
import { buildScopeGrantUrl, buildEventConfigUrl } from '../config/scopes';
import {
  registerBotFromCredentials,
  type RegisterBotResult,
  type RegisterBotFailure,
} from '../bot/register-bot';
import { backendIds, createBackend, DEFAULT_BACKEND_ID } from '../agent';
import type { PermissionMode } from '../agent/types';
import { readRecentLogs } from '../core/logger';
import { getServiceAdapter } from '../service/adapter';
import { checkUpdate as checkUpdateImpl, type UpdateCheck } from '../service/update';
import { bridgeVersion } from '../core/version';
import { collectHostDoctor, toDaemonStatus, type DaemonStatus, type HostDoctor } from './host';
import type { AdminWriteOp } from './ops';

/**
 * 管理面共享服务层（设计：.plans/auto-optimize/design/admin-surface.md）。
 *
 * 【契约】DM 卡片回调与 Web API **共享同一写逻辑**：
 *   - 体验对齐原则「Web 能操作的飞书也能操作」——本接口的方法清单严格对齐
 *     DM 私聊控制台已有的 dm.* 动作（src/card/dm-cards.ts）：
 *       listBots            ← CLI `bot list` / 多 bot 聚合视图
 *       listProjects        ← dm.projects（📁 项目列表）
 *       getProject          ← dm.projectSettings（⚙️ 项目设置）
 *       switchBackend       ← dm.proj.backend.submit（🧠 后端 · 切换）
 *       setPermissionMode   ← dm.proj.perm.submit（🔐 权限 · 保存）
 *       setNoMention        ← dm.proj.noMention（✋ 免@）
 *       setAutoCompact      ← dm.proj.autoCompact（🗜️ 自动压缩）
 *       doctorBackends      ← dm.doctor 的后端探测段（🩺 诊断）
 *       eventDiagnosis      ← dm.doctor 的事件订阅三态段（M-7）
 *       listSessions        ← dm.projectTopics（🧵 话题钻取）
 *       tailLogs            ← CLI `logs`（DM 够不着的宿主机域）
 *   - 写方法与 DM 的 dm.* 回调 handler 走**同一套共享纯函数**（admin/ops.ts 的
 *     perform*）：同样的校验、同样的会话驱逐、同样的落盘——双端行为零漂移。
 *
 * 【进程形态】写操作必须在持有该 bot registry/LIVE 会话的进程内执行：
 *   - 单 bot inline `run`：daemon 进程内直接调 Orchestrator.adminExecute。
 *   - 多 bot supervisor：经 IPC（admin/ipc.ts）转发给对应 bot 子进程执行。
 *   - 独立预览进程（`web` 命令、daemon 未跑）：无 executeWrite → 写抛
 *     {@link NotWiredYetError}（HTTP 501，只读预览）。
 *
 * 【实现注意】所有跨 bot 读取走 {@link botPaths} 的**显式路径**（listProjectsIn /
 * listSessionsIn / loadConfig(file)），绝不 useBotDir 全局切目录——daemon 进程内
 * 切目录会把在跑 bot 的 paths 指到别的 bot（第一棒遗留的坑，本棒修掉）。
 */
export interface AdminService {
  /** 全部已注册 bot + 进程在跑状态（daemon 内 = 真实 WS 状态；预览 = 锁文件探测）。 */
  listBots(): Promise<AdminBot[]>;
  /** 某 bot 的项目列表（含话题数等聚合字段），对齐 DM 📁 项目列表。 */
  listProjects(botId: string): Promise<AdminProject[]>;
  /** 单个项目详情；不存在返回 undefined。 */
  getProject(botId: string, name: string): Promise<AdminProject | undefined>;
  /** 🧠 切换项目后端（写）。与 DM dm.proj.backend.submit 同一套校验+落盘。 */
  switchBackend(botId: string, projectName: string, backendId: string): Promise<void>;
  /** 🔐 设置权限档（管理员档/普通用户档/联网）（写），含驱逐活跃会话的既有语义。 */
  setPermissionMode(
    botId: string,
    projectName: string,
    opts: { mode: PermissionMode; guestMode?: PermissionMode; network?: boolean },
  ): Promise<void>;
  /** ✋ 免@ 开关（写）。 */
  setNoMention(botId: string, projectName: string, on: boolean): Promise<void>;
  /** 🗜️ 自动压缩开关（写），含驱逐活跃会话的既有语义。 */
  setAutoCompact(botId: string, projectName: string, on: boolean): Promise<void>;
  /** 🩺 对全部注册后端做环境体检（doctor 探测，绝不抛错）。 */
  doctorBackends(): Promise<AdminBackendStatus[]>;
  /** 事件订阅三态诊断（ok / missing / unpublished / unchecked，绝不抛错）。 */
  eventDiagnosis(botId: string): Promise<EventDiagnosis>;
  /** 某项目的话题（会话）列表，新→旧，对齐 DM 🧵 话题钻取。 */
  listSessions(botId: string, projectName: string): Promise<AdminSession[]>;
  /** 最近文件日志尾部（JSON lines 文本）。 */
  tailLogs(opts?: { maxBytes?: number }): Promise<string>;

  // ── Web 专属：初始化 / 添加机器人（day-0 场景，飞书 DM 卡片做不到）──────────
  /**
   * 直填 appId + appSecret 注册一个机器人：真探活验证密钥 → 进 keystore + bots.json
   * 注册。这是 day-0 场景——bot 连上之前飞书 DM 卡片不存在，只能从 Web/CLI 手填。
   * 与每项目写操作（switchBackend 等）不同：注册纯写宿主机级别的 keystore + 注册表，
   * **不需要 daemon 在跑**（只读预览进程也能注册），所以无 NotWiredYetError 分支。
   * 绝不 throw——失败落 {@link RegisterBotFailure}（HTTP 层按 code 映射 400/409）。
   */
  registerBot(input: {
    appId: string;
    appSecret: string;
    tenant?: 'feishu' | 'lark';
    desiredName?: string;
  }): Promise<RegisterBotResult | RegisterBotFailure>;
  /**
   * 某 bot 的初始化 checklist 聚合三/四态：密钥有效（探活）/ 长连接在线 / 事件订阅
   * 三态诊断（复用 M-7）/ 缺失 scope 清单 + 一键深链。向导页 5s 轮询它直到「事件已
   * 生效」。绝不 throw——每段独立降级。
   */
  getSetupStatus(botId: string): Promise<AdminSetupStatus>;

  // ── Web 专属：daemon 生命周期 / 升级 / 多 bot 管理 / 宿主机体检 ──────────────
  /** daemon 生命周期快照（service 注册状态 + 运行 pid/版本/启动时长）。绝不抛错。 */
  getDaemonStatus(): Promise<DaemonStatus>;
  /**
   * 重启后台 daemon（service stop→start）。不能在 web 进程里直执行（会杀自己），
   * 走 detached helper（注入 {@link AdminServiceDeps.restartDaemon}）。只读预览
   * （无 daemon 在跑）抛 {@link NotWiredYetError}（HTTP 501）。
   */
  restartDaemon(): Promise<void>;
  /** 检查 npm 上有无新版（current / latest / hasUpdate / dev）。绝不抛错（latest=null 兜底）。 */
  checkUpdate(): Promise<UpdateCheck>;
  /**
   * 升级到最新版（npm i -g + 重启 daemon）。**默认只检测不自动升级**——只有用户
   * 点「升级」按钮才走这里。同 restart 走 detached helper。只读预览抛 NotWiredYetError。
   */
  applyUpdate(): Promise<void>;
  /** 宿主机体检：后端环境（复用 doctorBackends）+ Node/平台/配置目录/日志体量。绝不抛错。 */
  hostDoctor(): Promise<AdminHostDoctor>;
  /**
   * 切换某 bot 的 enabled（= 活跃集 active 字段，bots.json 落盘）。纯写宿主机级
   * 注册表，不需 daemon 在跑（与 registerBot 同档）。改活跃集需重启 daemon 才生效
   * （提示由 UI 给），这里只落盘。绝不 throw——失败回 {@link BotMutationResult}。
   */
  setBotEnabled(appId: string, enabled: boolean): Promise<BotMutationResult>;
  /**
   * 删除某 bot：注册表 + keystore 密钥 + 状态目录。**拒绝删除当前唯一 bot 或带
   * 运行中会话的 bot**——返回 { ok:false, reason } 让 UI 给清晰提示，绝不 throw。
   */
  deleteBot(appId: string): Promise<BotMutationResult>;
}

/** setBotEnabled / deleteBot 的统一返回：ok 或带中文拒因（HTTP 层映射 409）。 */
export type BotMutationResult = { ok: true } | { ok: false; reason: string };

/** 宿主机体检聚合：宿主机域（host.ts）+ 后端环境探测（doctorBackends 同源）。 */
export interface AdminHostDoctor extends HostDoctor {
  backends: AdminBackendStatus[];
}

/** 只读预览（daemon 未跑 / 独立 `web` 进程）里写方法统一抛它；HTTP 层映射 501。 */
export class NotWiredYetError extends Error {
  readonly code = 'NOT_WIRED_YET';
  constructor(action: string) {
    super(
      `「${action}」需要 daemon 在跑：当前是只读预览（直读本机文件）。` +
        '请先 `feishu-codex-bridge run`（或 `start`）启动，再运行 `web` 自动跳到 daemon 的控制台执行写操作。',
    );
    this.name = 'NotWiredYetError';
  }
}

export interface AdminBot {
  name: string;
  appId: string;
  tenant: 'feishu' | 'lark';
  botName?: string;
  /** run/start 会带起它（bot use 的多选活跃集） */
  active: boolean;
  /** bots.json 的 current（单 bot 代码路径的主 bot） */
  current: boolean;
  /** bridge 进程在跑吗。daemon 进程内 = 本进程/子进程的真实状态；预览进程 =
   * 单实例锁文件 + signal 0 探测。 */
  running: boolean;
  pid?: number;
  startedAt?: number;
  /** 真实 WS 长连接状态（connected / connecting / reconnecting / …）。仅 daemon
   * 进程内（本进程的 channel / 子进程 IPC 上报）可用；预览进程缺省。 */
  connection?: string;
}

/** daemon 进程内的实时运行状态（注入 {@link AdminServiceDeps.liveStatus}）。 */
export interface BotLiveStatus {
  running: boolean;
  pid?: number;
  startedAt?: number;
  connection?: string;
}

/** 项目快照——effective 值（缺省已按 registry 的单一事实源解析），UI 直接渲染。 */
export interface AdminProject {
  name: string;
  chatId: string;
  cwd: string;
  blank: boolean;
  branch?: string;
  kind: 'multi' | 'single';
  origin: 'created' | 'joined';
  /** effective 免@（noMention ?? defaultNoMention） */
  noMention: boolean;
  /** effective 自动压缩（autoCompact ?? true） */
  autoCompact: boolean;
  /** effective 管理员权限档 */
  mode: PermissionMode;
  /** effective 普通用户权限档 */
  guestMode: PermissionMode;
  network: boolean;
  /** effective 后端 id（backend ?? DEFAULT_BACKEND_ID） */
  backend: string;
  allowedUsersCount: number;
  /** 🧵 话题数（该群名下的会话记录数） */
  sessionCount: number;
  createdAt: number;
}

export interface AdminSession {
  threadId: string;
  chatId: string;
  summary: string;
  backend: string;
  model?: string;
  createdAt: number;
  updatedAt: number;
}

export interface AdminBackendStatus {
  id: string;
  name: string;
  ok: boolean;
  version: string | null;
  location?: string;
  hint?: string;
  /** 全局默认后端（项目未显式选择时用它） */
  isDefault: boolean;
}

/** 添加机器人向导的 checklist 聚合（GET /api/bots/:appId/setup-status）。 */
export interface AdminSetupStatus {
  appId: string;
  tenant: 'feishu' | 'lark';
  botName?: string;
  /** ① 密钥有效（tenant_access_token 探活）；undefined = 该 bot 配置缺失，没法探。 */
  credentials: { ok: boolean; reason?: string };
  /** ② bridge 长连接在线状态：running（进程在跑吗）+ connection（真实 WS，仅 daemon 内有）。 */
  connection: { running: boolean; connection?: string };
  /** ③ 事件订阅三态诊断（复用 M-7 event-diagnosis）。 */
  event: EventDiagnosis;
  /**
   * ④ 必需 scope 中尚未授权的（undefined = 没查成；空 = 已齐全）+ 一键授权深链。
   * grantUrl 始终给（预选全部 scope，缺啥点一次补齐）。
   */
  scopes: { missingRequired?: string[]; grantUrl: string };
  /** 开发者后台「事件与回调」配置页深链（事件订阅没写 API，只能引导手动配 + 发版本）。 */
  eventConfigUrl: string;
}

/** daemon/预览两种进程形态的差异全部收进这几个注入点；读路径完全同源。 */
export interface AdminServiceDeps {
  /** 写执行器：botId + op → 完成或抛 AdminWriteError（校验拒绝）。
   * 缺省 = 只读预览，写方法抛 {@link NotWiredYetError}（HTTP 501）。 */
  executeWrite?: (botId: string, op: AdminWriteOp) => Promise<void>;
  /** 实时运行状态（daemon 进程内：本进程 channel / 子进程 IPC）。返回 undefined
   * 或缺省 → 回退锁文件探测（该 bot 不归本 daemon 管，如未激活的 bot）。 */
  liveStatus?: (botId: string) => Promise<BotLiveStatus | undefined>;
  /** daemon 进程启动时刻（内嵌 web 注入）；只读预览不传 → uptime 缺省。 */
  daemonStartedAt?: number;
  /**
   * 重启 daemon 的真正执行器（detached helper，见 cli/commands/daemon-control）。
   * 缺省 = 只读预览（无 daemon 在跑），restartDaemon/applyUpdate 抛 NotWiredYetError。
   */
  restartDaemon?: () => void;
  /** 升级（npm i -g + 重启）的执行器（detached helper）。缺省同上抛 NotWiredYetError。 */
  applyUpdate?: () => void;
}

/**
 * AdminService 的统一实现：读路径直读 ~/.feishu-codex-bridge/bots/<appId>/ 下的
 * 注册表/会话文件（复用 registry / session-store / store 模块的显式路径导出，
 * 不自己解析 JSON；不碰全局 currentBotDir）；写路径与实时状态由 deps 注入。
 */
export function createAdminService(deps: AdminServiceDeps = {}): AdminService {
  async function projectsWithCounts(botId: string): Promise<AdminProject[]> {
    const files = botPaths(botId);
    const projects = await listProjectsIn(files.projectsFile);
    const sessions = await listSessionsIn(files.sessionsFile);
    const countByChat = new Map<string, number>();
    for (const s of sessions) {
      countByChat.set(s.chatId, (countByChat.get(s.chatId) ?? 0) + 1);
    }
    return projects.map((p) => ({
      name: p.name,
      chatId: p.chatId,
      cwd: p.cwd,
      blank: p.blank,
      branch: p.branch,
      kind: p.kind ?? 'multi',
      origin: p.origin ?? 'created',
      noMention: p.noMention ?? defaultNoMention(p),
      autoCompact: p.autoCompact ?? true,
      mode: effectiveMode(p),
      guestMode: effectiveGuestMode(p),
      network: p.network ?? false,
      backend: p.backend ?? DEFAULT_BACKEND_ID,
      allowedUsersCount: p.allowedUsers?.length ?? 0,
      sessionCount: p.chatId ? (countByChat.get(p.chatId) ?? 0) : 0,
      createdAt: p.createdAt,
    }));
  }

  /** 单实例锁文件（processes.json）→「bridge 进程在跑吗」。损坏/缺失一律视为
   * 未在跑（预览级探测，绝不抛错）。daemon 进程内只用于「不归本 daemon 管」的
   * bot（如未激活但被别的终端单独 run 起来的）。 */
  async function lockFileRunState(botId: string): Promise<BotLiveStatus> {
    try {
      const raw = await readFile(botPaths(botId).processesFile, 'utf8');
      const rec = JSON.parse(raw) as { pid?: number; startedAt?: number };
      if (typeof rec.pid === 'number' && isAlive(rec.pid)) {
        return { running: true, pid: rec.pid, startedAt: rec.startedAt };
      }
    } catch {
      /* 没锁文件 / 损坏 → 未在跑 */
    }
    return { running: false };
  }

  async function runState(botId: string): Promise<BotLiveStatus> {
    const live = await deps.liveStatus?.(botId).catch(() => undefined);
    return live ?? lockFileRunState(botId);
  }

  function executeWrite(botId: string, action: string, op: AdminWriteOp): Promise<void> {
    if (!deps.executeWrite) throw new NotWiredYetError(action);
    return deps.executeWrite(botId, op);
  }

  return {
    async listBots(): Promise<AdminBot[]> {
      const reg = await loadBots();
      const configured = reg.bots.some((b) => b.active !== undefined);
      const out: AdminBot[] = [];
      for (const b of reg.bots) {
        const run = await runState(b.appId);
        out.push({
          name: b.name,
          appId: b.appId,
          tenant: b.tenant,
          botName: b.botName,
          // 与 config/bots.activeBots 同语义：从未配置过活跃集 → 回退 current。
          active: configured ? b.active === true : reg.current === b.appId,
          current: reg.current === b.appId,
          running: run.running,
          pid: run.pid,
          startedAt: run.startedAt,
          connection: run.connection,
        });
      }
      return out;
    },

    listProjects(botId: string): Promise<AdminProject[]> {
      return projectsWithCounts(botId);
    },

    async getProject(botId: string, name: string): Promise<AdminProject | undefined> {
      return (await projectsWithCounts(botId)).find((x) => x.name === name);
    },

    async switchBackend(botId: string, projectName: string, backendId: string): Promise<void> {
      await executeWrite(botId, '🧠 切换后端', { kind: 'switchBackend', project: projectName, backend: backendId });
    },

    async setPermissionMode(
      botId: string,
      projectName: string,
      opts: { mode: PermissionMode; guestMode?: PermissionMode; network?: boolean },
    ): Promise<void> {
      await executeWrite(botId, '🔐 设置权限档', {
        kind: 'setPermissionMode',
        project: projectName,
        mode: opts.mode,
        guestMode: opts.guestMode,
        network: opts.network,
      });
    },

    async setNoMention(botId: string, projectName: string, on: boolean): Promise<void> {
      await executeWrite(botId, '✋ 免@ 开关', { kind: 'setNoMention', project: projectName, on });
    },

    async setAutoCompact(botId: string, projectName: string, on: boolean): Promise<void> {
      await executeWrite(botId, '🗜️ 自动压缩开关', { kind: 'setAutoCompact', project: projectName, on });
    },

    doctorBackends(): Promise<AdminBackendStatus[]> {
      return probeAllBackends();
    },

    async eventDiagnosis(botId: string): Promise<EventDiagnosis> {
      try {
        const cfg = await loadConfig(botPaths(botId).configFile);
        if (!isComplete(cfg)) return { state: 'unchecked', reason: '配置缺失（该 bot 尚未完成初始化）' };
        const { app } = cfg.accounts;
        const secret = await resolveAppSecret(cfg);
        return await diagnoseEventSubscription(app.id, secret, app.tenant);
      } catch (err) {
        return { state: 'unchecked', reason: err instanceof Error ? err.message : String(err) };
      }
    },

    async listSessions(botId: string, projectName: string): Promise<AdminSession[]> {
      const files = botPaths(botId);
      const p = (await listProjectsIn(files.projectsFile)).find((x) => x.name === projectName);
      if (!p?.chatId) return [];
      const sessions = await listSessionsIn(files.sessionsFile);
      return sessions
        .filter((s) => s.chatId === p.chatId)
        .sort((a, b) => b.updatedAt - a.updatedAt)
        .map((s) => ({
          threadId: s.threadId,
          chatId: s.chatId,
          summary: s.summary,
          backend: s.backend,
          model: s.model,
          createdAt: s.createdAt,
          updatedAt: s.updatedAt,
        }));
    },

    tailLogs(opts?: { maxBytes?: number }): Promise<string> {
      return readRecentLogs({ maxBytes: opts?.maxBytes ?? 64 * 1024 });
    },

    registerBot(input): Promise<RegisterBotResult | RegisterBotFailure> {
      // 纯写宿主机级 keystore + bots.json，与 daemon/预览进程形态无关——不走
      // executeWrite，预览进程也能注册（day-0 的全部意义就在这里）。
      return registerBotFromCredentials({
        appId: input.appId,
        appSecret: input.appSecret,
        tenant: input.tenant === 'lark' ? 'lark' : 'feishu',
        desiredName: input.desiredName,
      });
    },

    async getSetupStatus(botId: string): Promise<AdminSetupStatus> {
      const reg = await loadBots();
      const entry = reg.bots.find((b) => b.appId === botId);
      const tenant: 'feishu' | 'lark' = entry?.tenant ?? 'feishu';
      const base: Pick<AdminSetupStatus, 'appId' | 'tenant' | 'botName' | 'eventConfigUrl'> = {
        appId: botId,
        tenant,
        botName: entry?.botName,
        eventConfigUrl: buildEventConfigUrl(botId, tenant),
      };

      // ② 长连接：真实状态优先（daemon 内），否则锁文件探测——绝不抛错。
      const run = await runState(botId);

      // 配置缺失（注册一半 / 损坏）→ 探活、事件、scope 都没法查，统一降级。
      let cfg;
      try {
        cfg = await loadConfig(botPaths(botId).configFile);
      } catch {
        cfg = undefined;
      }
      if (!cfg || !isComplete(cfg)) {
        return {
          ...base,
          credentials: { ok: false, reason: '配置缺失（该机器人尚未完成注册或文件损坏）' },
          connection: { running: run.running, connection: run.connection },
          event: { state: 'unchecked', reason: '配置缺失，无法诊断事件订阅' },
          scopes: { grantUrl: buildScopeGrantUrl(botId, tenant) },
        };
      }

      // ①③④ 都要密钥——解析一次，三段共用（探活拿 scope、事件诊断各打各的只读 API）。
      let secret: string | undefined;
      try {
        secret = await resolveAppSecret(cfg);
      } catch (err) {
        return {
          ...base,
          credentials: { ok: false, reason: err instanceof Error ? err.message : String(err) },
          connection: { running: run.running, connection: run.connection },
          event: { state: 'unchecked', reason: '密钥不可解析' },
          scopes: { grantUrl: buildScopeGrantUrl(botId, tenant) },
        };
      }

      const { app } = cfg.accounts;
      // ①+④ 探活同时拿 botName / missingScopes；③ 事件诊断并发。各自绝不抛错。
      const [validation, event] = await Promise.all([
        validateAppCredentials(app.id, secret, app.tenant).catch((err) => ({
          ok: false as const,
          reason: err instanceof Error ? err.message : String(err),
        })),
        diagnoseEventSubscription(app.id, secret, app.tenant).catch(
          (err): EventDiagnosis => ({ state: 'unchecked', reason: err instanceof Error ? err.message : String(err) }),
        ),
      ]);

      return {
        ...base,
        botName: validation.ok ? (validation.botName ?? entry?.botName) : entry?.botName,
        credentials: validation.ok ? { ok: true } : { ok: false, reason: validation.reason },
        connection: { running: run.running, connection: run.connection },
        event,
        scopes: {
          missingRequired: validation.ok ? validation.missingScopes : undefined,
          grantUrl: buildScopeGrantUrl(app.id, app.tenant),
        },
      };
    },

    async getDaemonStatus(): Promise<DaemonStatus> {
      // service manager 状态在「未支持平台」会抛——按 undefined 降级（toDaemonStatus
      // 据此置 supported=false，UI 把重启按钮置灰引导前台 run）。
      let status;
      try {
        status = await getServiceAdapter().status();
      } catch {
        status = undefined;
      }
      return toDaemonStatus({ status, version: bridgeVersion(), startedAt: deps.daemonStartedAt });
    },

    async restartDaemon(): Promise<void> {
      // 不能在 web 进程里直执行（会杀自己）：daemon 内注入 detached helper；只读
      // 预览无 daemon 在跑 → 引导先起 daemon。
      if (!deps.restartDaemon) throw new NotWiredYetError('🔁 重启 daemon');
      deps.restartDaemon();
    },

    checkUpdate(): Promise<UpdateCheck> {
      return checkUpdateImpl().catch(() => ({
        current: bridgeVersion(),
        latest: null,
        hasUpdate: false,
        dev: false,
      }));
    },

    async applyUpdate(): Promise<void> {
      if (!deps.applyUpdate) throw new NotWiredYetError('⬆️ 升级');
      deps.applyUpdate();
    },

    async hostDoctor(): Promise<AdminHostDoctor> {
      const [host, backends] = await Promise.all([collectHostDoctor(), probeAllBackends()]);
      return { ...host, backends };
    },

    async setBotEnabled(appId: string, enabled: boolean): Promise<BotMutationResult> {
      const reg = await loadBots();
      if (!reg.bots.some((b) => b.appId === appId)) {
        return { ok: false, reason: `机器人「${appId}」不存在。` };
      }
      // 活跃集是多选 active 字段：基于当前激活集增/删该 bot 后整集重写（setActiveBots
      // 会给每个 bot 盖 explicit active 布尔，从此「已配置」）。
      const current = new Set(
        reg.bots.some((b) => b.active !== undefined)
          ? reg.bots.filter((b) => b.active === true).map((b) => b.appId)
          : reg.current
            ? [reg.current]
            : [],
      );
      if (enabled) current.add(appId);
      else current.delete(appId);
      await setActiveBots([...current]);
      return { ok: true };
    },

    async deleteBot(appId: string): Promise<BotMutationResult> {
      const reg = await loadBots();
      const target = reg.bots.find((b) => b.appId === appId);
      if (!target) return { ok: false, reason: `机器人「${appId}」不存在。` };
      // 保护①：唯一 bot 不许删（删完控制台空了、无从恢复，得引导重新 init）。
      if (reg.bots.length <= 1) {
        return { ok: false, reason: '这是当前唯一的机器人，不能删除——删完控制台就空了。先用 `bot init` 添加另一个再删。' };
      }
      // 保护②：带运行中会话（bridge 在跑 + 有话题记录）的 bot 不许删——会打断正在跑的
      // codex 会话、留下孤儿 app-server。先 `bot use` 退活跃集 + 重启让进程退出再删。
      const run = await runState(appId);
      if (run.running) {
        const sessions = await listSessionsIn(botPaths(appId).sessionsFile).catch(() => []);
        if (sessions.length > 0) {
          return {
            ok: false,
            reason: `机器人「${target.name}」正在运行且有 ${sessions.length} 个活跃会话，不能删除（会打断进行中的对话）。先在「多机器人」里关掉它并重启 daemon，等进程退出后再删。`,
          };
        }
      }
      // 注册表 + keystore 密钥 + 状态目录（projects/sessions/config），与 `bot rm` 同语义。
      await removeBot(appId);
      await removeSecret(secretKeyForApp(appId)).catch(() => undefined);
      await rm(botDir(appId), { recursive: true, force: true }).catch(() => undefined);
      return { ok: true };
    },
  };
}

/** 与 DM 🧠 后端检测卡同源：按注册表动态探测全部后端，绝不硬编码列表；绝不抛错。 */
async function probeAllBackends(): Promise<AdminBackendStatus[]> {
  return Promise.all(
    backendIds().map(async (id) => {
      const backend = createBackend(id);
      const probe = await backend.doctor({ force: true }).catch(() => undefined);
      return {
        id,
        name: backend.displayName,
        ok: probe?.ok === true,
        version: probe?.version ?? null,
        location: probe?.location,
        hint: probe?.ok ? undefined : (probe?.hint ?? '环境探测失败（未安装、未登录或探测超时）'),
        isDefault: id === DEFAULT_BACKEND_ID,
      };
    }),
  );
}

/** 只读预览（独立 `web` 进程，daemon 未跑）：无写执行器、状态靠锁文件探测。 */
export function createReadonlyAdminService(): AdminService {
  return createAdminService();
}

function isAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}
