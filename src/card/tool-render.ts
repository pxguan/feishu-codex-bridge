import type { ToolEntry } from './run-state';

const HEADER_TITLE_MAX = 80;
const OUTPUT_MAX = 1200;
/**
 * Cumulative cap on a tool's body markdown. Output blocks can stack to
 * multi-KB which, across many panels, pushes the card past Feishu's
 * per-element size limit (~30KB) and 400s the whole stream. Last belt.
 */
const BODY_TOTAL_MAX = 2500;

/** One-line panel header: status icon + title (the command, for shell). */
export function toolHeaderText(tool: ToolEntry): string {
  const icon = tool.status === 'done' ? '✅' : tool.status === 'error' ? '❌' : '⏳';
  return `${icon} **${escapeInline(truncate(tool.title, HEADER_TITLE_MAX))}**`;
}

/**
 * Panel body: the command's output as a ```bash block (Error on non-zero exit)
 * — the language tag buys Feishu's shell highlighting for free. Pre-fenced
 * output (the fileChange mapping ships a ready, self-truncated ```diff block)
 * passes through untouched so its highlighting and closing fence survive.
 */
export function toolBodyMd(tool: ToolEntry): string {
  if (!tool.output) {
    return tool.status === 'running' ? '_运行中…_' : '';
  }
  const label = tool.status === 'error' ? 'Error' : 'Output';
  const block = tool.output.startsWith('```') ? tool.output : bashBlock(tool.output);
  const body = `**${label}**\n${block}`;
  if (body.length <= BODY_TOTAL_MAX) return body;
  return `${body.slice(0, BODY_TOTAL_MAX)}…\n\n_（已截断，完整输出 ${tool.output.length} 字符）_`;
}

/** Raw output in a ```bash fence; over OUTPUT_MAX it's cut with a size note.
 * The note states the full size, NOT "see the log" — full tool output never
 * reaches the bridge log, and group members have no log access anyway. */
function bashBlock(output: string): string {
  const note = output.length > OUTPUT_MAX ? `\n_（已截断，完整输出 ${output.length} 字符）_` : '';
  return `\`\`\`bash\n${truncate(output, OUTPUT_MAX)}\n\`\`\`${note}`;
}

function truncate(s: string, max: number): string {
  return s.length > max ? `${s.slice(0, max)}…` : s;
}

/** Collapse whitespace so a multi-line command stays on the header's one line. */
function escapeInline(s: string): string {
  return s.replace(/\s+/g, ' ').trim();
}
