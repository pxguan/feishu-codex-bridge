import { createReadonlyAdminService } from '../../admin/service';
import { createWebServer } from '../../web/server';

/** 默认端口（设计文档 §3 方案 B 的示例端口）。 */
const DEFAULT_PORT = 7866;

/**
 * `feishu-codex-bridge web [--port]` —— 本机 Web 控制台（只读预览）。
 *
 * 不依赖 daemon 在跑：直读 ~/.feishu-codex-bridge 下的 bots/projects/sessions
 * 文件渲染快照（运行状态靠单实例锁文件探测）。仅绑定 127.0.0.1 + 随机 token，
 * 启动打印一行带 token 的可点击 URL。
 *
 * TODO(第二棒): run 进程内嵌集成 —— daemon 启动时用同一 createWebServer 挂一个
 * 进程内实例（service 换成接住在跑 bridge/orchestrator 的实现：真实 WS 连接
 * 状态、写操作落盘 + 驱逐活跃会话），并让 `web` 命令检测到 daemon 在跑时直接
 * 打开 daemon 的控制台 URL 而不是再起一个只读副本。
 */
export async function runWeb(opts: { port?: number } = {}): Promise<void> {
  const port = opts.port ?? DEFAULT_PORT;
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    console.error(`✗ 无效端口：${opts.port}`);
    process.exitCode = 1;
    return;
  }

  const service = createReadonlyAdminService();
  const web = createWebServer({ service });

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
  console.log('   · 当前为只读预览（直读本机文件）：写操作（🧠 后端切换 / 🔐 权限等）随 daemon 集成开放。');
  console.log('   · Ctrl+C 退出。');

  const shutdown = (): void => {
    void web.close().finally(() => process.exit(0));
  };
  process.once('SIGINT', shutdown);
  process.once('SIGTERM', shutdown);
}
