import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * codex-only：智能默认 effectiveDefaultBackend 规则、detectAgents 单 agent 分组、
 * backendForProject 显式优先/回退、ensureAnyAgent 放行。codex 探测 mock，不碰真机环境。
 */

// ── 受控的 codex 探测（detectAgents 据它推导默认）────────────────────────
const detectState = { codexBin: null as string | null, codexVersion: null as string | null };

vi.mock('../src/agent/codex-appserver/locate', () => ({
  resolveCodexBin: vi.fn(() => detectState.codexBin),
  codexVersionAsync: vi.fn(async () => detectState.codexVersion),
}));

import { detectAgents, effectiveDefaultBackend, backendForProject } from '../src/agent/detect';
import { ensureAnyAgent } from '../src/bot/onboarding';

beforeEach(() => {
  detectState.codexBin = null;
  detectState.codexVersion = null;
});
afterEach(() => vi.clearAllMocks());

describe('智能默认 effectiveDefaultBackend（detect 推导）', () => {
  it('有 codex → codex-appserver', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    expect(await effectiveDefaultBackend({ force: true })).toBe('codex-appserver');
  });

  it('都无 → 回退 codex-appserver 占位（doctor 会报需安装）', async () => {
    detectState.codexBin = null;
    detectState.codexVersion = null;
    expect(await effectiveDefaultBackend({ force: true })).toBe('codex-appserver');
  });
});

describe('backendForProject：显式优先（须为已注册后端），否则有效默认', () => {
  it('项目显式选了已注册后端 codex-appserver → 用它（不探测）', async () => {
    detectState.codexBin = null; // 即便探不到 codex，显式值也优先
    expect(await backendForProject({ backend: 'codex-appserver' })).toBe('codex-appserver');
  });

  it('项目指向已移除后端（旧 claude-sdk 配置）→ 回退有效默认 codex-appserver（避免 createBackend 抛未知后端）', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    expect(await backendForProject({ backend: 'claude-sdk' }, { force: true })).toBe('codex-appserver');
  });

  it('项目没选 → 落有效默认', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    expect(await backendForProject({}, { force: true })).toBe('codex-appserver');
  });
});

describe('detectAgents：按 agent 维度分组（仅 codex）', () => {
  it('codex 装了 → 单个 agent installed，其 codex-appserver 后端 available', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    const agents = await detectAgents();
    expect(agents).toHaveLength(1);
    const codex = agents.find((a) => a.id === 'codex')!;
    expect(codex.installed).toBe(true);
    expect(codex.backends).toHaveLength(1);
    expect(codex.backends[0]!.backendId).toBe('codex-appserver');
    expect(codex.backends[0]!.available).toBe(true);
    // external-cli 基线，绝不出「一键下载」信号
    expect(codex.backends[0]!.installable).toBe(false);
  });

  it('codex 未装 → 单个 agent not installed，后端 available:false（仍 installable:false，external-cli 手动装）', async () => {
    detectState.codexBin = null;
    detectState.codexVersion = null;
    const agents = await detectAgents();
    expect(agents).toHaveLength(1);
    const codex = agents.find((a) => a.id === 'codex')!;
    expect(codex.installed).toBe(false);
    expect(codex.backends[0]!.available).toBe(false);
    expect(codex.backends[0]!.installable).toBe(false);
  });
});

describe('ensureAnyAgent：agent 可用即放行；都无也不阻塞', () => {
  it('codex 可用 → 放行 true', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    expect(await ensureAnyAgent()).toBe(true);
  });

  it('都无 → 仍放行 true（告警但不阻塞，Web 引导下载）', async () => {
    detectState.codexBin = null;
    detectState.codexVersion = null;
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      expect(await ensureAnyAgent()).toBe(true);
      expect(errSpy).toHaveBeenCalled(); // 打了告警
    } finally {
      errSpy.mockRestore();
    }
  });
});
