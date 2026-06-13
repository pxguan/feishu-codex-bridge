import type { AgentEvent } from '../types';

/**
 * ACP `session/update` → normalized {@link AgentEvent} mapping.
 *
 * The shapes below are MINIMAL STRUCTURAL VIEWS of the ACP schema types
 * (`SessionUpdate` in @agentclientprotocol/sdk) — only the fields this mapper
 * consumes. Structural typing keeps the mapper a pure, dependency-free state
 * machine（与 claude-sdk/event-map 同款思路）: the contract tests feed plain
 * objects, no SDK import, no process spawn. The real SDK values satisfy these
 * views at runtime, and ACP protocol types stay confined to src/agent/acp/.
 *
 * Event contract this mapper must honor (see card/run-state.ts):
 *   - deltas are keyed by `itemId`. ACP message chunks carry no item id, so we
 *     synthesize one and BUMP it whenever a tool call interleaves — otherwise
 *     text arriving after a tool panel would append to the block ABOVE it
 *     (run-state keeps blocks in arrival order, upsert by id).
 *   - `tool_use` → `tool_result` pair by the SAME id — ACP's `toolCallId` IS
 *     the itemId. `failed` status → exitCode 1 (run-state marks failure iff
 *     exitCode != null && !== 0).
 *   - ACP `plan` is whole-replace; run-state's tool_use always APPENDS a new
 *     panel, so we emit `tool_use` once per turn and fold every plan update
 *     into a `tool_result` on the same id (output replaces in place).
 *   - exactly one terminal per turn: {@link AcpEventMapper.finish} maps the
 *     prompt response's stopReason to `done` / fatal `error`.
 */

interface ContentBlockLike {
  type: string;
  text?: string;
}

/** `agent_message_chunk` / `agent_thought_chunk` / `user_message_chunk`. */
export interface AcpContentChunkLike {
  sessionUpdate: 'agent_message_chunk' | 'agent_thought_chunk' | 'user_message_chunk';
  content: ContentBlockLike;
  /** chunks of one message share a messageId; a change = a new message */
  messageId?: string | null;
}

/** ToolCallContent entries（`content` 常规块 / `diff` 文件修改 / `terminal`）。 */
interface ToolCallContentLike {
  type: string;
  /** type 'content' */
  content?: ContentBlockLike;
  /** type 'diff' */
  path?: string;
  newText?: string;
}

export interface AcpToolCallLike {
  sessionUpdate: 'tool_call';
  toolCallId: string;
  title: string;
  kind?: string | null;
  status?: string | null;
  content?: ToolCallContentLike[] | null;
  rawInput?: unknown;
}

export interface AcpToolCallUpdateLike {
  sessionUpdate: 'tool_call_update';
  toolCallId: string;
  title?: string | null;
  status?: string | null;
  content?: ToolCallContentLike[] | null;
  rawOutput?: unknown;
}

export interface AcpPlanLike {
  sessionUpdate: 'plan';
  entries: { content: string; priority?: string; status?: string }[];
}

/** `usage_update`：`used`/`size` 是上下文窗口语义（非每轮 token 数）。 */
export interface AcpUsageUpdateLike {
  sessionUpdate: 'usage_update';
  used: number;
  size?: number | null;
}

export type AcpUpdateLike =
  | AcpContentChunkLike
  | AcpToolCallLike
  | AcpToolCallUpdateLike
  | AcpPlanLike
  | AcpUsageUpdateLike
  | { sessionUpdate: string };

/** ACP PromptResponse.stopReason（开放 string：未来新增值按 done 兜底）。 */
export type AcpStopReason = string;

/** PromptResponse 可选携带的 token 用量（unstable，claude-pty-acp 不发）。 */
export interface AcpUsageLike {
  inputTokens?: number;
  outputTokens?: number;
  cachedReadTokens?: number | null;
  cachedWriteTokens?: number | null;
}

/** Flatten a tool call's content entries to plain text for the panel body. */
function toolContentText(content: ToolCallContentLike[] | null | undefined): string | undefined {
  if (!Array.isArray(content)) return undefined;
  const parts: string[] = [];
  for (const c of content) {
    if (c.type === 'content' && c.content?.type === 'text' && typeof c.content.text === 'string') {
      if (c.content.text) parts.push(c.content.text);
    } else if (c.type === 'diff' && typeof c.path === 'string') {
      // 不重建完整 diff（卡片层有自己的截断带）；标注改了哪个文件即可。
      parts.push(`[diff] ${c.path}`);
    }
    // type 'terminal'：bridge 的 terminal capability 为 false，server 不应发；忽略。
  }
  const text = parts.join('\n');
  return text || undefined;
}

/** 计划条目渲染成面板正文（整体替换语义 → 每次全量重渲）。 */
function renderPlan(entries: AcpPlanLike['entries']): string {
  return entries
    .map((e) => {
      const mark = e.status === 'completed' ? '✅' : e.status === 'in_progress' ? '▶️' : '⬜';
      return `${mark} ${e.content}`;
    })
    .join('\n');
}

/**
 * One mapper instance per turn (runStreamed call). Holds the streaming state a
 * turn accumulates: synthesized text/thinking item ids (bumped on tool
 * interleave / messageId change) and the plan panel's lifecycle.
 */
export class AcpEventMapper {
  private textSeq = 0;
  private textOpen = false;
  private textMsgId: string | undefined;
  private thinkingSeq = 0;
  private thinkingOpen = false;
  private planStarted = false;

  constructor(private readonly turnId: string) {}

  /** Map one session/update to zero-or-more normalized events. */
  map(update: AcpUpdateLike): AgentEvent[] {
    switch (update.sessionUpdate) {
      case 'agent_message_chunk':
        return this.mapText(update as AcpContentChunkLike);
      case 'agent_thought_chunk':
        return this.mapThinking(update as AcpContentChunkLike);
      case 'tool_call':
        return this.mapToolCall(update as AcpToolCallLike);
      case 'tool_call_update':
        return this.mapToolCallUpdate(update as AcpToolCallUpdateLike);
      case 'plan':
        return this.mapPlan(update as AcpPlanLike);
      case 'usage_update': {
        const u = update as AcpUsageUpdateLike;
        return [{ type: 'context_usage', usedTokens: u.used, contextWindow: u.size ?? null }];
      }
      default:
        // user_message_chunk（load 回放/回显）/ available_commands_update /
        // current_mode_update / config_option_update … — nothing to render.
        return [];
    }
  }

  /** Map the prompt response's stopReason (+optional usage) to the terminal events. */
  finish(stopReason: AcpStopReason, usage?: AcpUsageLike | null): AgentEvent[] {
    const out: AgentEvent[] = [];
    if (usage && (usage.inputTokens !== undefined || usage.outputTokens !== undefined)) {
      out.push({
        type: 'usage',
        // cache 读写也是模型真实消费的上下文 —— 与 claude-sdk 的口径一致。
        inputTokens: (usage.inputTokens ?? 0) + (usage.cachedReadTokens ?? 0) + (usage.cachedWriteTokens ?? 0),
        outputTokens: usage.outputTokens ?? 0,
      });
    }
    switch (stopReason) {
      case 'refusal':
        out.push({ type: 'error', message: 'ACP 后端拒绝继续本轮（refusal）', willRetry: false });
        break;
      case 'max_tokens':
      case 'max_turn_requests':
        out.push({ type: 'error', message: `ACP 后端因上限提前停止（${stopReason}），回复可能不完整`, willRetry: false });
        break;
      default:
        // end_turn / cancelled（⏹ 中止的确认点）/ 未来新增值 → 正常收尾。
        out.push({ type: 'done', turnId: this.turnId });
        break;
    }
    return out;
  }

  private mapText(chunk: AcpContentChunkLike): AgentEvent[] {
    if (chunk.content.type !== 'text' || !chunk.content.text) return [];
    const msgId = chunk.messageId ?? undefined;
    if (!this.textOpen || (msgId !== undefined && msgId !== this.textMsgId)) {
      this.textSeq++;
      this.textOpen = true;
      this.textMsgId = msgId;
    }
    return [{ type: 'text_delta', itemId: `${this.turnId}:text:${this.textSeq}`, delta: chunk.content.text }];
  }

  private mapThinking(chunk: AcpContentChunkLike): AgentEvent[] {
    if (chunk.content.type !== 'text' || !chunk.content.text) return [];
    if (!this.thinkingOpen) {
      this.thinkingSeq++;
      this.thinkingOpen = true;
    }
    return [
      { type: 'thinking_delta', itemId: `${this.turnId}:thinking:${this.thinkingSeq}`, delta: chunk.content.text },
    ];
  }

  private mapToolCall(call: AcpToolCallLike): AgentEvent[] {
    // 工具面板插队 → 关闭当前文本/思考 run，让后续 chunk 开新块（保持时序）。
    this.textOpen = false;
    this.thinkingOpen = false;
    const out: AgentEvent[] = [
      { type: 'tool_use', itemId: call.toolCallId, title: call.title || call.kind || '工具调用' },
    ];
    // 回放/迟到场景：tool_call 一来就已是终态 → 直接补上结果。
    if (call.status === 'completed' || call.status === 'failed') {
      out.push({
        type: 'tool_result',
        itemId: call.toolCallId,
        output: toolContentText(call.content),
        exitCode: call.status === 'failed' ? 1 : undefined,
      });
    }
    return out;
  }

  private mapToolCallUpdate(update: AcpToolCallUpdateLike): AgentEvent[] {
    // 非终态进度更新（pending/in_progress 的 content 增量）不重绘 —— 等终态一次给全。
    if (update.status !== 'completed' && update.status !== 'failed') return [];
    return [
      {
        type: 'tool_result',
        itemId: update.toolCallId,
        output: toolContentText(update.content),
        exitCode: update.status === 'failed' ? 1 : undefined,
      },
    ];
  }

  private mapPlan(plan: AcpPlanLike): AgentEvent[] {
    if (!plan.entries.length) return [];
    const itemId = `${this.turnId}:plan`;
    const out: AgentEvent[] = [];
    if (!this.planStarted) {
      this.planStarted = true;
      this.textOpen = false;
      this.thinkingOpen = false;
      out.push({ type: 'tool_use', itemId, title: '任务计划' });
    }
    // 整体替换语义：每次 plan 都全量重渲进同一面板（tool_result 覆盖 output）。
    out.push({ type: 'tool_result', itemId, output: renderPlan(plan.entries) });
    return out;
  }
}
