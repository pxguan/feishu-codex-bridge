import { describe, expect, it } from 'vitest';
import {
  buildBackendDetectingCard,
  buildBackendPickerCard,
  buildProjectSettingsCard,
  DM,
  type BackendProbeRow,
} from '../src/card/dm-cards';
import { probeBackends, validateBackendSwitch } from '../src/bot/handle-message';
import { backendIds, createBackend } from '../src/agent';
import type { BackendProbe } from '../src/agent/types';

/** Every callback button {label, value} anywhere in the card tree. */
function collectButtons(card: object): { label: string; value: Record<string, unknown> }[] {
  const out: { label: string; value: Record<string, unknown> }[] = [];
  const walk = (n: unknown): void => {
    if (Array.isArray(n)) n.forEach(walk);
    else if (n && typeof n === 'object') {
      const o = n as Record<string, unknown>;
      if (o.tag === 'button' && Array.isArray(o.behaviors)) {
        const b = o.behaviors[0] as { type?: string; value?: Record<string, unknown> };
        const t = o.text as { content?: string } | undefined;
        if (b?.type === 'callback' && b.value) out.push({ label: t?.content ?? '', value: b.value });
      }
      Object.values(o).forEach(walk);
    }
  };
  walk(card);
  return out;
}

/** 「切换」button whose value carries target backend id `b`, or undefined. */
function switchButtonFor(card: object, backendId: string) {
  return collectButtons(card).find((b) => b.value.a === DM.backendSubmit && b.value.b === backendId);
}

const okCodex: BackendProbeRow = {
  id: 'codex-appserver',
  name: 'Codex (app-server)',
  probe: { ok: true, version: '0.139.0' },
};
const okClaude: BackendProbeRow = {
  id: 'claude-sdk',
  name: 'Claude Code (Agent SDK)',
  probe: { ok: true, version: '2.1.0' },
  supportedModes: ['full'],
};

describe('buildBackendDetectingCard（🧠 后端 · 第一段检测中间态）', () => {
  it('点击后立即可见的轻量反馈：检测中文案 + 项目名', () => {
    const json = JSON.stringify(buildBackendDetectingCard({ name: 'my-app' }));
    expect(json).toContain('正在检测本机可用后端');
    expect(json).toContain('my-app');
  });
});

describe('buildBackendPickerCard（第二段检测结果卡）三态渲染', () => {
  it('可用且非当前 → ✅ 一行一个「切换」按钮，value 直接带目标 id（单点直达）', () => {
    const card = buildBackendPickerCard({ name: 'P', backend: undefined }, [okCodex, okClaude]);
    const btn = switchButtonFor(card, 'claude-sdk');
    expect(btn).toBeDefined();
    expect(btn!.label).toBe('切换');
    expect(btn!.value.n).toBe('P'); // 复用既有 backendSubmit 校验+写盘，项目名随按钮走
    expect(JSON.stringify(card)).toContain('✅ **Claude Code (Agent SDK)** 2.1.0');
  });

  it('当前后端行标注「✓ 使用中」且无切换按钮；默认后端有（默认）标注', () => {
    const card = buildBackendPickerCard({ name: 'P', backend: undefined }, [okCodex, okClaude]);
    expect(switchButtonFor(card, 'codex-appserver')).toBeUndefined(); // 缺省 = codex 即当前
    const json = JSON.stringify(card);
    expect(json).toContain('✓ 使用中');
    expect(json).toContain('（默认）');
  });

  it('不可用 → ❌ 灰字附 doctor hint，无切换按钮', () => {
    const dead: BackendProbeRow = {
      id: 'claude-acp',
      name: 'Claude（订阅·ACP）',
      probe: { ok: false, version: null, hint: '未检测到 claude-pty-acp' },
    };
    const card = buildBackendPickerCard({ name: 'P' }, [okCodex, dead]);
    expect(switchButtonFor(card, 'claude-acp')).toBeUndefined();
    expect(JSON.stringify(card)).toContain('未检测到 claude-pty-acp');
  });

  it('探测超时/没跑成（probe undefined）按不可用渲染，绝不给切换按钮', () => {
    const timedOut: BackendProbeRow = { id: 'claude-sdk', name: 'Claude Code (Agent SDK)', probe: undefined };
    const card = buildBackendPickerCard({ name: 'P' }, [okCodex, timedOut]);
    expect(switchButtonFor(card, 'claude-sdk')).toBeUndefined();
    expect(JSON.stringify(card)).toContain('探测超时');
  });

  it('非 full 档项目对仅 full 后端：灰显「需完全访问档」提前告知，无按钮（而非点了才拒）', () => {
    const card = buildBackendPickerCard({ name: 'P', mode: 'qa', guestMode: 'qa' }, [okCodex, okClaude]);
    expect(switchButtonFor(card, 'claude-sdk')).toBeUndefined();
    const json = JSON.stringify(card);
    expect(json).toContain('完全访问');
    expect(json).toContain('🔐 权限');
  });

  it('切换失败原因渲染在卡顶（backendSubmit 拒绝后 patch 回本卡）', () => {
    const json = JSON.stringify(
      buildBackendPickerCard({ name: 'P' }, [okCodex], '后端「claude-sdk」当前不可用：未安装'),
    );
    expect(json).toContain('切换失败');
    expect(json).toContain('当前不可用：未安装');
  });

  it('尾部带 🔄 重新检测（回 dm.proj.backend）+ ⬅️ 返回设置，并注明「新话题生效」语义', () => {
    const card = buildBackendPickerCard({ name: 'P' }, [okCodex]);
    const buttons = collectButtons(card);
    expect(buttons.find((b) => b.value.a === DM.backend && b.value.n === 'P')).toBeDefined();
    expect(buttons.find((b) => b.value.a === DM.projectSettings && b.value.n === 'P')).toBeDefined();
    const json = JSON.stringify(card);
    expect(json).toContain('切换只对新话题生效');
    expect(json).toContain('已有话题会话仍走原后端');
  });

  it('注册表动态：注册了什么后端结果卡就列什么（新后端注册即自动出现）', () => {
    const rows: BackendProbeRow[] = backendIds().map((id) => {
      const be = createBackend(id);
      return { id, name: be.displayName, probe: { ok: true, version: '1.0' }, supportedModes: be.supportedModes };
    });
    const json = JSON.stringify(buildBackendPickerCard({ name: 'P' }, rows));
    for (const id of backendIds()) expect(json).toContain(createBackend(id).displayName);
  });
});

describe('probeBackends（并行 doctor + 单个超时兜底）', () => {
  const fast = (id: string, probe: BackendProbe) => ({
    id,
    displayName: id.toUpperCase(),
    supportedModes: undefined,
    doctor: async () => probe,
  });

  it('并行探测：每后端各成一行，带 id/displayName/supportedModes/probe，且 doctor 走 force（绕过缓存）', async () => {
    let seenForce: boolean | undefined;
    const be = {
      id: 'a',
      displayName: 'A',
      supportedModes: ['full'] as const,
      doctor: async (o?: { force?: boolean }) => {
        seenForce = o?.force;
        return { ok: true, version: '1.0' } satisfies BackendProbe;
      },
    };
    const rows = await probeBackends([be], 1000);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ id: 'a', name: 'A', probe: { ok: true, version: '1.0' } });
    expect(rows[0]!.supportedModes).toEqual(['full']);
    expect(seenForce).toBe(true);
  });

  it('单个后端卡死 → 超时兜底归一成 probe undefined（按不可用渲染），不拖垮同批其他后端', async () => {
    const hang = { id: 'h', displayName: 'H', supportedModes: undefined, doctor: () => new Promise<BackendProbe>(() => {}) };
    const rows = await probeBackends([hang, fast('ok', { ok: true, version: '2' })], 50);
    expect(rows[0]!.probe).toBeUndefined();
    expect(rows[1]!.probe).toEqual({ ok: true, version: '2' });
  });

  it('doctor 抛错同样归一成 probe undefined，绝不放行', async () => {
    const boom = {
      id: 'b',
      displayName: 'B',
      supportedModes: undefined,
      doctor: async (): Promise<BackendProbe> => {
        throw new Error('spawn ENOENT');
      },
    };
    const rows = await probeBackends([boom], 1000);
    expect(rows[0]!.probe).toBeUndefined();
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

  it('notice 提示行渲染在卡顶（切换成功后的「✅ 已切到 xxx · 新话题生效」留痕）', () => {
    const card = buildProjectSettingsCard(base, 'Codex (app-server)', '✅ 已切到 **Codex (app-server)** · 新话题生效');
    const first = JSON.stringify((card.body as { elements: unknown[] }).elements[0]);
    expect(first).toContain('已切到');
    expect(first).toContain('新话题生效');
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
