import { describe, expect, it } from 'vitest';
import type { AgentEvent } from '../src/agent/types';
import { ClaudeEventMapper, toolTitle, type SdkMessageLike } from '../src/agent/claude-sdk/event-map';
import { initialState, reduce, type RunState } from '../src/card/run-state';

/**
 * 契约测试（audit/04 F7）：以 golden 的 SDK 消息序列喂映射器，断言产出的
 * AgentEvent 满足 run-state 的隐式契约（itemId 键控 / 同 id reconcile 替换 /
 * tool_use↔tool_result 同 id 配对 / 单 done 关流 / willRetry 语义），并把事件
 * 直接折进 reduce 验证终态。纯对象 mock —— 不 import SDK、不 spawn、不调 API。
 */

function mapAll(mapper: ClaudeEventMapper, msgs: SdkMessageLike[]): AgentEvent[] {
  return msgs.flatMap((m) => mapper.map(m));
}

function reduceAll(events: AgentEvent[]): RunState {
  return events.reduce(reduce, initialState);
}

/** golden：一轮「流式文本 → 完整 assistant 消息 → result」 */
const TEXT_TURN: SdkMessageLike[] = [
  { type: 'system', subtype: 'init', session_id: 'sess-1' },
  { type: 'stream_event', event: { type: 'message_start', message: { id: 'msg_a' } }, parent_tool_use_id: null },
  { type: 'stream_event', event: { type: 'content_block_start', index: 0 }, parent_tool_use_id: null },
  {
    type: 'stream_event',
    event: { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: '你好' } },
    parent_tool_use_id: null,
  },
  {
    type: 'stream_event',
    event: { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: '，世界' } },
    parent_tool_use_id: null,
  },
  { type: 'stream_event', event: { type: 'content_block_stop', index: 0 }, parent_tool_use_id: null },
  {
    type: 'assistant',
    message: { id: 'msg_a', content: [{ type: 'text', text: '你好，世界！' }] },
    parent_tool_use_id: null,
  },
  {
    type: 'result',
    subtype: 'success',
    is_error: false,
    result: '你好，世界！',
    usage: { input_tokens: 7, output_tokens: 12, cache_read_input_tokens: 100, cache_creation_input_tokens: 3 },
  },
];

/** golden：一轮「思考 → Bash 工具调用（成功）→ 失败的 Read → 收尾文本 → result」 */
const TOOL_TURN: SdkMessageLike[] = [
  { type: 'stream_event', event: { type: 'message_start', message: { id: 'msg_t1' } }, parent_tool_use_id: null },
  {
    type: 'stream_event',
    event: { type: 'content_block_delta', index: 0, delta: { type: 'thinking_delta', thinking: '先看看目录' } },
    parent_tool_use_id: null,
  },
  {
    type: 'assistant',
    message: {
      id: 'msg_t1',
      content: [
        { type: 'thinking', thinking: '先看看目录结构。' },
        { type: 'tool_use', id: 'toolu_01', name: 'Bash', input: { command: 'ls -la', description: '列目录' } },
      ],
    },
    parent_tool_use_id: null,
  },
  {
    type: 'user',
    message: { content: [{ type: 'tool_result', tool_use_id: 'toolu_01', content: 'total 8\nREADME.md' }] },
    parent_tool_use_id: null,
  },
  {
    type: 'assistant',
    message: {
      id: 'msg_t2',
      content: [{ type: 'tool_use', id: 'toolu_02', name: 'Read', input: { file_path: '/etc/shadow' } }],
    },
    parent_tool_use_id: null,
  },
  {
    type: 'user',
    message: {
      content: [
        { type: 'tool_result', tool_use_id: 'toolu_02', content: [{ type: 'text', text: 'EACCES' }], is_error: true },
      ],
    },
    parent_tool_use_id: null,
  },
  {
    type: 'assistant',
    message: { id: 'msg_t3', content: [{ type: 'text', text: '目录里有一个 README。' }] },
    parent_tool_use_id: null,
  },
  { type: 'result', subtype: 'success', is_error: false, usage: { input_tokens: 50, output_tokens: 30 } },
];

describe('claude-sdk 事件映射：golden 序列 → AgentEvent 契约', () => {
  it('流式 text_delta 与终态 text 共用同一 itemId（${msgId}:${index}）→ reconcile 替换而非追加', () => {
    const events = mapAll(new ClaudeEventMapper('t1'), TEXT_TURN);
    const deltas = events.filter((e) => e.type === 'text_delta');
    const finals = events.filter((e) => e.type === 'text');
    expect(deltas).toHaveLength(2);
    expect(finals).toHaveLength(1);
    expect(deltas[0]!.itemId).toBe('msg_a:0');
    expect(finals[0]!.itemId).toBe('msg_a:0'); // 同 id —— run-state 才能替换流式缓冲

    const state = reduceAll(events);
    const textBlocks = state.blocks.filter((b) => b.kind === 'text');
    expect(textBlocks).toHaveLength(1); // 没有因 id 不一致产生重复块
    expect((textBlocks[0] as Extract<(typeof state.blocks)[number], { kind: 'text' }>).content).toBe('你好，世界！');
  });

  it('恰好一个 done（来自 result success），带 usage（含 cache token 计入 input）', () => {
    const events = mapAll(new ClaudeEventMapper('t2'), TEXT_TURN);
    const dones = events.filter((e) => e.type === 'done');
    expect(dones).toHaveLength(1);
    expect(dones[0]).toMatchObject({ type: 'done', turnId: 't2' });
    const usage = events.find((e) => e.type === 'usage');
    expect(usage).toMatchObject({ inputTokens: 110, outputTokens: 12 }); // 7+100+3

    const state = reduceAll(events);
    expect(state.terminal).toBe('done');
    expect(state.footer).toBeNull();
  });

  it('result 带 modelUsage → 补发 context_usage（used=input+cache，window=最大 contextWindow）→ 驱动 /context 与进度条', () => {
    const events = mapAll(new ClaudeEventMapper('tc'), [
      {
        type: 'result',
        subtype: 'success',
        is_error: false,
        usage: { input_tokens: 1000, output_tokens: 50, cache_read_input_tokens: 20000, cache_creation_input_tokens: 4000 },
        // 主模型 200k 窗口；子代理用 haiku 的小窗口不应被选中（取最大）。
        modelUsage: { 'claude-sonnet-4-6': { contextWindow: 200000 }, 'claude-haiku': { contextWindow: 50000 } },
      },
    ]);
    const cu = events.find((e) => e.type === 'context_usage');
    expect(cu).toMatchObject({ type: 'context_usage', usedTokens: 25000, contextWindow: 200000 }); // 1000+20000+4000
    // context_usage 必须在 done 之前（run loop 命中 done 即终止该轮）。
    const iCu = events.findIndex((e) => e.type === 'context_usage');
    const iDone = events.findIndex((e) => e.type === 'done');
    expect(iCu).toBeGreaterThanOrEqual(0);
    expect(iCu).toBeLessThan(iDone);
  });

  it('result 无 modelUsage（窗口未知）→ context_usage.contextWindow=null（诚实降级，/context 只显 token 数不显百分比）', () => {
    const events = mapAll(new ClaudeEventMapper('tc2'), [
      { type: 'result', subtype: 'success', is_error: false, usage: { input_tokens: 500, output_tokens: 10 } },
    ]);
    const cu = events.find((e) => e.type === 'context_usage');
    expect(cu).toMatchObject({ type: 'context_usage', usedTokens: 500, contextWindow: null });
  });

  it('system/init 映射为 system 事件并携带 session id', () => {
    const events = mapAll(new ClaudeEventMapper('t3'), TEXT_TURN);
    expect(events[0]).toEqual({ type: 'system', threadId: 'sess-1' });
  });

  it('tool_use/tool_result 以 API 的 toolu id 配对；is_error → exitCode 1 → 卡片标失败', () => {
    const events = mapAll(new ClaudeEventMapper('t4'), TOOL_TURN);
    const uses = events.filter((e) => e.type === 'tool_use');
    const results = events.filter((e) => e.type === 'tool_result');
    expect(uses.map((u) => u.itemId)).toEqual(['toolu_01', 'toolu_02']);
    expect(results.map((r) => r.itemId)).toEqual(['toolu_01', 'toolu_02']);
    expect(uses[0]).toMatchObject({ title: 'ls -la', detail: '列目录' });
    expect(uses[1]).toMatchObject({ title: '读取 /etc/shadow' });

    const state = reduceAll(events);
    const tools = state.blocks.filter((b) => b.kind === 'tool');
    expect(tools).toHaveLength(2);
    const [ok, failed] = tools as Extract<(typeof state.blocks)[number], { kind: 'tool' }>[];
    expect(ok!.tool).toMatchObject({ status: 'done', output: 'total 8\nREADME.md' });
    expect(failed!.tool).toMatchObject({ status: 'error', exitCode: 1, output: 'EACCES' });
    expect(state.terminal).toBe('done');
  });

  it('thinking_delta 与终态 thinking 同 id 收敛进 reasoning（不进正文 blocks）', () => {
    const events = mapAll(new ClaudeEventMapper('t5'), TOOL_TURN);
    const state = reduceAll(events);
    expect(state.reasoning).toHaveLength(1);
    expect(state.reasoning[0]!.text).toBe('先看看目录结构。'); // 终态替换了流式缓冲
  });

  it('api_retry → error(willRetry=true)：卡片转「重试中」而非失败终态', () => {
    const mapper = new ClaudeEventMapper('t6');
    const events = mapAll(mapper, [
      { type: 'system', subtype: 'api_retry', attempt: 1, max_retries: 3 },
      ...TEXT_TURN.slice(1), // 重试成功后正常走完
    ]);
    const retry = events[0]!;
    expect(retry).toMatchObject({ type: 'error', willRetry: true });
    const state = reduceAll(events);
    expect(state.terminal).toBe('done'); // willRetry 不终止流
  });

  it('result error → error(willRetry=false) 终态，不发 done', () => {
    const events = mapAll(new ClaudeEventMapper('t7'), [
      {
        type: 'result',
        subtype: 'error_during_execution',
        is_error: true,
        errors: ['模型超时'],
        usage: { input_tokens: 5, output_tokens: 0 },
      },
    ]);
    expect(events.some((e) => e.type === 'done')).toBe(false);
    const err = events.find((e) => e.type === 'error');
    expect(err).toMatchObject({ willRetry: false });
    expect((err as { message: string }).message).toContain('模型超时');
    const state = reduceAll(events);
    expect(state.terminal).toBe('error');
  });

  it('子代理消息（parent_tool_use_id 非空）整体跳过 —— 由父 Task 工具面板代表', () => {
    const events = mapAll(new ClaudeEventMapper('t8'), [
      {
        type: 'stream_event',
        event: { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: '内部输出' } },
        parent_tool_use_id: 'toolu_task',
      },
      {
        type: 'assistant',
        message: { id: 'msg_sub', content: [{ type: 'text', text: '子代理内部回复' }] },
        parent_tool_use_id: 'toolu_task',
      },
      {
        type: 'user',
        message: { content: [{ type: 'tool_result', tool_use_id: 'toolu_x', content: 'x' }] },
        parent_tool_use_id: 'toolu_task',
      },
    ]);
    expect(events).toEqual([]);
  });

  it('未知消息类型（status/hook/task 通知等）安静忽略', () => {
    const mapper = new ClaudeEventMapper('t9');
    expect(mapper.map({ type: 'system', subtype: 'status' })).toEqual([]);
    expect(mapper.map({ type: 'tool_progress' })).toEqual([]);
    expect(mapper.map({ type: 'auth_status' })).toEqual([]);
  });
});

describe('toolTitle 人话标题', () => {
  it('常见工具有专属标题，未知工具回退到工具名', () => {
    expect(toolTitle('Bash', { command: 'npm test' })).toEqual({ title: 'npm test', detail: undefined });
    expect(toolTitle('Edit', { file_path: 'src/a.ts' })).toEqual({ title: '编辑 src/a.ts' });
    expect(toolTitle('WebSearch', { query: 'vitest docs' })).toEqual({ title: '联网搜索：vitest docs' });
    expect(toolTitle('Grep', { pattern: 'TODO' })).toEqual({ title: 'Grep: TODO' });
    expect(toolTitle('SomeMcpTool', {})).toEqual({ title: 'SomeMcpTool' });
    expect(toolTitle('Bash', null)).toEqual({ title: 'Bash', detail: undefined });
  });
});
