import type { AgentEvent } from '../types';

/**
 * Claude Agent SDK stream → normalized {@link AgentEvent} mapping.
 *
 * The shapes below are MINIMAL STRUCTURAL VIEWS of the SDK's message types
 * (`SDKMessage` in @anthropic-ai/claude-agent-sdk) — only the fields this
 * mapper consumes. Structural typing keeps the mapper a pure, dependency-free
 * state machine: the contract tests feed plain objects, no SDK import, no API
 * call. The real `SDKMessage` values satisfy these views at runtime.
 *
 * Event contract this mapper must honor (see audit/04 F7 + card/run-state.ts):
 *   - deltas are keyed by `itemId`; a terminal `text`/`thinking` with the SAME
 *     id reconciles (replaces) the streamed buffer — so the stream itemId and
 *     the final-message itemId must match: `${message.id}:${blockIndex}`.
 *   - `tool_use` → `tool_result` pair by the SAME id — the API's tool_use id
 *     (`toolu_…`) links the assistant block to the user tool_result block, so
 *     it IS the itemId. `exitCode !== 0` marks failure (`is_error` → 1).
 *   - exactly one `done` per turn (from the SDK `result` message); a fatal
 *     `error` (willRetry=false) terminates the stream instead.
 *   - transient API retries map to `error` with willRetry=true (footer shows
 *     重试中, never flips the card to failed).
 */

interface TextBlockLike {
  type: 'text';
  text: string;
}
interface ThinkingBlockLike {
  type: 'thinking';
  thinking: string;
}
interface ToolUseBlockLike {
  type: 'tool_use';
  id: string;
  name: string;
  input?: unknown;
}
interface ToolResultBlockLike {
  type: 'tool_result';
  tool_use_id: string;
  content?: string | { type?: string; text?: string }[];
  is_error?: boolean;
}
type ContentBlockLike = TextBlockLike | ThinkingBlockLike | ToolUseBlockLike | ToolResultBlockLike | { type: string };

/** `SDKPartialAssistantMessage` (raw Anthropic stream event passthrough). */
export interface SdkStreamEventLike {
  type: 'stream_event';
  event: {
    type: string;
    /** message_start carries the API message id the content-block indexes hang off */
    message?: { id?: string };
    index?: number;
    delta?: { type?: string; text?: string; thinking?: string };
  };
  parent_tool_use_id?: string | null;
}

/** `SDKAssistantMessage` — the complete message (terminal reconcile + tools). */
export interface SdkAssistantMessageLike {
  type: 'assistant';
  message: { id?: string; content?: ContentBlockLike[] | string };
  parent_tool_use_id?: string | null;
}

/** `SDKUserMessage(Replay)` — tool_result blocks live on the user side. */
export interface SdkUserMessageLike {
  type: 'user';
  message: { content?: ContentBlockLike[] | string };
  parent_tool_use_id?: string | null;
}

/** `SDKSystemMessage` (subtype init) + `SDKAPIRetryMessage` (subtype api_retry). */
export interface SdkSystemMessageLike {
  type: 'system';
  subtype?: string;
  session_id?: string;
  /** api_retry */
  attempt?: number;
  max_retries?: number;
  error?: string;
}

/** `SDKResultMessage` — the turn terminal. */
export interface SdkResultMessageLike {
  type: 'result';
  subtype: string;
  is_error?: boolean;
  result?: string;
  errors?: string[];
  usage?: {
    input_tokens?: number;
    output_tokens?: number;
    cache_creation_input_tokens?: number;
    cache_read_input_tokens?: number;
  };
  /** Per-model usage (SDKResultMessage.modelUsage). Each entry carries the
   * model's total `contextWindow` — the ONLY place the SDK surfaces the window
   * size, so /context + the run-card gauge读这里算「已用 / 窗口」百分比（与
   * 终端 statusline 的 context_window.used_percentage 同源数据）。 */
  modelUsage?: Record<string, { contextWindow?: number }>;
}

export type SdkMessageLike =
  | SdkStreamEventLike
  | SdkAssistantMessageLike
  | SdkUserMessageLike
  | SdkSystemMessageLike
  | SdkResultMessageLike
  | { type: string };

/** 工具调用的人话标题（卡片折叠面板头），仿 codex event-map 的命令/文件样式。 */
export function toolTitle(name: string, input: unknown): { title: string; detail?: string } {
  const i = (input && typeof input === 'object' ? input : {}) as Record<string, unknown>;
  const s = (v: unknown): string => (typeof v === 'string' ? v : '');
  switch (name) {
    case 'Bash':
      return { title: s(i.command) || 'Bash', detail: s(i.description) || undefined };
    case 'Read':
      return { title: `读取 ${s(i.file_path)}`.trim() };
    case 'Write':
      return { title: `写入 ${s(i.file_path)}`.trim() };
    case 'Edit':
      return { title: `编辑 ${s(i.file_path)}`.trim() };
    case 'Glob':
    case 'Grep':
      return { title: `${name}: ${s(i.pattern)}`.trim() };
    case 'WebSearch':
      return { title: `联网搜索：${s(i.query)}` };
    case 'WebFetch':
      return { title: `抓取网页：${s(i.url)}` };
    case 'Task':
      return { title: `子任务：${s(i.description) || '(后台代理)'}` };
    case 'TodoWrite':
      return { title: '更新待办清单' };
    default:
      return { title: name };
  }
}

/** Flatten a tool_result's content (string or text-block array) to plain text. */
function toolResultText(content: ToolResultBlockLike['content']): string | undefined {
  if (typeof content === 'string') return content || undefined;
  if (!Array.isArray(content)) return undefined;
  const text = content
    .map((c) => (c && c.type === 'text' && typeof c.text === 'string' ? c.text : ''))
    .filter(Boolean)
    .join('\n');
  return text || undefined;
}

/**
 * One mapper instance per turn (runStreamed call). Holds the streaming state a
 * turn accumulates: the current API message id (stream itemIds hang off it)
 * and the synthesized turn id the `done` event reports.
 */
export class ClaudeEventMapper {
  /** message id from the latest message_start — keys stream-delta itemIds */
  private currentMsgId = 'msg';

  constructor(private readonly turnId: string) {}

  /** Map one SDK message to zero-or-more normalized events (pure per message). */
  map(msg: SdkMessageLike): AgentEvent[] {
    switch (msg.type) {
      case 'stream_event':
        return this.mapStreamEvent(msg as SdkStreamEventLike);
      case 'assistant':
        return this.mapAssistant(msg as SdkAssistantMessageLike);
      case 'user':
        return this.mapUser(msg as SdkUserMessageLike);
      case 'system':
        return this.mapSystem(msg as SdkSystemMessageLike);
      case 'result':
        return this.mapResult(msg as SdkResultMessageLike);
      default:
        // status / hook / task / notification … — nothing to render.
        return [];
    }
  }

  private mapStreamEvent(msg: SdkStreamEventLike): AgentEvent[] {
    // Subagent (Task) internals stream with parent_tool_use_id set — the parent
    // tool panel already represents them; don't interleave their text.
    if (msg.parent_tool_use_id) return [];
    const ev = msg.event;
    switch (ev.type) {
      case 'message_start':
        if (ev.message?.id) this.currentMsgId = ev.message.id;
        return [];
      case 'content_block_delta': {
        const itemId = `${this.currentMsgId}:${ev.index ?? 0}`;
        if (ev.delta?.type === 'text_delta' && ev.delta.text) {
          return [{ type: 'text_delta', itemId, delta: ev.delta.text }];
        }
        if (ev.delta?.type === 'thinking_delta' && ev.delta.thinking) {
          return [{ type: 'thinking_delta', itemId, delta: ev.delta.thinking }];
        }
        return []; // input_json_delta / signature_delta — not rendered
      }
      default:
        // tool_use panels are emitted from the COMPLETE assistant message (its
        // input is fully formed there; a content_block_start only has {} args,
        // and the message completes before the tool actually executes).
        return [];
    }
  }

  private mapAssistant(msg: SdkAssistantMessageLike): AgentEvent[] {
    if (msg.parent_tool_use_id) return [];
    const msgId = msg.message.id ?? this.currentMsgId;
    const content = msg.message.content;
    if (typeof content === 'string') {
      return content ? [{ type: 'text', itemId: `${msgId}:0`, text: content }] : [];
    }
    if (!Array.isArray(content)) return [];
    const out: AgentEvent[] = [];
    content.forEach((block, i) => {
      switch (block.type) {
        case 'text': {
          const text = (block as TextBlockLike).text;
          // Same `${messageId}:${index}` key as the stream deltas → reconcile.
          if (text) out.push({ type: 'text', itemId: `${msgId}:${i}`, text });
          break;
        }
        case 'thinking': {
          const text = (block as ThinkingBlockLike).thinking;
          if (text) out.push({ type: 'thinking', itemId: `${msgId}:${i}`, text });
          break;
        }
        case 'tool_use': {
          const t = block as ToolUseBlockLike;
          const { title, detail } = toolTitle(t.name, t.input);
          // The API tool_use id pairs this with the upcoming tool_result.
          out.push({ type: 'tool_use', itemId: t.id, title, detail });
          break;
        }
        default:
          break; // redacted_thinking / server_tool_use … — skipped
      }
    });
    return out;
  }

  private mapUser(msg: SdkUserMessageLike): AgentEvent[] {
    if (msg.parent_tool_use_id) return [];
    const content = msg.message.content;
    if (!Array.isArray(content)) return [];
    const out: AgentEvent[] = [];
    for (const block of content) {
      if (block.type !== 'tool_result') continue;
      const r = block as ToolResultBlockLike;
      out.push({
        type: 'tool_result',
        itemId: r.tool_use_id,
        output: toolResultText(r.content),
        // run-state marks failure iff exitCode != null && !== 0; the API only
        // gives a boolean, so map is_error → 1 (and success → undefined).
        exitCode: r.is_error ? 1 : undefined,
      });
    }
    return out;
  }

  private mapSystem(msg: SdkSystemMessageLike): AgentEvent[] {
    if (msg.subtype === 'init' && msg.session_id) {
      return [{ type: 'system', threadId: msg.session_id }];
    }
    if (msg.subtype === 'api_retry') {
      return [
        {
          type: 'error',
          message: `API 瞬断，重试中（${msg.attempt ?? '?'} / ${msg.max_retries ?? '?'}）`,
          willRetry: true,
        },
      ];
    }
    return [];
  }

  private mapResult(msg: SdkResultMessageLike): AgentEvent[] {
    const u = msg.usage;
    // cache reads/writes ARE context the model consumed — count them in, so the
    // number reflects the real request size (parity with codex's last-request
    // semantics).
    const usedTokens =
      (u?.input_tokens ?? 0) + (u?.cache_read_input_tokens ?? 0) + (u?.cache_creation_input_tokens ?? 0);
    const usage: AgentEvent = { type: 'usage', inputTokens: usedTokens, outputTokens: u?.output_tokens ?? 0 };
    // context_usage drives the run-card 上下文进度条 + /context（与 codex 同事件）。
    // window 取 modelUsage 各模型里最大的 contextWindow（主对话模型；子代理可能用更小
    // 窗口的模型）；拿不到则 null → /context 显示 token 数但不显百分比（诚实降级）。
    const out: AgentEvent[] = [usage];
    const window = maxContextWindow(msg.modelUsage);
    if (window || usedTokens) out.push({ type: 'context_usage', usedTokens, contextWindow: window });
    if (msg.subtype === 'success' && !msg.is_error) {
      out.push({ type: 'done', turnId: this.turnId });
      return out;
    }
    const detail = msg.errors?.filter(Boolean).join('；') || msg.result || msg.subtype;
    out.push({ type: 'error', message: `Claude 运行失败：${detail}`, willRetry: false });
    return out;
  }
}

/** Largest `contextWindow` across the result's per-model usage map (the main
 * conversation model has the widest window; subagents may run smaller ones).
 * null when the SDK reported no window → /context degrades to「token 数，无百分比」. */
function maxContextWindow(modelUsage: SdkResultMessageLike['modelUsage']): number | null {
  if (!modelUsage) return null;
  let max = 0;
  for (const mu of Object.values(modelUsage)) {
    if (typeof mu?.contextWindow === 'number' && mu.contextWindow > max) max = mu.contextWindow;
  }
  return max > 0 ? max : null;
}
