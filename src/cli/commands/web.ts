import { createReadonlyAdminService } from '../../admin/service';
import { readWebConsole } from '../../web/discovery';
import { createWebServer, DEFAULT_WEB_PORT } from '../../web/server';
import { openUrl } from '../../utils/open-url';
import { spawnDaemonControl } from './daemon-control';

/**
 * `feishu-codex-bridge web [--port]` —— 本机 Web 控制台。
 *
 * daemon（run/start）在跑时，控制台已内嵌在 daemon 进程里（写操作可用、连接
 * 状态实时）：检测发现文件（~/.feishu-codex-bridge/web-console.json，daemon
 * 写入、退出清理、pid 活性校验）命中就直接打开 daemon 的控制台 URL——绝不再
 * 起一个只读副本跟 daemon 抢心智。
 *
 * daemon 没跑时退化为只读预览：直读 ~/.feishu-codex-bridge 下的
 * bots/projects/sessions 文件渲染快照（运行状态靠单实例锁文件探测），写操作
 * 一律 501 引导先起 daemon。仅绑定 127.0.0.1 + 随机 token。
 */
export async function runWeb(opts: { port?: number } = {}): Promise<void> {
  // ── daemon 控制台已在 → 直接打开，不再起副本 ───────────────────────────
  const live = readWebConsole();
  if (live) {
    const url = `http://127.0.0.1:${live.port}/?token=${live.token}`;
    console.log(`🌐 检测到 daemon 的 Web 控制台已在运行（PID ${live.pid}，写操作可用），直接打开：`);
    console.log(`\n   ${url}\n`);
    if (openUrl(url)) console.log('   已尝试在浏览器打开；没弹出就手动复制上面链接。');
    console.log('   · 仅本机可访问（127.0.0.1）；URL 含 token，请勿外传/截图。');
    return;
  }

  const port = opts.port ?? DEFAULT_WEB_PORT;
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    console.error(`✗ 无效端口：${opts.port}`);
    process.exitCode = 1;
    return;
  }

  // 只读预览唯一放行的宿主级动作：「启动 daemon」。detached helper 装好后台服务并
  // 拉起（与本预览进程脱钩）；本预览仍持有 51847 → daemon 退化到临时端口。但用户不必
  // 手动重开 `web`：注入 liveConsole（readWebConsole）后，前端检测到 daemon 已起就自动
  // 把页面带去那条可写控制台（见 server.ts /api/console/live），端口在哪都不用用户操心。
  const service = createReadonlyAdminService({ startDaemon: () => spawnDaemonControl('start') });
  const web = createWebServer({ service, liveConsole: () => readWebConsole() });

  let url: string;
  try {
    ({ url } = await web.listen(port));
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code === 'EADDRINUSE') {
      console.error(`✗ 端口 ${port} 已被占用，换一个试试：feishu-codex-bridge web --port ${port + 1}`);
    } else {
      console.error(`✗ Web 控制台启动失败：${err instanceof Error ? err.message : String(err)}`);
    }
    process.exitCode = 1;
    return;
  }

  console.log('🌐 Web 控制台（只读预览）已启动，浏览器打开：');
  console.log(`\n   ${url}\n`);
  console.log('   · 仅本机可访问（127.0.0.1）；URL 含一次性 token，请勿外传/截图。');
  console.log('   · daemon 未在跑：当前为只读预览（直读本机文件）。先 `run`/`start` 再开 `web` 可用写操作。');
  console.log('   · Ctrl+C 退出。');

  const shutdown = (): void => {
    void web.close().finally(() => process.exit(0));
  };
  process.once('SIGINT', shutdown);
  process.once('SIGTERM', shutdown);
}
