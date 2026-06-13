import { mkdirSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { paths } from '../config/paths';

/**
 * daemon 内嵌 Web 控制台的发现文件（~/.feishu-codex-bridge/web-console.json）。
 *
 * 安全模型（与第一棒一致，不放松）：
 *   - token 只授予「本机本用户」已有的能力（控制台仍仅绑 127.0.0.1 + token），
 *     文件 0600 仅属主可读——与 secrets.enc / config.json 同一信任域；拿得到
 *     这个文件的人本来就拿得到密钥库。
 *   - daemon 退出清理（shutdown 路径 + process exit 钩子）；读取方校验 pid
 *     活性，崩溃残留（pid 已死）一律视为不存在，绝不把陈旧 token 当真。
 *   - pid 归属校验：clear 只删自己写的记录——两个 daemon 先后启动时，后者
 *     覆盖发现文件（last-writer-wins），先退出的旧 daemon 不会误删新记录。
 */
export interface WebConsoleRecord {
  port: number;
  token: string;
  pid: number;
  startedAt: number;
}

/** 发布发现记录（0600）。先 unlink 再带 mode 写——对已存在文件 writeFile 不会
 * 重设权限，必须重建才能保证 0600。 */
export function publishWebConsole(rec: WebConsoleRecord, file: string = paths.webConsoleFile): void {
  mkdirSync(dirname(file), { recursive: true });
  try {
    unlinkSync(file);
  } catch {
    /* ENOENT —— 没有旧文件 */
  }
  writeFileSync(file, `${JSON.stringify(rec, null, 2)}\n`, { mode: 0o600 });
}

/** 读发现记录；缺失/损坏/字段不对/持有 pid 已死 → undefined（残留不作数）。 */
export function readWebConsole(file: string = paths.webConsoleFile): WebConsoleRecord | undefined {
  try {
    const rec = JSON.parse(readFileSync(file, 'utf8')) as Partial<WebConsoleRecord>;
    if (
      typeof rec.port !== 'number' ||
      typeof rec.token !== 'string' ||
      rec.token === '' ||
      typeof rec.pid !== 'number'
    ) {
      return undefined;
    }
    if (!isAlive(rec.pid)) return undefined; // daemon 崩溃残留
    return rec as WebConsoleRecord;
  } catch {
    return undefined;
  }
}

/** 清理发现记录——只删**本进程**写下的那条（pid 归属校验，见模块注释）。 */
export function clearWebConsole(file: string = paths.webConsoleFile): void {
  try {
    const rec = JSON.parse(readFileSync(file, 'utf8')) as Partial<WebConsoleRecord>;
    if (rec.pid === process.pid) unlinkSync(file);
  } catch {
    /* 已被清 / 损坏 / 不可读 —— 读取方有 pid 活性兜底，无须强删 */
  }
}

function isAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    // EPERM = 进程存在但无权 signal（不是我们的 daemon 也算「活」——读取方只
    // 关心残留与否，归属由 clear 的 pid 校验把守）。
    return (err as NodeJS.ErrnoException).code === 'EPERM';
  }
}
