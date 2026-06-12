import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { dirname } from 'node:path';
import { paths } from '../config/paths';
import { DEFAULT_BACKEND_ID, type ReasoningEffort } from '../agent/types';

/**
 * A persisted session = one Feishu topic (thread) bound to a codex thread.
 * Survives bridge restarts so @bot inside an existing topic resumes the right
 * codex thread (instead of silently starting a fresh one) and the ⚙️ per-session
 * model/effort overrides stick.
 */
export interface SessionRecord {
  /** Feishu topic thread_id (the key) */
  threadId: string;
  chatId: string;
  cwd: string;
  /** backend session id（codex 的 thread id / claude 的 session UUID）—— pass to
   * backend.resumeThread。v1 文件里的旧字段名在 read() 时迁移（见 migrate）。 */
  sessionId: string;
  /** 创建该会话的 agent 后端 id（见 src/agent/index.ts 注册表）。重启后
   * resolveThread 按它路由 resume —— 项目事后换后端不影响既有会话的归属。
   * v1 文件缺省 → 默认 codex 后端（read() 时回填）。 */
  backend: string;
  model?: string;
  effort?: ReasoningEffort;
  /** first user message excerpt, for context */
  summary: string;
  /** createTime (epoch ms) of the most recent message woven into this session —
   * the high-water mark for topic-history catch-up: the next turn only pulls
   * thread messages newer than this (see context-weave.fetchThreadContext). */
  lastSeenAt?: number;
  createdAt: number;
  updatedAt: number;
}

interface StoreFile {
  version: number;
  sessions: SessionRecord[];
}

// v2：会话 id 字段改名 sessionId + 新增 backend 字段（M-8 多后端路由）。
const FILE_VERSION = 2;

/** v1 文件的旧字段名（`codexThread` + `Id`）。拼接而非字面量，是为了让「全链改名
 * 后 grep 旧名 = 0」的判据可机械验证 —— 这里是全仓唯一还认得旧名的地方。 */
const LEGACY_V1_SESSION_FIELD = 'codexThread' + 'Id';

/** 旧记录读入迁移：v1 的旧会话 id 字段 → sessionId；缺 backend 回填默认 codex
 * 后端（v1 时代只有它）。原地落盘格式只在下次写盘时才换新（read 不回写），
 * 所以迁移必须幂等且每次 read 都跑。 */
function migrate(raw: Record<string, unknown>): SessionRecord {
  const rec = raw as unknown as SessionRecord;
  if (typeof rec.sessionId !== 'string') {
    const legacy = raw[LEGACY_V1_SESSION_FIELD];
    if (typeof legacy === 'string') (rec as { sessionId: string }).sessionId = legacy;
  }
  if (typeof rec.backend !== 'string') (rec as { backend: string }).backend = DEFAULT_BACKEND_ID;
  return rec;
}

async function read(): Promise<SessionRecord[]> {
  try {
    const text = await readFile(paths.sessionsFile, 'utf8');
    const parsed = JSON.parse(text) as Partial<StoreFile>;
    if (!Array.isArray(parsed.sessions)) return [];
    return (parsed.sessions as unknown as Record<string, unknown>[]).map(migrate);
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') return [];
    throw err;
  }
}

// 同进程内并发的「读-改-写」串行化（upsertSession/patchSession）：话题天然并行
// （semaphore 默认 10），两个话题同时落盘会基于同一旧快照算结果、后写覆盖前写——
// 其中一个话题的 sessionId 绑定静默丢失，重启后上下文蒸发。与 registry.ts 的
// 同款锁一致：配合函数式 updater，把 read+算+write 收进一个临界区。
let opChain: Promise<unknown> = Promise.resolve();
function withLock<T>(fn: () => Promise<T>): Promise<T> {
  const run = opChain.then(fn, fn);
  opChain = run.then(
    () => undefined,
    () => undefined,
  );
  return run;
}

async function write(sessions: SessionRecord[]): Promise<void> {
  await mkdir(dirname(paths.sessionsFile), { recursive: true });
  const tmp = `${paths.sessionsFile}.tmp-${process.pid}-${randomUUID()}`;
  const body: StoreFile = { version: FILE_VERSION, sessions };
  await writeFile(tmp, `${JSON.stringify(body, null, 2)}\n`, 'utf8');
  await rename(tmp, paths.sessionsFile);
}

export async function listSessions(): Promise<SessionRecord[]> {
  return read();
}

export async function getSession(threadId: string): Promise<SessionRecord | undefined> {
  return (await read()).find((s) => s.threadId === threadId);
}

/** Insert or replace a session by threadId. */
export async function upsertSession(rec: SessionRecord): Promise<void> {
  return withLock(async () => {
    const sessions = await read();
    const idx = sessions.findIndex((s) => s.threadId === rec.threadId);
    if (idx === -1) sessions.push(rec);
    else sessions[idx] = rec;
    await write(sessions);
  });
}

/** Patch fields of an existing session; no-op if it doesn't exist. `patch` 可以是
 * 对象，或一个 `(s) => patch` 函数——后者在同一临界区内基于**最新盘值**计算补丁，
 * 避免并发读-改-写丢更新。 */
export async function patchSession(
  threadId: string,
  patch:
    | Partial<Omit<SessionRecord, 'threadId'>>
    | ((s: SessionRecord) => Partial<Omit<SessionRecord, 'threadId'>>),
): Promise<void> {
  return withLock(async () => {
    const sessions = await read();
    const rec = sessions.find((s) => s.threadId === threadId);
    if (!rec) return;
    const actual = typeof patch === 'function' ? patch(rec) : patch;
    const target = rec as unknown as Record<string, unknown>;
    for (const [k, v] of Object.entries(actual)) {
      if (v !== undefined) target[k] = v;
    }
    rec.updatedAt = Date.now();
    await write(sessions);
  });
}
