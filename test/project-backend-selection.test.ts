import { describe, expect, it } from 'vitest';
import { safeBackendId, bindModeFor } from '../src/bot/handle-message';
import { assertBackendUsable } from '../src/project/lifecycle';

/**
 * 「创建时选后端」的两道关：飞书表单值的防伪收口（safeBackendId）+ 落地前的可用性
 * 校验（assertBackendUsable）。这两条是 review wf_28088b2e #1/#12 点的关键不变量
 * （伪造拦截、卸载竞态防御），此前零断言覆盖。
 */

describe('safeBackendId —— 飞书表单 backend 值的防伪收口', () => {
  it('合法注册 id 原样放行（字符串 / 数组 / {value} 三形态都收）', () => {
    expect(safeBackendId({ backend: 'claude-sdk' })).toBe('claude-sdk');
    expect(safeBackendId({ backend: ['claude-sdk'] })).toBe('claude-sdk');
    expect(safeBackendId({ backend: { value: 'codex-appserver' } })).toBe('codex-appserver');
  });

  it('未选 / 空 → undefined（落回默认 codex）', () => {
    expect(safeBackendId({})).toBeUndefined();
    expect(safeBackendId({ backend: undefined })).toBeUndefined();
    expect(safeBackendId(undefined)).toBeUndefined();
  });

  it('伪造 / 未注册 id → 丢弃为 undefined（防表单篡改注入任意后端）', () => {
    expect(safeBackendId({ backend: 'gpt-9' })).toBeUndefined();
    expect(safeBackendId({ backend: 'claude-sdk; rm -rf' })).toBeUndefined();
    expect(safeBackendId({ backend: '../../etc' })).toBeUndefined();
  });
});

describe('bindModeFor —— 绑定『已有群』按所选后端定档（claude 可选 + 外部群仍默认 qa）', () => {
  it('codex / 未选 → undefined（沿用 joinExistingGroup 的 qa 只读默认，外部群安全不变）', () => {
    expect(bindModeFor('codex-appserver')).toBeUndefined();
    expect(bindModeFor(undefined)).toBeUndefined();
  });

  it('claude 系（supportedModes=[full]，不含 qa）→ full：显式选了才以完全访问档绑定，' +
    '否则 assertBackendUsable 会当场拒「不支持 qa 档」', () => {
    expect(bindModeFor('claude-sdk')).toBe('full');
    expect(bindModeFor('claude-acp')).toBe('full');
    // 选了 claude → full，assertBackendUsable 放行（之前 qa 写死会拒、根本选不了）。
    expect(() => assertBackendUsable('claude-sdk', bindModeFor('claude-sdk') ?? 'qa')).not.toThrow();
    expect(() => assertBackendUsable('claude-acp', bindModeFor('claude-acp') ?? 'qa')).not.toThrow();
  });

  it('未知 id → undefined（不臆造档；落地时 assertBackendUsable 再兜底拒绝）', () => {
    expect(bindModeFor('no-such-backend')).toBeUndefined();
  });
});

describe('assertBackendUsable —— 落地前校验「已下载 + 支持该档」（防卡渲染↔提交间卸载竞态）', () => {
  it('backend 未设（落回默认 codex）→ 放行不抛', () => {
    expect(() => assertBackendUsable(undefined, 'full')).not.toThrow();
  });

  it('codex 基线（external-cli）→ 恒可用，任意档放行（不参与已下载判定）', () => {
    expect(() => assertBackendUsable('codex-appserver', 'full')).not.toThrow();
    expect(() => assertBackendUsable('codex-appserver', 'qa')).not.toThrow();
  });

  it('未注册的后端 id → 抛「当前不可用」', () => {
    expect(() => assertBackendUsable('no-such-backend', 'full')).toThrow(/不可用/);
  });

  it('claude 系仅支持 full —— 在 qa 档下不可选（supportedModes 过滤，与安装无关）→ 抛', () => {
    // qa 档下 projectCreatableBackends 不含 claude-sdk（supportedModes=['full']），故拒。
    expect(() => assertBackendUsable('claude-sdk', 'qa')).toThrow(/不可用/);
  });
});
