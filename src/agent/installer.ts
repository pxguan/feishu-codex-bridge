import { mkdir, rm, writeFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { spawnProcess } from '../platform/spawn';
import { paths } from '../config/paths';
import { log } from '../core/logger';
import { isBackendDepInstalled } from './backend-loader';

/**
 * 按需后端依赖的安装执行器（npm-ondemand 包，如 @anthropic-ai/claude-agent-sdk）。
 *
 * 装到用户私装目录 {@link paths.backendsDir}（永远用户可写，零 sudo/brew——见
 * design/backend-catalog-ondemand.md §2.2 方案A）。流程：
 *   ① 确保 backendsDir 有 package.json（npm `--prefix` 目录必须有 package.json
 *      才不向上回溯查找——实验 D；首次写一个最小 private 包等价 `npm init`）。
 *   ② spawn `npm install <pkg> --prefix backendsDir --include=optional`（cross-spawn，
 *      Windows `.cmd` shim 安全；--include=optional 拉平台二进制——实验 F）。
 *      stdout/stderr 逐块流给 onProgress（Web SSE / DM 卡轮询的进度源）。
 *   ③ 装完用 {@link isBackendDepInstalled} 校验真能解析到（半装/损坏 = 失败）。
 *
 * 可取消：传 AbortSignal，abort 时 kill npm 子进程并清半装子目录回滚。
 * 限纯 JS / 自带 prebuild 二进制的包（catalog 的 npm-ondemand 标注保证）——不触发
 * 本机原生编译，所以无需 build 工具链。
 */

const NPM = 'npm';

/** 安装结果：ok + npm 退出码 + 合并输出尾部（失败时给用户看的诊断）。 */
export interface InstallResult {
  ok: boolean;
  /** npm 进程退出码（被取消时为 null） */
  code: number | null;
  /** 是否被 AbortSignal 取消 */
  aborted: boolean;
  /** 合并 stdout/stderr 的尾部（最多 ~2KB，失败诊断用） */
  tail: string;
}

/** 进度回调：npm 每输出一块就回调一次（已按行无关，原样字符串块）。 */
export type InstallProgress = (chunk: string) => void;

/** backendsDir 里要写的最小 package.json（让 npm `--prefix` 不向上回溯——实验 D）。 */
const BACKENDS_PACKAGE_JSON = JSON.stringify(
  { name: 'feishu-codex-bridge-backends', private: true, version: '0.0.0' },
  null,
  2,
);

/** 确保 backendsDir 存在且有最小 package.json（幂等；已存在不覆写，保留 npm 写的锁等）。 */
export async function ensureBackendsDir(): Promise<void> {
  await mkdir(paths.backendsDir, { recursive: true });
  const pkgFile = join(paths.backendsDir, 'package.json');
  if (!existsSync(pkgFile)) {
    await writeFile(pkgFile, `${BACKENDS_PACKAGE_JSON}\n`, 'utf8');
  }
}

/**
 * 构建 `npm install` 命令（纯函数，exported for tests——单测只验命令构建，不真跑 npm）。
 * 装进 backendsDir、拉平台二进制、用桥的 npm 缓存目录、关进度条（避免流里乱码）。
 */
export function buildInstallCommand(
  pkg: string,
  opts: { prefix?: string; cacheDir?: string } = {},
): { command: string; args: string[] } {
  const prefix = opts.prefix ?? paths.backendsDir;
  const cacheDir = opts.cacheDir ?? paths.npmCacheDir;
  return {
    command: NPM,
    args: [
      'install',
      pkg,
      '--prefix',
      prefix,
      '--include=optional',
      '--no-audit',
      '--no-fund',
      '--no-progress',
      '--cache',
      cacheDir,
    ],
  };
}

/**
 * 按需安装一个后端依赖到用户私装目录。绝不抛错——失败落 {@link InstallResult}.ok=false
 * （HTTP/卡片层据 tail 给诊断）。装完用 isBackendDepInstalled 校验；校验不过即视为失败
 * 并回滚半装子目录。可取消（signal.abort → kill npm + 回滚）。
 *
 * @param pkg     npm 包名（catalog 的 installSpec.pkg；可带 @version）
 * @param onProgress npm 输出流回调（每块）
 * @param signal  取消信号（abort → kill 子进程 + rm 半装子目录）
 */
export async function installBackendDep(
  pkg: string,
  onProgress?: InstallProgress,
  signal?: AbortSignal,
): Promise<InstallResult> {
  // 包名去掉版本后缀（@scope/name@1.2.3 → @scope/name）用于回滚定位 + 校验。
  const bareName = stripVersion(pkg);
  try {
    await ensureBackendsDir();
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    log.fail('agent', err, { phase: 'backend-install-mkdir', pkg });
    return { ok: false, code: null, aborted: false, tail: `创建后端目录失败：${msg}` };
  }

  if (signal?.aborted) {
    return { ok: false, code: null, aborted: true, tail: '安装已取消' };
  }

  const { command, args } = buildInstallCommand(pkg);
  log.info('agent', 'backend-install-start', { pkg });

  const result = await new Promise<InstallResult>((resolve) => {
    let child;
    try {
      child = spawnProcess(command, args, { stdio: ['ignore', 'pipe', 'pipe'] });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      resolve({ ok: false, code: null, aborted: false, tail: `spawn npm 失败：${msg}` });
      return;
    }

    let out = '';
    const capture = (d: Buffer): void => {
      const s = d.toString('utf8');
      out += s;
      // 只保留尾部，避免长安装把内存撑大（SSE/卡片只需要最近的进度）。
      if (out.length > 8192) out = out.slice(-8192);
      onProgress?.(s);
    };
    child.stdout?.on('data', capture);
    child.stderr?.on('data', capture);

    let aborted = false;
    const onAbort = (): void => {
      aborted = true;
      child.kill('SIGTERM');
    };
    if (signal) {
      if (signal.aborted) onAbort();
      else signal.addEventListener('abort', onAbort, { once: true });
    }

    child.on('error', (e) => {
      if (signal) signal.removeEventListener('abort', onAbort);
      resolve({ ok: false, code: null, aborted, tail: (out + e.message).slice(-2000) });
    });
    child.on('close', (code) => {
      if (signal) signal.removeEventListener('abort', onAbort);
      resolve({ ok: code === 0 && !aborted, code, aborted, tail: out.slice(-2000) });
    });
  });

  // 取消 / 退出码非零 → 回滚半装子目录（避免 resolve 假阳性放行一个加载会炸的包）。
  if (!result.ok) {
    await rollback(bareName);
    log.warn('agent', 'backend-install-failed', { pkg, code: result.code, aborted: result.aborted });
    return result;
  }

  // npm exit 0 仍要解析校验（半装、exports 缺失、平台二进制没拉到都可能 exit 0）。
  if (!isBackendDepInstalled(bareName)) {
    await rollback(bareName);
    log.warn('agent', 'backend-install-unverified', { pkg });
    return {
      ok: false,
      code: result.code,
      aborted: false,
      tail: `${result.tail}\n\n安装后校验失败：「${bareName}」装好了但解析不到（可能半装/平台二进制缺失），已回滚。`,
    };
  }

  log.info('agent', 'backend-install-done', { pkg });
  return result;
}

/** 卸载一个按需后端依赖（省空间 / 重装前清理）。绝不抛错。 */
export async function uninstallBackendDep(pkg: string): Promise<boolean> {
  await rollback(stripVersion(pkg));
  return !isBackendDepInstalled(stripVersion(pkg));
}

/** rm -rf backendsDir/node_modules/<pkg>（半装回滚 / 卸载）。绝不抛错。 */
async function rollback(bareName: string): Promise<void> {
  // scoped 包名（@scope/name）是两级目录，rm 整个包目录即可（@scope 空壳无害留着）。
  const target = join(paths.backendsDir, 'node_modules', ...bareName.split('/'));
  await rm(target, { recursive: true, force: true }).catch(() => undefined);
}

/** 去掉版本后缀：`@scope/name@1.2.3` → `@scope/name`，`name@1.2.3` → `name`。 */
export function stripVersion(pkg: string): string {
  const at = pkg.lastIndexOf('@');
  // scoped 包以 @ 开头（lastIndexOf>0 才是版本分隔符）；非 scoped 的 at>0 也是版本分隔符。
  return at > 0 ? pkg.slice(0, at) : pkg;
}
