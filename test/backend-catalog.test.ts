import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  BACKEND_CATALOG,
  catalogBackendIds,
  catalogById,
  catalogByFamily,
  isInstallable,
  projectCreatableBackends,
} from '../src/agent/catalog';
import { backendIds, createBackend } from '../src/agent';
import {
  BackendNotInstalledError,
  isBackendDepInstalled,
  loadBackendDep,
} from '../src/agent/backend-loader';
import { buildInstallCommand, stripVersion } from '../src/agent/installer';
import { paths } from '../src/config/paths';

/**
 * 棒A 地基：catalog↔REGISTRY 配对、按需加载器三路径、installer 命令构建（不真跑
 * npm）、claude-sdk doctor 三态、智能默认规则、ensureAnyAgent 放行。
 */

describe('catalog ↔ REGISTRY 配对（防漏注册）', () => {
  it('catalogBackendIds == backendIds（两处都注册才不红）', () => {
    expect([...catalogBackendIds()].sort()).toEqual([...backendIds()].sort());
  });

  it('每条 catalog id 都能 createBackend 出一个实例（工厂存在）', () => {
    for (const entry of BACKEND_CATALOG) {
      const be = createBackend(entry.id);
      expect(be.id).toBe(entry.id);
    }
  });

  it('catalog 的 supportedModes 与 backend 实例声明一致', () => {
    for (const entry of BACKEND_CATALOG) {
      const be = createBackend(entry.id);
      expect(entry.supportedModes).toEqual(be.supportedModes);
    }
  });

  it('三条初始后端齐全：codex(external-cli)/claude-sdk(npm-ondemand·库)/claude-acp(npm-ondemand·bin)', () => {
    expect(catalogById('codex-appserver')?.dep.kind).toBe('external-cli');
    expect(catalogById('claude-sdk')?.dep.kind).toBe('npm-ondemand');
    expect(catalogById('claude-acp')?.dep.kind).toBe('npm-ondemand');
  });

  it('npm-ondemand 都可一键下载（isInstallable）；external-cli 不可', () => {
    expect(isInstallable(catalogById('claude-sdk')!)).toBe(true);
    expect(isInstallable(catalogById('claude-acp')!)).toBe(true);
    expect(isInstallable(catalogById('codex-appserver')!)).toBe(false);
  });

  it('claude-sdk 是库类（无 binName，走 import）；claude-acp 是 bin 类（有 binName，走 spawn）', () => {
    expect(catalogById('claude-sdk')!.dep.binName).toBeUndefined();
    expect(catalogById('claude-acp')!.dep.binName).toBe('claude-pty-acp');
  });

  it('claude-sdk dep 标注包名 + 体积（version 省略走 latest，让下载/更新取最新）', () => {
    const sdk = catalogById('claude-sdk')!;
    expect(sdk.dep.pkg).toBe('@anthropic-ai/claude-agent-sdk');
    expect(sdk.dep.version).toBeUndefined();
    expect(sdk.dep.approxSizeMB).toBeGreaterThan(100);
  });

  it('claude-acp dep 标注包名 + 体积（version 省略走 latest——自管适配器跟新）', () => {
    const acp = catalogById('claude-acp')!;
    expect(acp.dep.pkg).toBe('claude-pty-acp');
    expect(acp.dep.version).toBeUndefined();
    expect(acp.dep.approxSizeMB).toBeGreaterThan(0);
  });

  it('catalogByFamily 分组：codex 1 条 / claude 2 条', () => {
    expect(catalogByFamily('codex').map((e) => e.id)).toEqual(['codex-appserver']);
    expect(catalogByFamily('claude').map((e) => e.id).sort()).toEqual(['claude-acp', 'claude-sdk']);
  });
});

describe('projectCreatableBackends —— 飞书新建/绑定卡的「可选后端」过滤（创建时选定）', () => {
  const ids = (mode: 'qa' | 'write' | 'full', inst: (e: { id: string }) => boolean) =>
    projectCreatableBackends(mode, inst).map((e) => e.id);

  it('codex 是默认基线 → 即便都「未下载」也始终可选（external-cli 不参与下载判定）', () => {
    expect(ids('full', () => false)).toEqual(['codex-appserver']);
  });

  it('full 档 + 全已下载 → codex + claude 两条都在（claude 系 supportedModes 含 full）', () => {
    expect(ids('full', () => true).sort()).toEqual(['claude-acp', 'claude-sdk', 'codex-appserver']);
  });

  it('qa 档（外部群）→ 只剩 codex：claude 系仅支持 full，不含 qa 被档位过滤掉', () => {
    expect(ids('qa', () => true)).toEqual(['codex-appserver']);
  });

  it('按「已下载」过滤：只有 claude-sdk 装了 → codex(默认) + claude-sdk，claude-acp 不显示', () => {
    expect(ids('full', (e) => e.id === 'claude-sdk').sort()).toEqual(['claude-sdk', 'codex-appserver']);
  });
});

describe('backend-loader：按需依赖加载（三路径）', () => {
  const origBackendsDir = paths.backendsDir;
  let userDir: string;

  beforeEach(() => {
    userDir = mkdtempSync(join(tmpdir(), 'backends-'));
    paths.backendsDir = userDir;
  });
  afterEach(() => {
    paths.backendsDir = origBackendsDir;
    rmSync(userDir, { recursive: true, force: true });
  });

  it('① bridge 自身 node_modules 命中：bare import（dev/worktree 模式，不碰用户目录）', async () => {
    // cross-spawn 是 bridge 自身 dependency，必在 worktree node_modules —— 走第①条。
    // 用户目录是空临时目录，证明根本没用到它（第①条就命中了）。
    const mod = await loadBackendDep<{ default?: unknown }>('cross-spawn');
    expect(mod).toBeDefined();
    // cross-spawn 是 CJS 函数；ESM interop 下函数挂在 default（或模块本身）。
    expect(typeof (mod.default ?? mod)).toBe('function');
    expect(isBackendDepInstalled('cross-spawn')).toBe(true);
  });

  it('② 用户私装目录解析：bridge 自身没有的包，从 backendsDir 加载', async () => {
    // 在临时用户目录里造一个 bridge 自身绝无的假包，证明走第②条（createRequire 锚 backendsDir）。
    const pkgName = 'fcb-fake-ondemand-pkg';
    const pkgDir = join(userDir, 'node_modules', pkgName);
    mkdirSync(pkgDir, { recursive: true });
    writeFileSync(join(pkgDir, 'package.json'), JSON.stringify({ name: pkgName, version: '1.0.0', main: 'index.mjs' }));
    writeFileSync(join(pkgDir, 'index.mjs'), 'export const marker = "from-user-dir";\n');

    expect(isBackendDepInstalled(pkgName)).toBe(true);
    const mod = await loadBackendDep<{ marker: string }>(pkgName);
    expect(mod.marker).toBe('from-user-dir');
  });

  it('③ 两处都没有：抛 BackendNotInstalledError（携带包名）', async () => {
    const ghost = 'fcb-definitely-not-installed-anywhere';
    expect(isBackendDepInstalled(ghost)).toBe(false);
    await expect(loadBackendDep(ghost)).rejects.toBeInstanceOf(BackendNotInstalledError);
    await expect(loadBackendDep(ghost)).rejects.toMatchObject({ pkg: ghost });
  });
});

describe('installer：命令构建（不真跑 npm）', () => {
  it('buildInstallCommand：npm install <pkg> --prefix backendsDir --include=optional + 关进度', () => {
    const { command, args } = buildInstallCommand('@anthropic-ai/claude-agent-sdk@0.3.175', {
      prefix: '/u/backends',
      cacheDir: '/u/npm-cache',
    });
    expect(command).toBe('npm');
    expect(args).toContain('install');
    expect(args).toContain('@anthropic-ai/claude-agent-sdk@0.3.175');
    expect(args).toContain('--include=optional');
    expect(args).toContain('--no-progress'); // 关 npm 进度条避免流里乱码
    // 用默认 --save（多后端共存靠 package.json 记账；--no-save 会 prune 掉其它后端 → 互斥）
    expect(args).not.toContain('--no-save');
    const i = args.indexOf('--prefix');
    expect(args[i + 1]).toBe('/u/backends');
    const c = args.indexOf('--cache');
    expect(args[c + 1]).toBe('/u/npm-cache');
  });

  it('buildInstallCommand 默认用 paths.backendsDir / npmCacheDir', () => {
    const { args } = buildInstallCommand('some-pkg');
    expect(args[args.indexOf('--prefix') + 1]).toBe(paths.backendsDir);
    expect(args[args.indexOf('--cache') + 1]).toBe(paths.npmCacheDir);
  });

  it('stripVersion：scoped/非 scoped 都正确去版本', () => {
    expect(stripVersion('@anthropic-ai/claude-agent-sdk@0.3.175')).toBe('@anthropic-ai/claude-agent-sdk');
    expect(stripVersion('@anthropic-ai/claude-agent-sdk')).toBe('@anthropic-ai/claude-agent-sdk');
    expect(stripVersion('nanoid@5.0.0')).toBe('nanoid');
    expect(stripVersion('nanoid')).toBe('nanoid');
  });
});

describe('claude-sdk doctor：依赖装没装的两态（SDK 是按需依赖，dev 可能删了测下载）', () => {
  it('SDK 可解析 → ok:true/installed/location 指向包名；解析不到 → ok:false/not-installed', async () => {
    // 不耦合 node_modules 状态：按 isBackendDepInstalled 实际判定再断言对应分支，
    // CI（npm ci 装 devDep）走已装态、本地删了 SDK 测下载走可下载态，两边都绿。
    const installed = isBackendDepInstalled('@anthropic-ai/claude-agent-sdk');
    const probe = await createBackend('claude-sdk').doctor();
    expect(probe.ok).toBe(installed);
    if (installed) {
      expect(probe.depState).toBe('installed');
      expect(probe.location).toContain('claude-agent-sdk');
    } else {
      expect(probe.depState).toBe('not-installed');
    }
  });
});
