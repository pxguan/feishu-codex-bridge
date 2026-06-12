import { rmSync } from 'node:fs';
import { readFile, rm } from 'node:fs/promises';
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

function rec(threadId: string, codexThreadId: string): SessionRecord {
  return {
    threadId,
    chatId: 'oc_chat',
    cwd: '/tmp/proj',
    codexThreadId,
    summary: `s-${threadId}`,
    createdAt: Date.now(),
    updatedAt: Date.now(),
  };
}

describe('session-store', () => {
  it('upsert + getSession roundtrip; upsert replaces by threadId', async () => {
    await upsertSession(rec('t1', 'cx1'));
    await upsertSession(rec('t1', 'cx1b'));
    expect((await getSession('t1'))?.codexThreadId).toBe('cx1b');
    expect(await listSessions()).toHaveLength(1);
  });

  // F3 核心：并发 upsert 不同 threadId，无锁版会基于同一旧快照后写覆盖前写丢绑定。
  it('20 concurrent upserts of distinct threadIds all survive', async () => {
    await Promise.all(Array.from({ length: 20 }, (_, i) => upsertSession(rec(`t${i}`, `cx${i}`))));
    const all = await listSessions();
    expect(all).toHaveLength(20);
    for (let i = 0; i < 20; i++) {
      expect(all.find((s) => s.threadId === `t${i}`)?.codexThreadId).toBe(`cx${i}`);
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
