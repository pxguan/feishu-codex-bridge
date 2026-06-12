import type { AgentEvent } from '../types';
import type { FileUpdateChange, ServerNotification, ThreadItem } from './protocol';

/** Files named in a fileChange title before collapsing to `等 N 个文件`. */
const TITLE_FILES_MAX = 2;
/**
 * Cap on the rendered diff body. Pre-fenced output bypasses the card layer's
 * OUTPUT_MAX belt (re-truncating would cut the closing fence), so the diff
 * caps itself here — same budget, and the fenced block stays well under the
 * card layer's BODY_TOTAL_MAX.
 */
const DIFF_MAX = 1200;

/**
 * Map one app-server ServerNotification to a normalized AgentEvent.
 * Returns null for notifications we don't surface (M1 subset).
 *
 * Streaming: token-level deltas via item/agentMessage/delta &
 * item/reasoning/textDelta; item/completed gives the final text for
 * reconciliation; commandExecution/fileChange map to tool blocks.
 */
export function mapNotification(n: ServerNotification): AgentEvent | null {
  switch (n.method) {
    case 'thread/started':
      return { type: 'system', threadId: n.params.thread.id };
    case 'turn/started':
      return { type: 'turn_started', turnId: n.params.turn.id };
    case 'item/agentMessage/delta':
      return { type: 'text_delta', itemId: n.params.itemId, delta: n.params.delta };
    case 'item/reasoning/textDelta':
      return { type: 'thinking_delta', itemId: n.params.itemId, delta: n.params.delta };
    case 'item/started':
      return mapItemStart(n.params.item);
    case 'item/completed':
      return mapItemComplete(n.params.item);
    case 'thread/tokenUsage/updated':
      // `last` (most recent turn), NOT `total` (cumulative session sum). `total`
      // only ever grows — it can exceed the window and never drops after a
      // /compact — so it's wrong for a "how full is the context right now" gauge.
      // `last.totalTokens` is the size of the latest request's context, which is
      // what gets re-sent next turn and what shrinks once compaction takes hold.
      return {
        type: 'context_usage',
        usedTokens: n.params.tokenUsage.last.totalTokens,
        contextWindow: n.params.tokenUsage.modelContextWindow,
      };
    case 'thread/compacted':
      return { type: 'context_compacted' };
    case 'turn/completed':
      return { type: 'done', turnId: n.params.turn.id };
    case 'thread/goal/updated': {
      const g = n.params.goal;
      return {
        type: 'goal_update',
        status: g.status,
        objective: g.objective,
        tokensUsed: g.tokensUsed,
        timeUsedSeconds: g.timeUsedSeconds,
        tokenBudget: g.tokenBudget,
      };
    }
    // thread/goal/cleared — we clear goals ourselves; nothing to surface.
    case 'error':
      return { type: 'error', message: n.params.error.message, willRetry: n.params.willRetry };
    default:
      return null;
  }
}

function mapItemStart(item: ThreadItem): AgentEvent | null {
  switch (item.type) {
    case 'commandExecution':
      return { type: 'tool_use', itemId: item.id, title: item.command, detail: String(item.cwd) };
    case 'fileChange':
      return { type: 'tool_use', itemId: item.id, title: fileChangeTitle(item.changes) };
    case 'webSearch':
      return { type: 'tool_use', itemId: item.id, title: '联网搜索' };
    case 'mcpToolCall':
    case 'dynamicToolCall':
      return { type: 'tool_use', itemId: item.id, title: '工具调用' };
    default:
      return null;
  }
}

function mapItemComplete(item: ThreadItem): AgentEvent | null {
  switch (item.type) {
    case 'agentMessage':
      return { type: 'text', itemId: item.id, text: item.text };
    case 'reasoning': {
      const text = item.content.length ? item.content.join('\n') : item.summary.join('\n');
      return { type: 'thinking', itemId: item.id, text };
    }
    case 'commandExecution':
      return {
        type: 'tool_result',
        itemId: item.id,
        output: item.aggregatedOutput ?? undefined,
        exitCode: item.exitCode,
      };
    case 'fileChange':
      return { type: 'tool_result', itemId: item.id, output: fileChangeDiffMd(item.changes) };
    case 'webSearch':
    case 'mcpToolCall':
    case 'dynamicToolCall':
      return { type: 'tool_result', itemId: item.id };
    default:
      return null;
  }
}

/** `编辑 src/foo.ts (+12 −3)` — multi-file lists the first N paths + a count;
 * +/− aggregate over every file's diff. Falls back to the old fixed label when
 * codex sent no changes. */
function fileChangeTitle(changes: FileUpdateChange[] | undefined): string {
  if (!changes?.length) return '编辑文件';
  let adds = 0;
  let dels = 0;
  for (const c of changes) {
    for (const line of c.diff.split('\n')) {
      if (line.startsWith('+') && !line.startsWith('+++')) adds++;
      else if (line.startsWith('-') && !line.startsWith('---')) dels++;
    }
  }
  const names = changes.slice(0, TITLE_FILES_MAX).map((c) => c.path).join('、');
  const files = changes.length > TITLE_FILES_MAX ? `${names} 等 ${changes.length} 个文件` : names;
  return `编辑 ${files} (+${adds} −${dels})`;
}

/** The changes as ONE pre-fenced ```diff block (truncated at {@link DIFF_MAX}).
 * The card layer passes pre-fenced output through as-is, so Feishu's diff
 * highlighting (red/green lines) survives. Multi-file chunks get a diff-native
 * `diff --git` header so each file stays identifiable inside the block. */
function fileChangeDiffMd(changes: FileUpdateChange[] | undefined): string | undefined {
  if (!changes?.length) return undefined;
  const joined = changes
    .map((c) => (changes.length > 1 ? `diff --git a/${c.path} b/${c.path}\n${c.diff}` : c.diff))
    .join('\n')
    .replace(/\n+$/, '');
  const cut = joined.length > DIFF_MAX;
  const body = cut ? `${joined.slice(0, DIFF_MAX)}…` : joined;
  const note = cut ? `\n_（已截断，完整 diff ${joined.length} 字符）_` : '';
  return `\`\`\`diff\n${body}\n\`\`\`${note}`;
}
