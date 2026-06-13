import { afterEach, describe, expect, it, vi } from 'vitest';
import type { BackendProbe } from '../src/agent/types';

// 隔离 listBackendCatalog（状态聚合）与 installBackend（installer 委托 + 501 预览态）。
// mock 整个 ../src/agent：给受控 catalog + 受控 doctor + effectiveDefaultBackend，
// 避免真机探测（codex 是否装会让断言飘）。installBackendDep 这里不用（service 走
// 注入的 deps.installBackend），但 index 仍需导出它（service import 了）。

// vi.mock 会被 hoist 到文件顶部 → 共享常量也得 hoist（vi.hoisted）才能被工厂引用。
const { CATALOG, doctorResults } = vi.hoisted(() => {
  const doctorResults: Record<string, BackendProbe> = {
    'codex-appserver': { ok: true, version: '1.2.3' },
    'claude-sdk': { ok: false, version: null, hint: '未安装，点下载', depState: 'not-installed', installable: true },
    'claude-acp': { ok: false, version: null, hint: '需手动装适配器', depState: 'external-missing' },
  };
  const CATALOG = [
    {
      id: 'codex-appserver',
      agentFamily: 'codex',
      displayName: 'Codex',
      access: 'app-server',
      dep: { kind: 'external-cli', pkg: 'codex', detectHint: '未找到 codex' },
      blurb: '能力最全',
    },
    {
      id: 'claude-sdk',
      agentFamily: 'claude',
      displayName: 'Claude（Agent SDK）',
      access: 'sdk',
      dep: {
        kind: 'npm-ondemand',
        pkg: '@anthropic-ai/claude-agent-sdk',
        version: '0.3.175',
        approxSizeMB: 224,
        detectHint: '未安装，点下载',
        installCmd: '点「下载 Claude SDK」',
      },
      supportedModes: ['full'],
      blurb: '开箱即用',
    },
    {
      id: 'claude-acp',
      agentFamily: 'claude',
      displayName: 'Claude（订阅·ACP）',
      access: 'acp',
      dep: {
        kind: 'npm-external',
        pkg: 'claude-code-acp',
        detectHint: '需手动装适配器',
        installCmd: 'npm i -g claude-code-acp',
      },
      supportedModes: ['full'],
      blurb: '走订阅计费',
    },
  ];
  return { CATALOG, doctorResults };
});

vi.mock('../src/agent', () => ({
  DEFAULT_BACKEND_ID: 'codex-appserver',
  BACKEND_CATALOG: CATALOG,
  backendIds: () => CATALOG.map((e) => e.id),
  createBackend: (id: string) => ({
    id,
    displayName: CATALOG.find((e) => e.id === id)?.displayName ?? id,
    doctor: async (): Promise<BackendProbe> => doctorResults[id] ?? { ok: false, version: null },
  }),
  catalogById: (id: string) => CATALOG.find((e) => e.id === id),
  isInstallable: (entry: { dep: { kind: string } }) => entry.dep.kind === 'npm-ondemand',
  effectiveDefaultBackend: async () => 'codex-appserver',
  installBackendDep: vi.fn(),
}));

import { createAdminService, NotWiredYetError } from '../src/admin/service';

afterEach(() => vi.clearAllMocks());

describe('listBackendCatalog · 状态聚合', () => {
  it('聚合 catalog + 三态 depState/installable/version/approxSize + 默认标记', async () => {
    const svc = createAdminService();
    const out = await svc.listBackendCatalog();
    expect(out.defaultBackend).toBe('codex-appserver');
    expect(out.entries.map((e) => e.id)).toEqual(['codex-appserver', 'claude-sdk', 'claude-acp']);

    const codex = out.entries.find((e) => e.id === 'codex-appserver')!;
    expect(codex).toMatchObject({
      depKind: 'external-cli',
      depState: 'installed',
      installable: false,
      version: '1.2.3',
      isDefault: true,
    });

    const sdk = out.entries.find((e) => e.id === 'claude-sdk')!;
    expect(sdk).toMatchObject({
      depKind: 'npm-ondemand',
      depState: 'not-installed',
      installable: true,
      approxSizeMB: 224,
      version: null,
      isDefault: false,
    });
    expect(sdk.hint).toContain('下载');
    expect(sdk.supportedModes).toEqual(['full']);

    const acp = out.entries.find((e) => e.id === 'claude-acp')!;
    expect(acp).toMatchObject({ depKind: 'npm-external', depState: 'external-missing', installable: false });
  });
});

describe('installBackend · installer 委托 + 守门', () => {
  it('daemon 注入态：installable 后端委托 installer（带版本 pin），透传进度/结果', async () => {
    const installer = vi.fn(async (pkg: string, onProgress?: (c: string) => void) => {
      onProgress?.('added 1 package\n');
      void pkg;
      return { ok: true as const, code: 0, aborted: false, tail: 'done' };
    });
    const svc = createAdminService({ installBackend: installer });
    const chunks: string[] = [];
    const r = await svc.installBackend('claude-sdk', (c) => chunks.push(c));
    expect(r.ok).toBe(true);
    // 带 pin 版本拼包名
    expect(installer).toHaveBeenCalledWith('@anthropic-ai/claude-agent-sdk@0.3.175', expect.any(Function), undefined);
    expect(chunks).toEqual(['added 1 package\n']);
  });

  it('external（claude-acp）不真装：返回 {ok:false} 带手动装法，不调 installer', async () => {
    const installer = vi.fn();
    const svc = createAdminService({ installBackend: installer });
    const r = await svc.installBackend('claude-acp');
    expect(r.ok).toBe(false);
    expect(r.tail).toContain('不支持一键下载');
    expect(r.tail).toContain('npm i -g claude-code-acp');
    expect(installer).not.toHaveBeenCalled();
  });

  it('未知后端 id → {ok:false}，不调 installer', async () => {
    const installer = vi.fn();
    const svc = createAdminService({ installBackend: installer });
    const r = await svc.installBackend('nope');
    expect(r.ok).toBe(false);
    expect(r.tail).toContain('未知后端');
    expect(installer).not.toHaveBeenCalled();
  });

  it('只读预览态（无 installer 注入）：installable 后端抛 NotWiredYetError（→ HTTP 501）', async () => {
    const svc = createAdminService(); // 无 deps.installBackend
    await expect(svc.installBackend('claude-sdk')).rejects.toBeInstanceOf(NotWiredYetError);
  });

  it('只读预览态 + external 后端：先被 external 分支拦下（不抛 501，给手动装法）', async () => {
    const svc = createAdminService();
    const r = await svc.installBackend('claude-acp');
    expect(r.ok).toBe(false);
    expect(r.tail).toContain('不支持一键下载');
  });
});
