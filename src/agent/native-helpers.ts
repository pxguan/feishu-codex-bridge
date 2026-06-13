import { chmod, readdir } from 'node:fs/promises';
import type { Dirent } from 'node:fs';
import { join } from 'node:path';
import { paths } from '../config/paths';
import { log } from '../core/logger';

/**
 * 平台 prebuild 原生 helper 的可执行位修复。
 *
 * 【为什么需要】node-pty 1.1.0 的 macOS `spawn-helper`（prebuildify 产物，node-pty
 * 在 macOS 上经它 posix_spawnp 起 PTY 子进程）经 `npm install` 装进用户私装目录后
 * **偶发丢失可执行位变成 0644**（node-pty 历史上的打包/解包回归）。结果：node-pty
 * 一 spawn 就 `posix_spawnp failed.`，ACP 后端每个 session 必挂，飞书端只看到一句
 * 被 JSON-RPC 吞掉的「ACP 后端启动失败：Internal error」。
 *
 * 【修法】对用户私装目录下所有 `node-pty/prebuilds/<平台>/spawn-helper` 无条件补
 * 0755。幂等、绝不抛错（无该 helper 的平台 / 没装 node-pty / 不可写都静默跳过）。
 * 安装后（installer）跑一次治本，ACP 启动前（backend）再自愈一次——后者让**已经装坏
 * 的存量安装**升级桥代码后无需重装即可恢复。
 */
export async function fixNativeHelperPerms(rootDir: string = paths.backendsDir): Promise<void> {
  // npm 默认 hoist → node-pty 在顶层 node_modules；个别未 hoist 的情形嵌在 claude-pty-acp 下。
  const nodePtyDirs = [
    join(rootDir, 'node_modules', 'node-pty'),
    join(rootDir, 'node_modules', 'claude-pty-acp', 'node_modules', 'node-pty'),
  ];
  let fixed = 0;
  for (const npDir of nodePtyDirs) {
    const prebuilds = join(npDir, 'prebuilds');
    let plats: Dirent[];
    try {
      plats = await readdir(prebuilds, { withFileTypes: true });
    } catch {
      continue; // 该位置没装 node-pty
    }
    for (const p of plats) {
      if (!p.isDirectory()) continue;
      const helper = join(prebuilds, p.name, 'spawn-helper');
      try {
        await chmod(helper, 0o755);
        fixed++;
      } catch {
        /* 该平台无 spawn-helper（仅 darwin 有）/ 不可写 → 跳过 */
      }
    }
  }
  if (fixed > 0) log.info('agent', 'native-helper-perms-fixed', { count: fixed });
}
