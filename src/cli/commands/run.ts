import { ensureOnboarded, announceEventsWhenLive } from '../../bot/onboarding';
import { startBridge } from '../../bot/bridge';
import { runSupervisor } from '../../bot/supervisor';
import { acquireSingleInstanceLock, BridgeAlreadyRunningError } from '../../core/single-instance';
import { recordServicePid } from '../../service/win-startup';
import { activeBots, loadBots } from '../../config/bots';
import { log } from '../../core/logger';

/**
 * `run` — foreground long-connection bot(s).
 *
 * Dispatch:
 *   - `run --bot <name>` (explicit selector) → run that ONE bot inline.
 *   - `run` with a single active bot (or a legacy single-bot install) → run it
 *     inline, exactly as before. This keeps the proven npx single-process path.
 *   - `run` with multiple active bots (`bot use` picked a set) → hand off to the
 *     multi-process supervisor, one child process per bot.
 *
 * Inline runs onboard first (scan-QR if no bot is configured yet — "init if not
 * initialized"), take the per-bot single-instance lock, then bring up the
 * bridge. SIGINT/SIGTERM trigger a graceful teardown that closes every codex
 * session (no orphan app-servers) and drops the WS before exiting.
 */
export async function runRun(botName?: string): Promise<void> {
  // Explicit selector always runs exactly that one bot inline (this is also how
  // the supervisor launches each child: `run --bot <appId>`).
  if (botName) {
    await runSingle(botName);
    return;
  }

  // No selector: bring up the active set. 0/1 → inline (preserve the simple
  // single-process path, incl. first-run onboarding); ≥2 → supervisor.
  const active = activeBots(await loadBots());
  if (active.length > 1) {
    await runSupervisor(active);
    return;
  }
  await runSingle(active[0]?.name);
}

/** Run a single bot inline in this process. `botName` undefined → the implicit
 *  current/default bot (with first-run onboarding allowed). */
async function runSingle(botName?: string): Promise<void> {
  const ready = await ensureOnboarded({ allowCreate: !botName, bot: botName });
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

  // If launched as the Windows background service, publish our PID so
  // `status`/`stop` can find us (no-op for a foreground run / other platforms /
  // a supervised child, whose service env flag the supervisor strips).
  recordServicePid();

  const fallbackCwd = process.env.FEISHU_CODEX_CWD || process.cwd();
  console.log('\n正在启动长连接 bot…');
  console.log('私聊我 `/new <名>` 建项目；在项目群里 @我 干活。Ctrl+C 退出。\n');
  const handle = await startBridge({ cfg, appSecret: secret, fallbackCwd });
  // 长连接已在线（事件页保存的硬前置）→ 事件未订阅时自动开配置深链 + 轮询版本 API，
  // 配置生效后播报「事件已生效」。fire-and-forget，自身绝不 throw。
  void announceEventsWhenLive(ready);

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
