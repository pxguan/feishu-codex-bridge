import { describe, expect, it } from 'vitest';
import { buildHelpCard, buildResumeCard, buildWelcomeCard, type ResumeCardState } from '../src/card/command-cards';
import type { ThreadSummary } from '../src/agent/types';

function buttons(node: unknown, acc: any[] = []): any[] {
  if (Array.isArray(node)) node.forEach((n) => buttons(n, acc));
  else if (node && typeof node === 'object') {
    const o = node as Record<string, unknown>;
    if (o.tag === 'button') acc.push(o);
    for (const k of Object.keys(o)) buttons(o[k], acc);
  }
  return acc;
}

function state(threads: ThreadSummary[]): ResumeCardState {
  return { chatId: 'oc_x', originalMsgId: 'om_x', requesterOpenId: 'ou_x', cwd: '/x', threads, createdAt: Date.now() };
}

const NOW = Math.floor(Date.now() / 1000);

describe('buildResumeCard', () => {
  it('renders one labeled button per session (time · title), unambiguous', () => {
    const longTitle = 'X-Alimail-AntiSpam:AC=CONTINUE;BC=-1|1;BR=01201311R271S09ruler050_05242_201210;CH=green';
    const threads: ThreadSummary[] = [
      { sessionId: 'c1', preview: '查看功能', name: '查看功能', createdAt: NOW - 1500, updatedAt: NOW - 1500 },
      { sessionId: 'c2', preview: longTitle, name: '', createdAt: NOW - 20 * 86400, updatedAt: NOW - 20 * 86400 },
      // two same-title sessions, different times → must get different labels
      { sessionId: 'c3', preview: '分析 SLS 告警', name: '分析 SLS 告警', createdAt: NOW - 20 * 86400, updatedAt: NOW - 20 * 86400 },
      { sessionId: 'c4', preview: '分析 SLS 告警', name: '分析 SLS 告警', createdAt: NOW - 20 * 86400 - 4000, updatedAt: NOW - 20 * 86400 - 4000 },
    ];
    const card = buildResumeCard(state(threads));
    const btns = buttons(card);

    // exactly one button per session, each carrying its sessionId
    expect(btns.length).toBe(4);
    expect(btns.map((b) => b.behaviors[0].value.t)).toEqual(['c1', 'c2', 'c3', 'c4']);
    // every label is the time·title button (no bare "恢复" buttons)
    for (const b of btns) expect(b.text.content).toMatch(/^↩️ .+ · /);
    // long/garbage title is truncated to one line with …
    const longLabel = btns[1].text.content as string;
    expect(longLabel).toContain('…');
    expect(longLabel.length).toBeLessThan(60); // bounded, not the full ~90-char title
    expect(longLabel).not.toContain('\n');
    // same-title sessions are disambiguated by the timestamp
    expect(btns[2].text.content).not.toBe(btns[3].text.content);
  });

  it('shows an empty-state hint and no buttons when there is no history', () => {
    const card = buildResumeCard(state([]));
    expect(buttons(card).length).toBe(0);
    expect(JSON.stringify(card)).toContain('还没有历史会话');
  });

  // M-8：回调值带 backend 标识（b），pick → readHistory → 重绑全程同后端。
  it('carries the backend id in each pick button value when the state has one', () => {
    const threads: ThreadSummary[] = [
      { sessionId: 'c1', preview: 'x', createdAt: NOW - 60, updatedAt: NOW - 60 },
    ];
    const withBackend = buildResumeCard({ ...state(threads), backend: 'claude-sdk' });
    expect(buttons(withBackend)[0].behaviors[0].value).toMatchObject({ t: 'c1', b: 'claude-sdk' });
    // 旧 state（无 backend）→ 不带 b，handler 落回默认后端
    const legacy = buildResumeCard(state(threads));
    expect(buttons(legacy)[0].behaviors[0].value.b).toBeUndefined();
  });
});

describe('buildHelpCard 权限过滤', () => {
  it('主群区：非管理员看不到 owner-only 命令(/resume、/settings)，但对话类命令仍在', () => {
    const json = JSON.stringify(buildHelpCard('main', true, false));
    expect(json).not.toContain('/resume');
    expect(json).not.toContain('/settings');
    expect(json).toContain('/model'); // 对话类命令对所有人开放
    expect(json).toContain('/help');
  });

  it('主群区：管理员能看到全部命令', () => {
    const json = JSON.stringify(buildHelpCard('main', true, true));
    expect(json).toContain('/resume');
    expect(json).toContain('/settings');
    expect(json).toContain('/model');
  });

  it('单会话群：/settings 仅管理员可见，/model 始终可见', () => {
    expect(JSON.stringify(buildHelpCard('single', true, false))).not.toContain('/settings');
    expect(JSON.stringify(buildHelpCard('single', true, true))).toContain('/settings');
    expect(JSON.stringify(buildHelpCard('single', true, false))).toContain('/model');
  });
});

describe('/goal 可发现性', () => {
  it('三个 scope 的 help 卡都列出 /goal（非管理员也可见）', () => {
    for (const scope of ['main', 'topic', 'single'] as const) {
      expect(JSON.stringify(buildHelpCard(scope, true, false))).toContain('/goal');
    }
  });

  it('欢迎卡两种群类型都列出 /goal（默认 = codex，caps undefined）', () => {
    expect(JSON.stringify(buildWelcomeCard('multi'))).toContain('/goal');
    expect(JSON.stringify(buildWelcomeCard('single'))).toContain('/goal');
  });
});

describe('buildWelcomeCard 按后端能力裁剪命令（三后端一致性，与 /help 同源 caps）', () => {
  // claude-sdk：compact 已实现(true)，但 goal/resume 仍未支持。
  const sdkCaps = { goal: false, compact: true, resume: false };
  // claude-acp：goal/compact/resume 全不支持。
  const acpCaps = { goal: false, compact: false, resume: false };

  it('claude-sdk：欢迎卡不列 /goal、不列 /resume，但仍列 /compact 与 /context（compact 已实现）', () => {
    for (const kind of ['multi', 'single'] as const) {
      const j = JSON.stringify(buildWelcomeCard(kind, undefined, true, sdkCaps));
      expect(j).not.toContain('/goal');
      expect(j).not.toContain('/resume');
      expect(j).toContain('/model'); // 共享能力仍在
    }
    const multi = JSON.stringify(buildWelcomeCard('multi', undefined, true, sdkCaps));
    expect(multi).toContain('/compact'); // sdk 支持 → 话题内仍列压缩
    expect(multi).toContain('/context');
  });

  it('claude-acp：欢迎卡不列 /goal /resume /compact —— 话题内只剩「/context → 看上下文」', () => {
    const multi = JSON.stringify(buildWelcomeCard('multi', undefined, true, acpCaps));
    expect(multi).not.toContain('/goal');
    expect(multi).not.toContain('/resume');
    expect(multi).not.toContain('/compact');
    expect(multi).toContain('/context'); // 看占用仍在
    expect(multi).toContain('/model');
  });

  it('codex（caps undefined ⇒ 全 true）：欢迎卡仍全列 /goal /resume /compact（向后兼容）', () => {
    const multi = JSON.stringify(buildWelcomeCard('multi'));
    expect(multi).toContain('/goal');
    expect(multi).toContain('/resume');
    expect(multi).toContain('/compact');
  });
});
