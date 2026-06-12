import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { dirname } from 'node:path';
import { paths } from '../config/paths';
import type { ReasoningEffort } from '../agent/types';

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
  /** codex thread id — pass to backend.resumeThread */
  codexThreadId: string;
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

const FILE_VERSION = 1;

async function read(): Promise<SessionRecord[]> {
  try {
    const text = await readFile(paths.sessionsFile, 'utf8');
    const parsed = JSON.parse(text) as Partial<StoreFile>;
    return Array.isArray(parsed.sessions) ? parsed.sessions : [];
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') return [];
    throw err;
  }
}

// 同进程内并发的「读-改-写」串行化（upsertSession/patchSession）：话题天然并行
// （semaphore 默认 10），两个话题同时落盘会基于同一旧快照算结果、后写覆盖前写——
// 其中一个话题的 codexThreadId 绑定静默丢失，重启后上下文蒸发。与 registry.ts 的
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
