import type { ChildProcess } from 'node:child_process';
import { spawnProcess } from '../platform/spawn';
import { SERVICE_ENV_FLAG } from '../service/win-startup';
import { log } from '../core/logger';
import type { BotEntry } from '../config/bots';

/**
 * Multi-bot supervisor. `run` delegates here when more than one bot is active
 * (`bot use` picked a set): each bot runs in its OWN child process
 * (`run --bot <appId>`), so one bot crashing, leaking, or wedging its codex
 * tree can't corrupt or take down the others — the design's "multi-process,
 * one bot per process" model. The supervisor restarts a crashed child with
 * exponential backoff (the OS service manager only restarts the supervisor, not
 * individual children, so child auto-recovery has to live here), and forwards
 * SIGINT/SIGTERM so a graceful stop tears the whole tree down.
 *
 * Children inherit the supervisor's CLI entry (`process.argv[1]`) so this works
 * identically for a global install, npx, and a local `./bin` checkout.
 */

const BACKOFF_MIN_MS = 1_000;
const BACKOFF_MAX_MS = 30_000;
/** A child that stays up at least this long resets its backoff to the minimum. */
const HEALTHY_UPTIME_MS = 60_000;
/** Grace period for children to exit on a signal before we SIGKILL them. */
const SHUTDOWN_GRACE_MS = 8_000;

interface Child {
  bot: BotEntry;
  proc?: ChildProcess;
  backoffMs: number;
  restartTimer?: NodeJS.Timeout;
  startedAt: number;
}

export async function runSupervisor(bots: BotEntry[]): Promise<void> {
  const cliEntry = process.argv[1];
  if (!cliEntry) throw new Error('supervisor: 无法解析 CLI 入口（process.argv[1] 为空）');

  // Children must NOT record service.pid (that's the supervisor's job on
  // Windows) — strip the service flag from their env so recordServicePid no-ops.
  const childEnv = { ...process.env };
  delete childEnv[SERVICE_ENV_FLAG];

  let shuttingDown = false;
  const children = bots.map<Child>((bot) => ({ bot, backoffMs: BACKOFF_MIN_MS, startedAt: 0 }));

  console.log(`\n正在启动 ${bots.length} 个机器人（各自独立进程）：`);
  for (const b of bots) console.log(`  • ${b.name}  (${b.appId})  [${b.tenant}]`);
  console.log('Ctrl+C 退出（关闭全部）。\n');

  const prefixPipe = (name: string, src: NodeJS.ReadableStream | null, dst: NodeJS.WriteStream): void => {
    if (!src) return;
    let buf = '';
    src.setEncoding('utf8');
    src.on('data', (chunk: string) => {
      buf += chunk;
      let nl: number;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        dst.write(`\x1b[2m[${name}]\x1b[0m ${line}\n`);
      }
    });
    src.on('end', () => {
      if (buf) dst.write(`\x1b[2m[${name}]\x1b[0m ${buf}\n`);
    });
  };

  const spawnChild = (c: Child): void => {
    c.startedAt = Date.now();
    const proc = spawnProcess(process.execPath, [cliEntry, 'run', '--bot', c.bot.appId], {
      stdio: ['ignore', 'pipe', 'pipe'],
      env: childEnv,
    });
    c.proc = proc;
    log.info('supervisor', 'child-start', { bot: c.bot.name, appId: c.bot.appId, pid: proc.pid ?? null });
    prefixPipe(c.bot.name, proc.stdout, process.stdout);
    prefixPipe(c.bot.name, proc.stderr, process.stderr);

    proc.on('exit', (code, signal) => {
      c.proc = undefined;
      if (shuttingDown) return;
      // Reset backoff if it had been healthy a while; otherwise grow it.
      const uptime = Date.now() - c.startedAt;
      if (uptime >= HEALTHY_UPTIME_MS) c.backoffMs = BACKOFF_MIN_MS;
      const wait = c.backoffMs;
      c.backoffMs = Math.min(c.backoffMs * 2, BACKOFF_MAX_MS);
      log.warn('supervisor', 'child-exit', { bot: c.bot.name, code, signal, restartInMs: wait });
      console.error(
        `\x1b[2m[${c.bot.name}]\x1b[0m 进程退出（code=${code ?? signal ?? '?'}），${Math.round(wait / 1000)}s 后重启…`,
      );
      c.restartTimer = setTimeout(() => spawnChild(c), wait);
    });
    proc.on('error', (err) => {
      log.fail('supervisor', err, { bot: c.bot.name, phase: 'spawn' });
    });
  };

  for (const c of children) spawnChild(c);

  await new Promise<void>((resolve) => {
    const stop = (sig: NodeJS.Signals): void => {
      if (shuttingDown) return;
      shuttingDown = true;
      console.log(`\n收到 ${sig}，正在关闭全部机器人…`);
      for (const c of children) {
        if (c.restartTimer) clearTimeout(c.restartTimer);
        c.proc?.kill('SIGTERM');
      }
      // Give children a grace period to close their codex sessions, then force.
      const deadline = Date.now() + SHUTDOWN_GRACE_MS;
      const poll = setInterval(() => {
        const alive = children.filter((c) => c.proc && !c.proc.killed);
        if (alive.length === 0 || Date.now() >= deadline) {
          clearInterval(poll);
          for (const c of alive) c.proc?.kill('SIGKILL');
          resolve();
        }
      }, 200);
    };
    for (const sig of ['SIGINT', 'SIGTERM'] as const) process.once(sig, () => stop(sig));
  });

  process.exit(0);
}
