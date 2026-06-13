import { readFile } from 'node:fs/promises';
import { loadBots } from '../config/bots';
import { botPaths } from '../config/paths';
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
import { backendIds, createBackend, DEFAULT_BACKEND_ID } from '../agent';
import type { PermissionMode } from '../agent/types';
import { readRecentLogs } from '../core/logger';
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

/** daemon/预览两种进程形态的差异全部收进这两个注入点；读路径完全同源。 */
export interface AdminServiceDeps {
  /** 写执行器：botId + op → 完成或抛 AdminWriteError（校验拒绝）。
   * 缺省 = 只读预览，写方法抛 {@link NotWiredYetError}（HTTP 501）。 */
  executeWrite?: (botId: string, op: AdminWriteOp) => Promise<void>;
  /** 实时运行状态（daemon 进程内：本进程 channel / 子进程 IPC）。返回 undefined
   * 或缺省 → 回退锁文件探测（该 bot 不归本 daemon 管，如未激活的 bot）。 */
  liveStatus?: (botId: string) => Promise<BotLiveStatus | undefined>;
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

    async doctorBackends(): Promise<AdminBackendStatus[]> {
      // 与 DM 🧠 后端检测卡同源：按注册表动态探测，绝不硬编码后端列表。
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
  };
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
