import { describe, expect, it } from 'vitest';
import type { AgentEvent } from '../src/agent/types';
import { AcpEventMapper, type AcpUpdateLike } from '../src/agent/acp/event-map';
import { finalMessageText, initialState, reduce, type RunState } from '../src/card/run-state';

/**
 * 契约测试（与 claude-event-map.test 同款思路）：golden 的 ACP sessionUpdate
 * 序列喂映射器，断言产出的 AgentEvent 满足 run-state 的隐式契约（itemId 键控 /
 * tool_use↔tool_result 同 id 配对 / 工具插队后文本另起新块 / plan 整体替换原地
 * 重渲 / finish 单终态），并直接折进 reduce 验证终态不炸。纯对象 mock —— 不
 * import SDK、不 spawn 进程。
 */

function mapAll(mapper: AcpEventMapper, updates: AcpUpdateLike[]): AgentEvent[] {
  return updates.flatMap((u) => mapper.map(u));
}

function reduceAll(events: AgentEvent[]): RunState {
  return events.reduce(reduce, initialState);
}

const text = (t: string): AcpUpdateLike => ({
  sessionUpdate: 'agent_message_chunk',
  content: { type: 'text', text: t },
});
const thought = (t: string): AcpUpdateLike => ({
  sessionUpdate: 'agent_thought_chunk',
  content: { type: 'text', text: t },
});

/** golden：思考 → 工具（开始/完成）→ 文本两段 → 用量 */
const GOLDEN: AcpUpdateLike[] = [
  thought('先看看目录'),
  { sessionUpdate: 'tool_call', toolCallId: 'tc-1', title: 'ls -la', kind: 'execute', status: 'in_progress' },
  {
    sessionUpdate: 'tool_call_update',
    toolCallId: 'tc-1',
    status: 'completed',
    content: [{ type: 'content', content: { type: 'text', text: 'README.md' } }],
  },
  text('你好'),
  text('，世界'),
  { sessionUpdate: 'usage_update', used: 1234, size: 200000 },
];

describe('acp 事件映射：golden 序列 → AgentEvent 契约', () => {
  it('文本增量共用合成 itemId；tool_use↔tool_result 按 toolCallId 配对；usage_update → context_usage', () => {
    const mapper = new AcpEventMapper('t1');
    const events = mapAll(mapper, GOLDEN);

    const deltas = events.filter((e) => e.type === 'text_delta');
    expect(deltas).toHaveLength(2);
    expect(deltas[0]!.itemId).toBe(deltas[1]!.itemId); // 同一文本 run 共用 id

    const tools = events.filter((e) => e.type === 'tool_use');
    const results = events.filter((e) => e.type === 'tool_result');
    expect(tools).toHaveLength(1);
    expect(tools[0]).toMatchObject({ itemId: 'tc-1', title: 'ls -la' });
    expect(results[0]).toMatchObject({ itemId: 'tc-1', output: 'README.md' });
    expect(results[0]!.exitCode).toBeUndefined(); // completed → 非失败

    const usage = events.find((e) => e.type === 'context_usage');
    expect(usage).toMatchObject({ usedTokens: 1234, contextWindow: 200000 });

    const state = reduceAll([...events, ...mapper.finish('end_turn')]);
    expect(state.terminal).toBe('done');
    expect(finalMessageText(state)).toBe('你好，世界');
    expect(state.usage).toEqual({ used: 1234, window: 200000 });
    const toolBlocks = state.blocks.filter((b) => b.kind === 'tool');
    expect(toolBlocks).toHaveLength(1);
    expect((toolBlocks[0] as Extract<(typeof state.blocks)[number], { kind: 'tool' }>).tool.status).toBe('done');
  });

  it('工具插队后文本另起新块（itemId 递增），避免追加到工具面板上方的旧块', () => {
    const mapper = new AcpEventMapper('t2');
    const events = mapAll(mapper, [
      text('第一段'),
      { sessionUpdate: 'tool_call', toolCallId: 'tc-x', title: 'cat a.txt' },
      { sessionUpdate: 'tool_call_update', toolCallId: 'tc-x', status: 'completed' },
      text('第二段'),
    ]);
    const deltas = events.filter((e) => e.type === 'text_delta');
    expect(deltas).toHaveLength(2);
    expect(deltas[0]!.itemId).not.toBe(deltas[1]!.itemId);

    const state = reduceAll(events);
    // 块顺序：文本 → 工具 → 文本（时序保持）
    expect(state.blocks.map((b) => b.kind)).toEqual(['text', 'tool', 'text']);
  });

  it('messageId 变化 = 新消息 → 新文本块', () => {
    const mapper = new AcpEventMapper('t3');
    const events = mapAll(mapper, [
      { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'A' }, messageId: 'm1' },
      { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'B' }, messageId: 'm1' },
      { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'C' }, messageId: 'm2' },
    ] as AcpUpdateLike[]);
    const deltas = events.filter((e) => e.type === 'text_delta');
    expect(deltas[0]!.itemId).toBe(deltas[1]!.itemId);
    expect(deltas[2]!.itemId).not.toBe(deltas[0]!.itemId);
  });

  it('思考增量映射为 thinking_delta（独立于文本的 id 序列）', () => {
    const mapper = new AcpEventMapper('t4');
    const events = mapAll(mapper, [thought('想一'), thought('想二')]);
    const deltas = events.filter((e) => e.type === 'thinking_delta');
    expect(deltas).toHaveLength(2);
    expect(deltas[0]!.itemId).toBe(deltas[1]!.itemId);
    const state = reduceAll(events);
    expect(state.reasoning).toHaveLength(1);
    expect(state.reasoning[0]!.text).toBe('想一想二');
  });

  it('失败的工具：failed → exitCode 1 → run-state 标记 error', () => {
    const mapper = new AcpEventMapper('t5');
    const events = mapAll(mapper, [
      { sessionUpdate: 'tool_call', toolCallId: 'tc-f', title: 'cat /etc/shadow' },
      {
        sessionUpdate: 'tool_call_update',
        toolCallId: 'tc-f',
        status: 'failed',
        content: [{ type: 'content', content: { type: 'text', text: 'EACCES' } }],
      },
    ]);
    expect(events.find((e) => e.type === 'tool_result')).toMatchObject({ itemId: 'tc-f', exitCode: 1 });
    const state = reduceAll(events);
    const tool = state.blocks.find((b) => b.kind === 'tool') as Extract<(typeof state.blocks)[number], { kind: 'tool' }>;
    expect(tool.tool.status).toBe('error');
  });

  it('非终态 tool_call_update（in_progress 进度）不产出事件；tool_call 一来即终态则直接补结果', () => {
    const mapper = new AcpEventMapper('t6');
    expect(mapper.map({ sessionUpdate: 'tool_call_update', toolCallId: 'tc-p', status: 'in_progress' })).toEqual([]);
    const replay = mapper.map({
      sessionUpdate: 'tool_call',
      toolCallId: 'tc-r',
      title: '历史工具',
      status: 'completed',
    });
    expect(replay.map((e) => e.type)).toEqual(['tool_use', 'tool_result']);
  });

  it('plan 整体替换：首次 tool_use+tool_result，后续仅 tool_result 原地重渲同一面板', () => {
    const mapper = new AcpEventMapper('t7');
    const first = mapper.map({
      sessionUpdate: 'plan',
      entries: [
        { content: '读代码', priority: 'high', status: 'in_progress' },
        { content: '写补丁', priority: 'medium', status: 'pending' },
      ],
    });
    expect(first.map((e) => e.type)).toEqual(['tool_use', 'tool_result']);
    const second = mapper.map({
      sessionUpdate: 'plan',
      entries: [
        { content: '读代码', priority: 'high', status: 'completed' },
        { content: '写补丁', priority: 'medium', status: 'in_progress' },
      ],
    });
    expect(second.map((e) => e.type)).toEqual(['tool_result']);

    const state = reduceAll([...first, ...second]);
    const planBlocks = state.blocks.filter((b) => b.kind === 'tool');
    expect(planBlocks).toHaveLength(1); // 同一面板，不堆新块
    const tool = (planBlocks[0] as Extract<(typeof state.blocks)[number], { kind: 'tool' }>).tool;
    expect(tool.output).toContain('✅ 读代码');
    expect(tool.output).toContain('▶️ 写补丁');
  });

  it('未知/不渲染的 sessionUpdate（user_message_chunk / available_commands_update …）静默丢弃', () => {
    const mapper = new AcpEventMapper('t8');
    expect(
      mapAll(mapper, [
        { sessionUpdate: 'user_message_chunk', content: { type: 'text', text: '回放' } },
        { sessionUpdate: 'available_commands_update' },
        { sessionUpdate: 'current_mode_update' },
        { sessionUpdate: 'config_option_update' },
      ] as AcpUpdateLike[]),
    ).toEqual([]);
  });
});

describe('acp 事件映射：finish（stopReason → 终态）', () => {
  it('end_turn / cancelled / 未知值 → 恰好一个 done', () => {
    for (const reason of ['end_turn', 'cancelled', 'some_future_reason']) {
      const events = new AcpEventMapper('tf').finish(reason);
      expect(events).toEqual([{ type: 'done', turnId: 'tf' }]);
    }
  });

  it('refusal / max_tokens / max_turn_requests → 致命 error（willRetry=false）', () => {
    for (const reason of ['refusal', 'max_tokens', 'max_turn_requests']) {
      const events = new AcpEventMapper('tf').finish(reason);
      expect(events).toHaveLength(1);
      expect(events[0]).toMatchObject({ type: 'error', willRetry: false });
    }
  });

  it('携带 usage 时先发 usage 事件（cache token 计入 input），再发终态', () => {
    const events = new AcpEventMapper('tf').finish('end_turn', {
      inputTokens: 10,
      outputTokens: 5,
      cachedReadTokens: 100,
      cachedWriteTokens: 3,
    });
    expect(events[0]).toEqual({ type: 'usage', inputTokens: 113, outputTokens: 5 });
    expect(events[1]).toEqual({ type: 'done', turnId: 'tf' });
  });
});
