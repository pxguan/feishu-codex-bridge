import { rmSync } from 'node:fs';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import { afterAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { getSession, listSessions, patchSession, upsertSession, type SessionRecord } from '../src/bot/session-store';
import { paths } from '../src/config/paths';

// 把 sessions.json 指到临时目录，绝不碰真实 ~/.feishu-codex-bridge。
vi.mock('../src/config/paths', async () => {
  const { mkdtempSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const dir = mkdtempSync(join(tmpdir(), 'session-store-test-'));
  return { paths: { sessionsFile: join(dir, 'sessions.json') } };
});

afterAll(() => {
  rmSync(dirname(paths.sessionsFile), { recursive: true, force: true });
});

beforeEach(async () => {
  await rm(paths.sessionsFile, { force: true });
});

function rec(threadId: string, sessionId: string): SessionRecord {
  return {
    threadId,
    chatId: 'oc_chat',
    cwd: '/tmp/proj',
    sessionId,
    backend: 'codex-appserver',
    summary: `s-${threadId}`,
    createdAt: Date.now(),
    updatedAt: Date.now(),
  };
}

describe('session-store', () => {
  it('upsert + getSession roundtrip; upsert replaces by threadId', async () => {
    await upsertSession(rec('t1', 'cx1'));
    await upsertSession(rec('t1', 'cx1b'));
    expect((await getSession('t1'))?.sessionId).toBe('cx1b');
    expect(await listSessions()).toHaveLength(1);
  });

  // F3 核心：并发 upsert 不同 threadId，无锁版会基于同一旧快照后写覆盖前写丢绑定。
  it('20 concurrent upserts of distinct threadIds all survive', async () => {
    await Promise.all(Array.from({ length: 20 }, (_, i) => upsertSession(rec(`t${i}`, `cx${i}`))));
    const all = await listSessions();
    expect(all).toHaveLength(20);
    for (let i = 0; i < 20; i++) {
      expect(all.find((s) => s.threadId === `t${i}`)?.sessionId).toBe(`cx${i}`);
    }
    // 落盘文件本身完好（tmp 交错会产生半截 JSON）
    const onDisk = JSON.parse(await readFile(paths.sessionsFile, 'utf8'));
    expect(onDisk.sessions).toHaveLength(20);
  });

  it('concurrent functional patches see the latest on-disk value (no lost update)', async () => {
    await upsertSession(rec('t1', 'cx1'));
    await Promise.all(
      Array.from({ length: 20 }, () => patchSession('t1', (s) => ({ lastSeenAt: (s.lastSeenAt ?? 0) + 1 }))),
    );
    expect((await getSession('t1'))?.lastSeenAt).toBe(20);
  });

  // M-8：旧 v1 文件读入迁移 —— 会话 id 旧字段名 → sessionId；缺 backend 回填
  // 默认 codex 后端。重启后既不丢绑定，也能按 backend 正确路由 resume。
  it('migrates a legacy v1 file on read (session-id field rename + backend backfill)', async () => {
    await mkdir(dirname(paths.sessionsFile), { recursive: true });
    const legacy = {
      version: 1,
      sessions: [
        {
          threadId: 'old-topic',
          chatId: 'oc_chat',
          cwd: '/tmp/proj',
          ['codexThread' + 'Id']: 'cx-legacy', // 旧字段名（拼接缘由见 session-store 注释）
          summary: 's',
          createdAt: 1,
          updatedAt: 1,
        },
      ],
    };
    await writeFile(paths.sessionsFile, JSON.stringify(legacy), 'utf8');
    const got = await getSession('old-topic');
    expect(got?.sessionId).toBe('cx-legacy');
    expect(got?.backend).toBe('codex-appserver');
    // 写回（patch 任意字段）后落盘的是新字段名 + 回填的 backend + 新文件版本
    await patchSession('old-topic', { model: 'gpt-5.5' });
    const onDisk = JSON.parse(await readFile(paths.sessionsFile, 'utf8'));
    expect(onDisk.version).toBe(2);
    expect(onDisk.sessions[0].sessionId).toBe('cx-legacy');
    expect(onDisk.sessions[0].backend).toBe('codex-appserver');
  });

  it('keeps an explicit non-default backend (no backfill clobber)', async () => {
    await upsertSession({ ...rec('t-claude', 'sess-uuid'), backend: 'claude-sdk' });
    expect((await getSession('t-claude'))?.backend).toBe('claude-sdk');
  });

  it('patchSession skips undefined fields and is a no-op for an unknown threadId', async () => {
    await upsertSession(rec('t1', 'cx1'));
    await patchSession('t1', { model: 'gpt-5.5', effort: undefined });
    const got = await getSession('t1');
    expect(got?.model).toBe('gpt-5.5');
    expect(got?.effort).toBeUndefined();
    await patchSession('nope', { model: 'x' }); // must not throw or create a record
    expect(await listSessions()).toHaveLength(1);
  });
});
