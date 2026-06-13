import type { AdminService } from '../admin/service';
import { log } from '../core/logger';
import { clearWebConsole, publishWebConsole } from './discovery';
import { createWebServer, DEFAULT_WEB_PORT } from './server';

/**
 * daemon（run / supervisor）进程内挂 Web 控制台。
 *
 *   - 先试默认端口 51847；被占（另一个 daemon / 任意进程）退临时端口——端口
 *     由发现文件透出，`web` 子命令不靠猜。
 *   - 起不来只告警，**绝不拖垮 bot 主流程**（控制台是旁路，不是必需品）。
 *   - 成功后写发现文件（0600，{port, token, pid}），并挂 process exit 钩子
 *     兜底清理（正常路径走返回的 close()）。
 *   - token 只打给调用方（前台 TTY 打印用）；这里只记 port 进日志——token
 *     绝不进文件日志（daemon 的 stdout 会被 launchd/systemd 重定向落盘）。
 */
export interface MountedWebConsole {
  url: string;
  port: number;
  close: () => Promise<void>;
}

export async function mountWebConsole(service: AdminService): Promise<MountedWebConsole | undefined> {
  const web = createWebServer({ service });
  let port: number;
  let url: string;
  try {
    ({ port, url } = await web.listen(DEFAULT_WEB_PORT));
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code !== 'EADDRINUSE') {
      log.fail('web', err, { phase: 'console-listen' });
      return undefined;
    }
    try {
      ({ port, url } = await web.listen(0)); // 默认口被占 → 临时端口
    } catch (err2) {
      log.fail('web', err2, { phase: 'console-listen-fallback' });
      return undefined;
    }
  }

  publishWebConsole({ port, token: web.token, pid: process.pid, startedAt: Date.now() });
  const exitCleanup = (): void => clearWebConsole();
  process.once('exit', exitCleanup);
  log.info('web', 'console-up', { port }); // 只记端口，token 绝不进日志

  return {
    url,
    port,
    close: async (): Promise<void> => {
      process.removeListener('exit', exitCleanup);
      clearWebConsole();
      await web.close().catch(() => undefined);
    },
  };
}
