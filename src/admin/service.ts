import { readFile } from 'node:fs/promises';
import { loadBots } from '../config/bots';
import { paths, useBotDir } from '../config/paths';
import {
  defaultNoMention,
  effectiveGuestMode,
  effectiveMode,
  getProjectByName,
  listProjects as registryListProjects,
} from '../project/registry';
import { listSessions as storeListSessions } from '../bot/session-store';
import { loadConfig } from '../config/store';
import { isComplete } from '../config/schema';
import { resolveAppSecret } from '../config/secret-resolver';
import { diagnoseEventSubscription, type EventDiagnosis } from '../utils/event-diagnosis';
import { backendIds, createBackend, DEFAULT_BACKEND_ID } from '../agent';
import type { PermissionMode } from '../agent/types';
import { readRecentLogs } from '../core/logger';

/**
 * 管理面共享服务层（设计：.plans/auto-optimize/design/admin-surface.md 阶段 1）。
 *
 * 【契约】DM 卡片回调与 Web API **共享此层**：
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
 *   - 第二棒（daemon 进程内集成）必须让 DM 的 dm.* 回调 handler 与 Web 的写路由
 *     调用**同一个 AdminService 实现**，保证两面行为一致（同样的校验、同样的
 *     会话驱逐、同样的生效播报），杜绝双写两套逻辑漂移。
 *
 * 【本棒边界】只实现只读方法（直读 registry / session-store / bots / paths 模块，
 * 不自己解析 JSON）；四个写方法一律抛 {@link NotWiredYetError} —— 写操作必须在
 * daemon 进程内执行（落盘 + 驱逐活跃会话才能立即生效），独立预览进程写盘会与
 * 在跑的 bot 进程产生双写竞争，故第一棒刻意不接。
 *
 * 【实现注意】只读实现通过 useBotDir() 全局切换当前 bot 目录后再读文件——这只
 * 在**独立 web 预览进程**（`feishu-codex-bridge web`）里安全。第二棒在 daemon
 * 进程内集成时**绝不可**这样切（会把在跑 bot 进程的 paths 指到别的 bot），必须
 * 换成每 bot 进程只服务自身状态、或经 supervisor 聚合的实现。
 */
export interface AdminService {
  /** 全部已注册 bot + 进程在跑状态（预览级探测：单实例锁文件 + signal 0）。 */
  listBots(): Promise<AdminBot[]>;
  /** 某 bot 的项目列表（含话题数等聚合字段），对齐 DM 📁 项目列表。 */
  listProjects(botId: string): Promise<AdminProject[]>;
  /** 单个项目详情；不存在返回 undefined。 */
  getProject(botId: string, name: string): Promise<AdminProject | undefined>;
  /** 🧠 切换项目后端（写）。第一棒：抛 NotWiredYetError。 */
  switchBackend(botId: string, projectName: string, backendId: string): Promise<void>;
  /** 🔐 设置权限档（管理员档/普通用户档/联网）（写）。第一棒：抛 NotWiredYetError。 */
  setPermissionMode(
    botId: string,
    projectName: string,
    opts: { mode: PermissionMode; guestMode?: PermissionMode; network?: boolean },
  ): Promise<void>;
  /** ✋ 免@ 开关（写）。第一棒：抛 NotWiredYetError。 */
  setNoMention(botId: string, projectName: string, on: boolean): Promise<void>;
  /** 🗜️ 自动压缩开关（写）。第一棒：抛 NotWiredYetError。 */
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

/** 写方法在第一棒（只读预览）里统一抛它；HTTP 层映射成 501。 */
export class NotWiredYetError extends Error {
  readonly code = 'NOT_WIRED_YET';
  constructor(action: string) {
    super(`「${action}」尚未接线：写操作将在第二棒（daemon 进程内集成）开放，当前为只读预览。`);
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
  /** 预览级探测：该 bot 的单实例锁被活进程持有 = bridge 在跑。
   * 真实 WS 连接状态（connected/reconnecting）第二棒由 daemon 进程内上报。 */
  running: boolean;
  pid?: number;
  startedAt?: number;
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

/**
 * 只读 AdminService —— 直读 ~/.feishu-codex-bridge 下的注册表/会话文件，复用
 * registry / session-store / bots 模块的导出（不自己解析 JSON）。不依赖 daemon
 * 在跑。所有「切 bot 目录 + 读」收进同一把进程内锁，防并发请求交错 useBotDir。
 */
export function createReadonlyAdminService(): AdminService {
  // 与 registry/session-store 同款的串行锁：useBotDir 是模块级全局态，
  // 两个并发请求读不同 bot 时必须串行，否则 A 的读会落到 B 的目录。
  let opChain: Promise<unknown> = Promise.resolve();
  function withBotDir<T>(botId: string, fn: () => Promise<T>): Promise<T> {
    const run = opChain.then(
      () => {
        useBotDir(botId);
        return fn();
      },
      () => {
        useBotDir(botId);
        return fn();
      },
    );
    opChain = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  async function projectsWithCounts(): Promise<AdminProject[]> {
    const projects = await registryListProjects();
    const sessions = await storeListSessions();
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
   * 未在跑（预览级探测，绝不抛错）。 */
  async function readRunState(): Promise<{ running: boolean; pid?: number; startedAt?: number }> {
    try {
      const raw = await readFile(paths.processesFile, 'utf8');
      const rec = JSON.parse(raw) as { pid?: number; startedAt?: number };
      if (typeof rec.pid === 'number' && isAlive(rec.pid)) {
        return { running: true, pid: rec.pid, startedAt: rec.startedAt };
      }
    } catch {
      /* 没锁文件 / 损坏 → 未在跑 */
    }
    return { running: false };
  }

  return {
    async listBots(): Promise<AdminBot[]> {
      const reg = await loadBots();
      const configured = reg.bots.some((b) => b.active !== undefined);
      const out: AdminBot[] = [];
      for (const b of reg.bots) {
        const run = await withBotDir(b.appId, readRunState);
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
        });
      }
      return out;
    },

    listProjects(botId: string): Promise<AdminProject[]> {
      return withBotDir(botId, projectsWithCounts);
    },

    getProject(botId: string, name: string): Promise<AdminProject | undefined> {
      return withBotDir(botId, async () => {
        const p = await getProjectByName(name);
        if (!p) return undefined;
        return (await projectsWithCounts()).find((x) => x.name === name);
      });
    },

    async switchBackend(): Promise<void> {
      throw new NotWiredYetError('🧠 切换后端');
    },

    async setPermissionMode(): Promise<void> {
      throw new NotWiredYetError('🔐 设置权限档');
    },

    async setNoMention(): Promise<void> {
      throw new NotWiredYetError('✋ 免@ 开关');
    },

    async setAutoCompact(): Promise<void> {
      throw new NotWiredYetError('🗜️ 自动压缩开关');
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

    eventDiagnosis(botId: string): Promise<EventDiagnosis> {
      return withBotDir(botId, async () => {
        try {
          const cfg = await loadConfig();
          if (!isComplete(cfg)) return { state: 'unchecked', reason: '配置缺失（该 bot 尚未完成初始化）' };
          const { app } = cfg.accounts;
          const secret = await resolveAppSecret(cfg);
          return await diagnoseEventSubscription(app.id, secret, app.tenant);
        } catch (err) {
          return { state: 'unchecked', reason: err instanceof Error ? err.message : String(err) };
        }
      });
    },

    listSessions(botId: string, projectName: string): Promise<AdminSession[]> {
      return withBotDir(botId, async () => {
        const p = await getProjectByName(projectName);
        if (!p?.chatId) return [];
        const sessions = await storeListSessions();
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
      });
    },

    tailLogs(opts?: { maxBytes?: number }): Promise<string> {
      return readRecentLogs({ maxBytes: opts?.maxBytes ?? 64 * 1024 });
    },
  };
}

function isAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}
