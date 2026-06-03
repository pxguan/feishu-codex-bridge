import { describe, expect, it } from 'vitest';
import { effectiveMode } from '../src/project/registry';
import { sandboxParams } from '../src/agent/codex-appserver/backend';
import { buildProjectSettingsCard } from '../src/card/dm-cards';

describe('effectiveMode', () => {
  it('defaults missing mode to full (legacy data unaffected)', () => {
    expect(effectiveMode({})).toBe('full');
    expect(effectiveMode({ mode: undefined })).toBe('full');
  });
  it('passes explicit tiers through', () => {
    expect(effectiveMode({ mode: 'qa' })).toBe('qa');
    expect(effectiveMode({ mode: 'write' })).toBe('write');
    expect(effectiveMode({ mode: 'full' })).toBe('full');
  });
});

describe('sandboxParams', () => {
  // Force a platform for one assertion, then restore — keeps these deterministic
  // regardless of the host the tests run on.
  function withPlatform<T>(value: NodeJS.Platform, fn: () => T): T {
    const orig = process.platform;
    Object.defineProperty(process, 'platform', { value, configurable: true });
    try {
      return fn();
    } finally {
      Object.defineProperty(process, 'platform', { value: orig, configurable: true });
    }
  }

  it('full (and undefined) → danger-full-access, no permissions config (any platform)', () => {
    for (const plat of ['darwin', 'win32', 'linux'] as const) {
      withPlatform(plat, () => {
        expect(sandboxParams('full', false)).toEqual({ sandbox: 'danger-full-access' });
        expect(sandboxParams(undefined, true)).toEqual({ sandbox: 'danger-full-access' });
      });
    }
  });

  // qa/write read-confinement is enforceable on macOS (Seatbelt) and native
  // Windows (restricted token) — assert the profile shape on both.
  for (const plat of ['darwin', 'win32'] as const) {
    it(`${plat}: qa → read-only confined to workspace, network off`, () => {
      withPlatform(plat, () => {
        const p = sandboxParams('qa', false) as any;
        expect(p.sandbox).toBeUndefined();
        expect(p.config.default_permissions).toBe('feishu');
        const fs = p.config.permissions.feishu.filesystem;
        expect(fs[':minimal']).toBe('read');
        expect(fs[':workspace_roots']['.']).toBe('read');
        expect(p.config.permissions.feishu.network.enabled).toBe(false);
      });
    });
    it(`${plat}: write → workspace write + network honored`, () => {
      withPlatform(plat, () => {
        const p = sandboxParams('write', true) as any;
        expect(p.config.permissions.feishu.filesystem[':workspace_roots']['.']).toBe('write');
        expect(p.config.permissions.feishu.network.enabled).toBe(true);
      });
    });
  }

  it('fail-closed on Linux/WSL: qa/write throw (reads not confined there), full still works', () => {
    withPlatform('linux', () => {
      expect(() => sandboxParams('qa', false)).toThrow();
      expect(() => sandboxParams('write', false)).toThrow();
      expect(sandboxParams('full', false)).toEqual({ sandbox: 'danger-full-access' });
    });
  });
});

describe('permission cards', () => {
  // The 🔐 权限 block lives only in the DM project-settings container
  // (buildProjectSettingsCard), driven by DM.setMode/DM.setNetwork actions and
  // resolving the project by the `n` (name) the buttons carry.
  it('project settings card lists all three tiers + the DM toggle action ids', () => {
    const json = JSON.stringify(
      buildProjectSettingsCard({ name: 'P', cwd: '/x', kind: 'multi', origin: 'created', mode: 'qa', network: false }),
    );
    expect(json).toContain('项目设置');
    expect(json).toContain('项目内只读');
    expect(json).toContain('项目内读写');
    expect(json).toContain('完全访问');
    expect(json).toContain('dm.proj.mode');
    expect(json).toContain('dm.proj.network'); // network toggle shown because mode !== full
    expect(json).toContain('"n":"P"'); // buttons carry the project name
    expect(json).toContain('dm.projects'); // ⬅️ 项目列表 back button
  });

  it('full tier hides the network toggle (always networked)', () => {
    const json = JSON.stringify(
      buildProjectSettingsCard({ name: 'P', cwd: '/x', kind: 'multi', origin: 'created', mode: 'full', network: false }),
    );
    expect(json).not.toContain('dm.proj.network');
    expect(json).toContain('恒为联网');
  });
});
