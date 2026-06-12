import { describe, expect, it } from 'vitest';
import { buildBackendCard, buildProjectSettingsCard, DM } from '../src/card/dm-cards';
import { validateBackendSwitch } from '../src/bot/handle-message';
import { backendIds, createBackend } from '../src/agent';
import type { BackendProbe } from '../src/agent/types';

/** 注册表动态选项（与 handle-message 的 backendChoices 同构，测试里直接取）。 */
const choices = backendIds().map((id) => ({ id, name: createBackend(id).displayName }));

/** Every selectMenu option {label,value} anywhere in the card tree. */
function collectOptions(card: object): { label: string; value: string }[] {
  const out: { label: string; value: string }[] = [];
  const walk = (n: unknown): void => {
    if (Array.isArray(n)) n.forEach(walk);
    else if (n && typeof n === 'object') {
      const o = n as Record<string, unknown>;
      if (typeof o.value === 'string' && o.text && typeof o.text === 'object') {
        const t = o.text as Record<string, unknown>;
        if (typeof t.content === 'string') out.push({ label: t.content, value: o.value });
      }
      Object.values(o).forEach(walk);
    }
  };
  walk(card);
  return out;
}

describe('buildBackendCard（DM 项目设置 → 🧠 后端）', () => {
  it('下拉选项 = 注册表动态后端列表（id 为值、displayName 为标签，默认项有标注）', () => {
    const card = buildBackendCard({ name: 'P', backend: undefined }, choices);
    const options = collectOptions(card);
    // 注册表里每个后端都成为一个可选项 —— 不硬编码（新后端注册即出现在卡上）
    for (const c of choices) {
      const opt = options.find((o) => o.value === c.id);
      expect(opt, `registry backend ${c.id} should be an option`).toBeDefined();
      expect(opt!.label).toContain(c.name);
    }
    expect(options.find((o) => o.value === 'codex-appserver')!.label).toContain('（默认）');
    expect(options.find((o) => o.value === 'claude-sdk')!.label).not.toContain('（默认）');
  });

  it('提交按钮携带 dm.proj.backend.submit + 项目名；返回按钮回项目设置', () => {
    const json = JSON.stringify(buildBackendCard({ name: 'my-app', backend: 'claude-sdk' }, choices));
    expect(json).toContain(DM.backendSubmit);
    expect(json).toContain(DM.projectSettings);
    expect(json).toContain('my-app');
  });

  it('注明「已有话题会话仍走原后端，新话题生效」（SessionRecord.backend 既有语义）', () => {
    const json = JSON.stringify(buildBackendCard({ name: 'P' }, choices));
    expect(json).toContain('切换只对新话题生效');
    expect(json).toContain('已有话题会话仍走原后端');
  });

  it('拒绝原因渲染在卡顶（切换失败 + 原因原文）', () => {
    const json = JSON.stringify(buildBackendCard({ name: 'P' }, choices, '后端「claude-sdk」当前不可用：未安装'));
    expect(json).toContain('切换失败');
    expect(json).toContain('当前不可用：未安装');
  });
});

describe('buildProjectSettingsCard 的 🧠 后端区块', () => {
  const base = { name: 'P', cwd: '/x', kind: 'multi' as const, origin: 'created' as const };

  it('显示当前后端（缺省 = codex-appserver），按钮携带 dm.proj.backend', () => {
    const json = JSON.stringify(buildProjectSettingsCard(base));
    expect(json).toContain('🧠 后端');
    expect(json).toContain(DM.backend);
    expect(json).toContain('codex-appserver'); // 缺省回退到默认 id
    expect(json).toContain('已有话题会话仍走原后端');
  });

  it('调用方传入展示名时优先用展示名', () => {
    const json = JSON.stringify(
      buildProjectSettingsCard({ ...base, backend: 'claude-sdk' }, 'Claude Code (Agent SDK)'),
    );
    expect(json).toContain('Claude Code (Agent SDK)');
  });
});

describe('validateBackendSwitch（切换校验的纯函数）', () => {
  const ok: BackendProbe = { ok: true, version: '1.0' };
  const registered = ['codex-appserver', 'claude-sdk'];

  it('注册表里没有的 id 拒绝，并列出可用后端', () => {
    const reason = validateBackendSwitch({ target: 'no-such', registered, project: {}, probe: ok });
    expect(reason).toContain('未知后端');
    expect(reason).toContain('codex-appserver');
  });

  it('doctor 探测不通过拒绝，并把 hint（装法/登录提示）带给用户', () => {
    const reason = validateBackendSwitch({
      target: 'claude-sdk',
      registered,
      project: {},
      probe: { ok: false, version: null, hint: '未安装 @anthropic-ai/claude-agent-sdk' },
    });
    expect(reason).toContain('不可用');
    expect(reason).toContain('未安装 @anthropic-ai/claude-agent-sdk');
  });

  it('探测没跑成（probe undefined）按不可用拒绝，绝不放行', () => {
    expect(validateBackendSwitch({ target: 'claude-sdk', registered, project: {} })).toContain('不可用');
  });

  it('目标后端仅支持 full 时：项目任一档不是 full 都拒绝并说明（含 guestMode 分档）', () => {
    const supportedModes = ['full'] as const;
    // 管理员档非 full
    expect(
      validateBackendSwitch({ target: 'claude-sdk', registered, project: { mode: 'qa' }, supportedModes, probe: ok }),
    ).toContain('仅支持');
    // 管理员档 full 但普通用户档 qa —— guest 档也必须被支持
    const reason = validateBackendSwitch({
      target: 'claude-sdk',
      registered,
      project: { mode: 'full', guestMode: 'qa' },
      supportedModes,
      probe: ok,
    });
    expect(reason).toContain('完全访问');
    expect(reason).toContain('🔐 权限');
  });

  it('全过返回 null：注册 + 探活 + 档位支持（缺省档 = full 视为 full）', () => {
    expect(
      validateBackendSwitch({
        target: 'claude-sdk',
        registered,
        project: {}, // 旧数据缺省 → effectiveMode 'full'
        supportedModes: ['full'],
        probe: ok,
      }),
    ).toBeNull();
  });

  it('supportedModes 未声明（codex）⇒ 任意档位放行', () => {
    expect(
      validateBackendSwitch({
        target: 'codex-appserver',
        registered,
        project: { mode: 'qa', guestMode: 'write' },
        probe: ok,
      }),
    ).toBeNull();
  });

  it('claude-sdk 后端实例声明 supportedModes=[full]（切换 UI 的提前拦截与硬守卫同源）', () => {
    expect(createBackend('claude-sdk').supportedModes).toEqual(['full']);
    expect(createBackend('codex-appserver').supportedModes).toBeUndefined();
  });
});
