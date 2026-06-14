import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * 棒A：claude-sdk doctor 三态（mock loader）、智能默认 effectiveDefaultBackend
 * 规则、ensureAnyAgent 放行。loader 与 codex/acp 探测都 mock，不碰真机环境。
 */

// ── 受控的 backend-loader（doctor 三态据它分流）──────────────────────────
const loaderState = {
  loadResult: 'ok' as 'ok' | 'not-installed' | 'broken',
  sdkInstalled: false,
};

vi.mock('../src/agent/backend-loader', async () => {
  const actual = await vi.importActual<typeof import('../src/agent/backend-loader')>(
    '../src/agent/backend-loader',
  );
  return {
    ...actual,
    loadBackendDep: vi.fn(async (pkg: string) => {
      if (loaderState.loadResult === 'not-installed') throw new actual.BackendNotInstalledError(pkg);
      if (loaderState.loadResult === 'broken') throw new Error('SyntaxError: broken module');
      return {};
    }),
    isBackendDepInstalled: vi.fn(() => loaderState.sdkInstalled),
  };
});

// ── 受控的 codex / acp 探测（detectAgents 据它推导默认）─────────────────
const detectState = { codexBin: null as string | null, codexVersion: null as string | null, acpCmd: null as unknown };

vi.mock('../src/agent/codex-appserver/locate', () => ({
  resolveCodexBin: vi.fn(() => detectState.codexBin),
  codexVersionAsync: vi.fn(async () => detectState.codexVersion),
}));

vi.mock('../src/agent/acp/backend', async () => {
  const actual = await vi.importActual<typeof import('../src/agent/acp/backend')>('../src/agent/acp/backend');
  return { ...actual, resolveAcpCommand: vi.fn(async () => detectState.acpCmd) };
});

import { ClaudeSdkBackend } from '../src/agent/claude-sdk/backend';
import { detectAgents, effectiveDefaultBackend, backendForProject } from '../src/agent/detect';
import { ensureAnyAgent } from '../src/bot/onboarding';

beforeEach(() => {
  loaderState.loadResult = 'ok';
  loaderState.sdkInstalled = false;
  detectState.codexBin = null;
  detectState.codexVersion = null;
  detectState.acpCmd = null;
});
afterEach(() => vi.clearAllMocks());

describe('claude-sdk doctor：三态（mock loader）', () => {
  const be = new ClaudeSdkBackend();

  it('已装（load 成功）→ ok + depState:installed', async () => {
    loaderState.loadResult = 'ok';
    const p = await be.doctor();
    expect(p.ok).toBe(true);
    expect(p.depState).toBe('installed');
  });

  it('未装（load 抛 BackendNotInstalledError）→ ok:false + installable + not-installed + hint 含「下载」', async () => {
    loaderState.loadResult = 'not-installed';
    const p = await be.doctor();
    expect(p.ok).toBe(false);
    expect(p.installable).toBe(true);
    expect(p.depState).toBe('not-installed');
    expect(p.hint).toContain('下载');
  });

  it('真坏（load 抛别的错）→ ok:false 但 NOT installable（绝不混成「未装·点下载」）', async () => {
    loaderState.loadResult = 'broken';
    const p = await be.doctor();
    expect(p.ok).toBe(false);
    expect(p.installable).toBeUndefined();
    expect(p.depState).toBeUndefined();
    expect(p.hint).toContain('加载失败');
  });
});

describe('智能默认 effectiveDefaultBackend（detect 推导）', () => {
  it('有 codex → codex-appserver（轻核心优先）', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    expect(await effectiveDefaultBackend({ force: true })).toBe('codex-appserver');
  });

  it('用户不可见闸：无 codex、SDK 装了 → 仍回退 codex-appserver 占位（hidden 后端绝不当默认；删 hidden 后应回 claude-sdk）', async () => {
    detectState.codexBin = null;
    loaderState.sdkInstalled = true;
    expect(await effectiveDefaultBackend({ force: true })).toBe('codex-appserver');
  });

  it('用户不可见闸：无 codex、SDK 未装、ACP 适配器在 → 仍回退 codex-appserver 占位（hidden 后端绝不当默认）', async () => {
    detectState.codexBin = null;
    loaderState.sdkInstalled = false;
    detectState.acpCmd = { command: '/opt/bin/claude-pty-acp', args: [] };
    expect(await effectiveDefaultBackend({ force: true })).toBe('codex-appserver');
  });

  it('都无 → 回退 codex-appserver 占位（doctor 会报需安装）', async () => {
    detectState.codexBin = null;
    loaderState.sdkInstalled = false;
    detectState.acpCmd = null;
    expect(await effectiveDefaultBackend({ force: true })).toBe('codex-appserver');
  });

  it('用户不可见闸：codex --version 失败（不可用）+ SDK 装了 → 仍 codex-appserver（hidden 后端不顶默认；删 hidden 后应落 claude-sdk）', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = null;
    loaderState.sdkInstalled = true;
    expect(await effectiveDefaultBackend({ force: true })).toBe('codex-appserver');
  });
});

describe('backendForProject：显式优先，否则有效默认', () => {
  it('项目显式选了后端 → 用它（不探测）', async () => {
    detectState.codexBin = null; // 即便默认是别的，显式值也优先
    expect(await backendForProject({ backend: 'claude-sdk' })).toBe('claude-sdk');
  });

  it('项目没选 → 落有效默认', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    expect(await backendForProject({}, { force: true })).toBe('codex-appserver');
  });
});

describe('detectAgents：按 agent 维度分组', () => {
  it('codex 装了 + claude-sdk 装了 → 两个 agent 各自 installed', async () => {
    detectState.codexBin = '/usr/bin/codex';
    detectState.codexVersion = 'codex 9.9';
    loaderState.sdkInstalled = true;
    const agents = await detectAgents();
    const codex = agents.find((a) => a.id === 'codex')!;
    const claude = agents.find((a) => a.id === 'claude')!;
    expect(codex.installed).toBe(true);
    expect(codex.backends[0]!.available).toBe(true);
    expect(claude.installed).toBe(true);
    expect(claude.backends.find((b) => b.backendId === 'claude-sdk')!.available).toBe(true);
    // SDK 未装才 installable；这里装了 → false
    expect(claude.backends.find((b) => b.backendId === 'claude-sdk')!.installable).toBe(false);
  });

  it('claude-sdk 未装 → 该后端 installable:true（Web 出下载按钮的信号）', async () => {
    loaderState.sdkInstalled = false;
    const agents = await detectAgents();
    const claude = agents.find((a) => a.id === 'claude')!;
    expect(claude.backends.find((b) => b.backendId === 'claude-sdk')!.installable).toBe(true);
  });
});

describe('ensureAnyAgent：任一 agent 可用即放行；都无也不阻塞', () => {
  it('只有 claude（SDK 装了，无 codex）→ 放行 true', async () => {
    detectState.codexBin = null;
    loaderState.sdkInstalled = true;
    expect(await ensureAnyAgent()).toBe(true);
  });

  it('都无 → 仍放行 true（告警但不阻塞，Web 引导下载）', async () => {
    detectState.codexBin = null;
    loaderState.sdkInstalled = false;
    detectState.acpCmd = null;
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      expect(await ensureAnyAgent()).toBe(true);
      expect(errSpy).toHaveBeenCalled(); // 打了告警
    } finally {
      errSpy.mockRestore();
    }
  });
});
