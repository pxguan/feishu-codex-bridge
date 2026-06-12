import { describe, expect, it } from 'vitest';
import { buildQueuedCard, buildRunCard, buildRunCardPlain, CONTROLS_EID } from '../src/card/run-card';
import { initialState } from '../src/card/run-state';
import { cardIdFromMessageContent } from '../src/bot/handle-message';

/** All nodes carrying the given element_id (depth-first). */
function byEid(node: unknown, eid: string, acc: Record<string, any>[] = []): Record<string, any>[] {
  if (Array.isArray(node)) node.forEach((n) => byEid(n, eid, acc));
  else if (node && typeof node === 'object') {
    const o = node as Record<string, any>;
    if (o.element_id === eid) acc.push(o);
    for (const k of Object.keys(o)) byEid(o[k], eid, acc);
  }
  return acc;
}

// M-4 ⏹ 静默失败反馈：orphan 卡点击自愈靠「按 element_id 删掉控件行」——
// 控件行必须带稳定的 CONTROLS_EID，且只出现在有按钮的版式上。
describe('run card controls row — CONTROLS_EID（M-4 orphan 自愈锚点）', () => {
  it('the running run card carries exactly one controls row with CONTROLS_EID', () => {
    const rows = byEid(buildRunCard({ rs: initialState, cardKey: 'om_1' }), CONTROLS_EID);
    expect(rows).toHaveLength(1);
    expect(JSON.stringify(rows[0])).toContain('⏹ 终止');
  });

  it('goal cards (both layouts) anchor their controls with CONTROLS_EID', () => {
    const both = byEid(buildRunCard({ rs: initialState, cardKey: 'om_1', goalControls: true }), CONTROLS_EID);
    expect(both).toHaveLength(1);
    expect(JSON.stringify(both[0])).toContain('🎯 结束目标');
    const ending = byEid(
      buildRunCard({ rs: initialState, cardKey: 'om_1', goalControls: true, goalEnding: true }),
      CONTROLS_EID,
    );
    expect(ending).toHaveLength(1);
  });

  it('the queued placeholder card anchors its ⏹ 取消 row with CONTROLS_EID', () => {
    const rows = byEid(buildQueuedCard({ position: 2, cardKey: 'om_1' }), CONTROLS_EID);
    expect(rows).toHaveLength(1);
    expect(JSON.stringify(rows[0])).toContain('⏹ 取消');
  });

  it('button-less layouts have no controls row to delete', () => {
    expect(byEid(buildRunCardPlain({ rs: initialState, cardKey: 'om_1' }), CONTROLS_EID)).toHaveLength(0);
    expect(byEid(buildQueuedCard({ cancelled: true }), CONTROLS_EID)).toHaveLength(0);
  });
});

// 重启后 orphan 卡自愈的前半程：从载体消息 body.content 反查 CardKit 实体 card_id。
describe('cardIdFromMessageContent', () => {
  it('extracts the card_id from a CardKit-entity carrier message', () => {
    expect(cardIdFromMessageContent('{"type":"card","data":{"card_id":"7bd1…cardid"}}')).toBe('7bd1…cardid');
  });

  it('returns undefined for non-card content, malformed JSON, and missing card_id', () => {
    expect(cardIdFromMessageContent('{"text":"hi"}')).toBeUndefined();
    expect(cardIdFromMessageContent('not json')).toBeUndefined();
    expect(cardIdFromMessageContent('{"type":"card","data":{}}')).toBeUndefined();
  });
});
