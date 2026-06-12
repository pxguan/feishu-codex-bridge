import { describe, expect, it } from 'vitest';
import { buildQueuedCard, RC } from '../src/card/run-card';
import { pickIdleSessions } from '../src/bot/handle-message';

function buttons(node: unknown, acc: Record<string, any>[] = []): Record<string, any>[] {
  if (Array.isArray(node)) node.forEach((n) => buttons(n, acc));
  else if (node && typeof node === 'object') {
    const o = node as Record<string, any>;
    if (o.tag === 'button') acc.push(o);
    for (const k of Object.keys(o)) buttons(o[k], acc);
  }
  return acc;
}

// M-3: 排队占位卡 —— acquire 前可见、可 ⏹ 取消。
describe('buildQueuedCard', () => {
  it('shows the 1-based queue position and the shared-pool note while waiting', () => {
    const json = JSON.stringify(buildQueuedCard({ position: 3, cardKey: 'om_1' }));
    expect(json).toContain('排队中（第 **3** 位）');
    expect(json).toContain('全局并发池已满');
  });

  it('routes the ⏹ 取消 button through the run card\'s RC.stop action (cardKey = own messageId)', () => {
    const btns = buttons(buildQueuedCard({ position: 1, cardKey: 'om_1' }));
    expect(btns.length).toBe(1);
    expect(btns[0]!.behaviors[0].value).toMatchObject({ a: RC.stop, m: 'om_1' });
  });

  it('has no button before the messageId exists (first frame)', () => {
    expect(buttons(buildQueuedCard({ position: 1 })).length).toBe(0);
  });

  it('cancelled layout is terminal: no buttons, tells about dropped queued messages', () => {
    const card = buildQueuedCard({ cancelled: true, dropped: 2 });
    expect(buttons(card).length).toBe(0);
    const json = JSON.stringify(card);
    expect(json).toContain('已取消排队');
    expect(json).toContain('2 条排队消息已丢弃');
    // 没有滞留的排队文案
    expect(json).not.toContain('排队中（第');
  });

  it('started layout (goal) is a short note with no buttons', () => {
    const card = buildQueuedCard({ started: true });
    expect(buttons(card).length).toBe(0);
    expect(JSON.stringify(card)).toContain('已开始执行');
  });
});

// M-3: 空闲进程 reaper 的纯决策 —— busy / 新鲜 / 无打点的都不回收。
describe('pickIdleSessions', () => {
  const NOW = 1_000_000_000;
  const IDLE = 45 * 60_000;

  it('reaps only sessions idle past the threshold', () => {
    const touched = new Map<string, number>([
      ['stale', NOW - IDLE - 1],
      ['fresh', NOW - IDLE + 1_000],
      ['boundary', NOW - IDLE], // 恰好到阈值 → 回收（< idleMs 才算新鲜）
    ]);
    const out = pickIdleSessions(touched.keys(), touched, () => false, IDLE, NOW);
    expect(out.sort()).toEqual(['boundary', 'stale']);
  });

  it('skips busy sessions (active run/queue or doc-lock chain) even when stale', () => {
    const touched = new Map<string, number>([
      ['busy', NOW - IDLE * 2],
      ['idle', NOW - IDLE * 2],
    ]);
    const out = pickIdleSessions(touched.keys(), touched, (k) => k === 'busy', IDLE, NOW);
    expect(out).toEqual(['idle']);
  });

  it('never reaps a key without a touch timestamp (caller stamps it first)', () => {
    const out = pickIdleSessions(['unknown'], new Map(), () => false, IDLE, NOW);
    expect(out).toEqual([]);
  });
});
