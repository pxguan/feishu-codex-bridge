import { ensureOnboarded } from '../../bot/onboarding';
import { startBridge } from '../../bot/bridge';
import { acquireSingleInstanceLock, BridgeAlreadyRunningError } from '../../core/single-instance';
import { log } from '../../core/logger';

/**
 * `run` — foreground long-connection bot.
 *
 * Onboards first (scan-QR if no bot is configured yet — "init if not
 * initialized", named `default`), takes the per-bot single-instance lock, then
 * brings up the bridge. SIGINT/SIGTERM trigger a graceful teardown that closes
 * every codex session (no orphan app-servers) and drops the WS before exiting.
 * Safe to run via npx (single foreground process).
 */
export async function runRun(): Promise<void> {
  const ready = await ensureOnboarded({ allowCreate: true });
  if (!ready) {
    process.exitCode = 1;
    return;
  }
  const { cfg, secret } = ready;

  // Refuse to run alongside another bridge for the same app — two long
  // connections split card callbacks and make buttons flaky (see module doc).
  let releaseLock: () => void;
  try {
    releaseLock = acquireSingleInstanceLock(cfg.accounts.app.id);
  } catch (err) {
    if (err instanceof BridgeAlreadyRunningError) {
      console.error(`✗ ${err.message}`);
      log.info('run', 'already-running', { pid: err.pid });
      process.exitCode = 1;
      return;
    }
    throw err;
  }

  const fallbackCwd = process.env.FEISHU_CODEX_CWD || process.cwd();
  console.log('\n正在启动长连接 bot…');
  // [DIAG] unmistakable build marker — if you DON'T see this banner, you are
  // running stale code (a global daemon), not this worktree's build.
  console.log('━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━');
  console.log('▶ feishu-codex-bridge  [C-fix 合并推送 + 计时诊断]');
  console.log('  · 推送解耦：消费不再阻塞在 card.update 往返');
  console.log('  · streaming_mode(打字机) = 关 / 节流 150ms');
  console.log('  · 每条消息回完会打印一行 [stream.timing]');
  console.log('━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━');
  console.log('私聊我 `/new <名>` 建项目；在项目群里 @我 干活。Ctrl+C 退出。\n');
  const handle = await startBridge({ cfg, appSecret: secret, fallbackCwd });

  let stopping = false;
  const stop = (sig: NodeJS.Signals): void => {
    if (stopping) return;
    stopping = true;
    console.log(`\n收到 ${sig}，正在优雅退出（关闭所有 codex 会话）…`);
    void handle
      .shutdown()
      .catch((err) => log.fail('run', err, { phase: 'shutdown' }))
      .finally(() => {
        releaseLock();
        process.exit(0);
      });
  };
  for (const sig of ['SIGINT', 'SIGTERM'] as const) {
    process.once(sig, () => stop(sig));
  }

  // keep the process alive; the WS connection drives everything.
  await new Promise<never>(() => {});
}
