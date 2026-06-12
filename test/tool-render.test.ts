import { describe, expect, it } from 'vitest';
import { toolBodyMd, toolHeaderText } from '../src/card/tool-render';
import type { ToolEntry } from '../src/card/run-state';

function tool(over: Partial<ToolEntry> = {}): ToolEntry {
  return { id: 't1', title: 'npm test', status: 'done', ...over };
}

describe('toolHeaderText', () => {
  it('prefixes the status icon and collapses whitespace', () => {
    expect(toolHeaderText(tool({ title: 'npm\n  test' }))).toBe('✅ **npm test**');
    expect(toolHeaderText(tool({ status: 'error' }))).toContain('❌');
    expect(toolHeaderText(tool({ status: 'running' }))).toContain('⏳');
  });
});

describe('toolBodyMd', () => {
  it('fences raw command output as ```bash (Error label on failure)', () => {
    expect(toolBodyMd(tool({ output: 'ok' }))).toBe('**Output**\n```bash\nok\n```');
    expect(toolBodyMd(tool({ output: 'boom', status: 'error' }))).toBe('**Error**\n```bash\nboom\n```');
  });

  it('passes pre-fenced output (fileChange ```diff blocks) through untouched', () => {
    const diff = '```diff\n+new\n-old\n```';
    expect(toolBodyMd(tool({ output: diff }))).toBe(`**Output**\n${diff}`);
  });

  it('truncates oversized raw output inside the fence (fence stays closed)', () => {
    const body = toolBodyMd(tool({ output: 'y'.repeat(5000) }));
    expect(body.length).toBeLessThan(1400);
    expect(body.startsWith('**Output**\n```bash\n')).toBe(true);
    expect(body).toMatch(/```\s*$/);
  });

  it('renders the running/empty placeholders without output', () => {
    expect(toolBodyMd(tool({ status: 'running' }))).toBe('_运行中…_');
    expect(toolBodyMd(tool())).toBe('');
  });
});
