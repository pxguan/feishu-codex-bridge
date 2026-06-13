import { describe, expect, it } from 'vitest';
import { ANSWER_EID, buildQueuedCard, buildRunCard, buildRunCardPlain, CONTROLS_EID } from '../src/card/run-card';
import { initialState, type RunState } from '../src/card/run-state';
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

/** Top-level body elements of a built card. */
function bodyEls(card: unknown): Array<Record<string, any>> {
  return ((card as { body?: { elements?: Array<Record<string, any>> } }).body?.elements ?? []);
}

// e2e 实测：⏹ 在 footer 位时，长输出流式期间打字机不断把按钮推出视野，用户摸不到。
// 修复：running 版式控制行固定在卡片顶部（正文之前）——卡片向下增长，顶部不动。
describe('run card controls row — 顶部固定（长流式可达）', () => {
  const streamed: RunState = {
    ...initialState,
    blocks: [{ kind: 'text', id: 'a', content: '长输出 '.repeat(50), streaming: true }],
    footer: 'streaming',
  };

  it('running 卡的控制行是 body 第一个元素，答案元素在其后', () => {
    const els = bodyEls(buildRunCard({ rs: streamed, cardKey: 'om_1' }));
    expect(els[0]?.element_id).toBe(CONTROLS_EID);
    // 流式答案元素（ANSWER_EID）仍存在且在控制行之后 — 打字机元素 id 稳定不受布局影响
    const answerIdx = els.findIndex((e) => e.element_id === ANSWER_EID);
    expect(answerIdx).toBeGreaterThan(0);
  });

  it('goal 双按钮 / goalEnding 版式同样顶置（goalEnding 的提示行紧随控制行）', () => {
    const both = bodyEls(buildRunCard({ rs: streamed, cardKey: 'om_1', goalControls: true }));
    expect(both[0]?.element_id).toBe(CONTROLS_EID);
    const ending = bodyEls(buildRunCard({ rs: streamed, cardKey: 'om_1', goalControls: true, goalEnding: true }));
    expect(ending[0]?.element_id).toBe(CONTROLS_EID);
    expect(JSON.stringify(ending[1])).toContain('目标已解除');
  });

  it('terminal 版式不受影响：无控制行，过程折叠 + 答案布局不变', () => {
    const done: RunState = { ...streamed, terminal: 'done', footer: null };
    const els = bodyEls(buildRunCard({ rs: done, cardKey: 'om_1' }));
    expect(els.some((e) => e.element_id === CONTROLS_EID)).toBe(false);
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
