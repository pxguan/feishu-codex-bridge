import { describe, expect, it } from 'vitest';
import { buildHelpCard, type HelpScope } from '../src/card/command-cards';

// /help 按后端能力裁剪命令清单（三后端一致性）：claude-sdk/acp 的 capabilities
// goal/compact/resume 都 false → 速查卡不列这些 codex 专属命令，避免「列了点了才发现
// 不支持」。codex（capabilities undefined ⇒ 全 true）保持全列。

const claudeCaps = { goal: false, compact: false, resume: false };
const scopes: HelpScope[] = ['single', 'topic', 'main'];

describe('buildHelpCard 后端能力裁剪', () => {
  it('默认（不传 caps，=codex）→ 三 scope 仍列 /goal（向后兼容）', () => {
    for (const s of scopes) expect(JSON.stringify(buildHelpCard(s, true, true))).toContain('/goal');
  });

  it('claude 能力（goal/compact/resume 全 false）→ 三 scope 都不列 /goal', () => {
    for (const s of scopes) {
      expect(JSON.stringify(buildHelpCard(s, true, true, claudeCaps))).not.toContain('/goal');
    }
  });

  it('claude：single/topic 不列 /compact（但仍列 /model、/context）', () => {
    for (const s of ['single', 'topic'] as HelpScope[]) {
      const j = JSON.stringify(buildHelpCard(s, true, true, claudeCaps));
      expect(j).not.toContain('/compact');
      expect(j).toContain('/model'); // 共享能力仍在
      expect(j).toContain('/context');
    }
  });

  it('claude 主群区（管理员）→ 不列 /resume，但 /settings 仍在', () => {
    const j = JSON.stringify(buildHelpCard('main', true, true, claudeCaps));
    expect(j).not.toContain('/resume');
    expect(j).toContain('/settings');
  });

  it('codex 主群区（管理员）→ 仍列 /resume', () => {
    const j = JSON.stringify(buildHelpCard('main', true, true)); // 无 caps = codex
    expect(j).toContain('/resume');
  });

  it('部分能力：goal 支持但 compact 不支持 → 列 /goal 不列 /compact', () => {
    const j = JSON.stringify(buildHelpCard('single', true, true, { goal: true, compact: false }));
    expect(j).toContain('/goal');
    expect(j).not.toContain('/compact');
  });
});
