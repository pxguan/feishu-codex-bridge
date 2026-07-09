import { describe, expect, it } from 'vitest';
import {
  buildRelauncherPowershell,
  runWinRelaunch,
  type WinRelaunchDeps,
} from '../src/service/win-startup';

// Windows update→restart fix. restartWinStartup can't kill from its own process
// (it's inside the target's taskkill /T tree — DM path IS the daemon, Web path is
// its child), so it hands "kill old → wait for it to die → startNow" to a
// tree-free relauncher (WMI/Task Scheduler parented outside the tree). These
// lock the two load-bearing pieces that can't run on the mac dev box: the WMI
// command string and runWinRelaunch's ordering. Fully dependency-injected — no
// real process is ever killed and nothing touches the real ~/.feishu-codex-bridge.

describe('buildRelauncherPowershell (WMI tree-free spawn)', () => {
  it('creates the process via WMI Win32_Process.Create (parented by WmiPrvSE, escapes taskkill /T)', () => {
    const ps = buildRelauncherPowershell('C:\\bin\\feishu.mjs', 'C:\\node.exe');
    expect(ps).toContain('Invoke-CimMethod');
    expect(ps).toContain('Win32_Process');
    expect(ps).toContain('Create');
  });

  it('re-enters the CLI as the hidden __win-relaunch subcommand with node+bin quoted', () => {
    const ps = buildRelauncherPowershell('C:\\Program Files\\feishu\\bin.mjs', 'C:\\node.exe');
    expect(ps).toContain('__win-relaunch');
    // node + bin each wrapped in double quotes so a space in the path can't split args
    expect(ps).toContain('"C:\\node.exe" "C:\\Program Files\\feishu\\bin.mjs" __win-relaunch');
  });

  it('escapes single quotes in paths (PowerShell literal → doubled) so paths cannot break out', () => {
    const ps = buildRelauncherPowershell("C:\\it's\\bin.mjs", 'C:\\node.exe');
    // The CommandLine is a single-quoted PS literal; an embedded ' must be doubled.
    expect(ps).toContain("it''s");
    // and never left as a lone quote that would terminate the literal early
    expect(ps).not.toContain("it's\\bin");
  });

  it('falls back to a one-shot Scheduled Task if WMI fails at runtime (PS2.0 / WMI off)', () => {
    const ps = buildRelauncherPowershell('C:\\bin.mjs', 'C:\\node.exe');
    expect(ps).toContain('-ErrorAction Stop'); // make a WMI failure throw, not silently no-op
    expect(ps).toContain('catch');
    expect(ps).toContain('schtasks /create');
    expect(ps).toContain('schtasks /run');
  });
});

/**
 * A fake old-daemon lifecycle for pidAlive: starts alive, dies after N poll
 * probes (Infinity = never dies → the elevated/Access-Denied abort path).
 */
function makeWorld(opts: { aliveInitially?: boolean; diesAfterProbes?: number }) {
  const aliveInitially = opts.aliveInitially ?? true;
  const diesAfter = opts.diesAfterProbes ?? 0;
  let probes = 0;
  const events: string[] = [];
  let started: string | null | undefined = undefined; // undefined = never called
  let cleared = false;

  const deps: WinRelaunchDeps = {
    claimRequest: () => ({ oldPid: 4242, envPath: 'C:\\node;C:\\npm', nonce: 'n1' }),
    clearRequest: () => {
      cleared = true;
      events.push('clear');
    },
    pidAlive: () => {
      if (!aliveInitially) return false;
      return probes++ < diesAfter;
    },
    taskkill: () => {
      events.push('taskkill');
      return { status: 0, stderr: '' };
    },
    start: (envPath: string) => {
      started = envPath;
      events.push('start');
    },
    // deterministic + instant: no real timers, no real clock
    sleep: async () => undefined,
    now: () => 0, // deadline = graceMs; loop exits via alive() flipping false, not the clock
    graceMs: 10_000,
    pollMs: 1,
    ensureLogs: async () => undefined,
    logLine: (line: string) => events.push(`log:${line}`),
  };

  return {
    deps,
    events,
    getStarted: () => started,
    wasCleared: () => cleared,
  };
}

describe('runWinRelaunch (ordering: kill → wait for death → startNow)', () => {
  it('kills the old daemon, waits until it is dead, then starts new with the carried PATH, then clears', async () => {
    const w = makeWorld({ aliveInitially: true, diesAfterProbes: 3 });
    await runWinRelaunch(w.deps);

    // taskkill must precede start, and start must precede clear.
    const kIdx = w.events.indexOf('taskkill');
    const sIdx = w.events.indexOf('start');
    const cIdx = w.events.indexOf('clear');
    expect(kIdx).toBeGreaterThanOrEqual(0);
    expect(sIdx).toBeGreaterThan(kIdx);
    expect(cIdx).toBeGreaterThan(sIdx);
    // new daemon inherits the daemon-side PATH (WmiPrvSE env may lack the user PATH).
    expect(w.getStarted()).toBe('C:\\node;C:\\npm');
  });

  it('skips taskkill when the old daemon is already dead, but still starts the replacement', async () => {
    const w = makeWorld({ aliveInitially: false });
    await runWinRelaunch(w.deps);
    expect(w.events).not.toContain('taskkill');
    expect(w.events).toContain('start');
    expect(w.wasCleared()).toBe(true);
  });

  it('ABORTS (does NOT start a second instance) if the old daemon refuses to die', async () => {
    const w = makeWorld({ aliveInitially: true, diesAfterProbes: Infinity });
    // now() advances so the grace deadline is actually reached and the loop exits.
    let t = 0;
    w.deps.now = () => (t += 5_000);
    await runWinRelaunch(w.deps);

    expect(w.events).toContain('taskkill');
    expect(w.getStarted()).toBeUndefined(); // never started → no double instance
    expect(w.wasCleared()).toBe(true); // claim consumed → cleared in finally (no orphan claim file)
    expect(w.events.some((e) => e.startsWith('log:ABORT'))).toBe(true);
  });

  it('no-ops when there is no relaunch request to claim', async () => {
    const events: string[] = [];
    await runWinRelaunch({
      claimRequest: () => null,
      ensureLogs: async () => undefined,
      logLine: (l) => events.push(l),
      start: () => events.push('start'),
      taskkill: () => {
        events.push('taskkill');
        return { status: 0, stderr: '' };
      },
    });
    expect(events).not.toContain('start');
    expect(events).not.toContain('taskkill');
    expect(events.some((e) => e.includes('no relaunch request'))).toBe(true);
  });

  it('a concurrent second relauncher loses the atomic claim and no-ops (no double daemon)', async () => {
    // Shared request: the first claim wins (returns it), any later claim gets null
    // — models renameSync's atomic single-winner across two racing relaunchers.
    let claimed = false;
    const claimRequest = () => {
      if (claimed) return null;
      claimed = true;
      return { oldPid: 4242, envPath: 'C:\\node', nonce: 'n1' };
    };
    const mkEvents = () => {
      const events: string[] = [];
      return {
        events,
        deps: {
          claimRequest,
          clearRequest: () => events.push('clear'),
          pidAlive: () => false, // old already gone → straight to start
          taskkill: () => ({ status: 0, stderr: '' }),
          start: () => events.push('start'),
          sleep: async () => undefined,
          ensureLogs: async () => undefined,
          logLine: (l: string) => events.push(`log:${l}`),
        } as WinRelaunchDeps,
      };
    };
    const winner = mkEvents();
    const loser = mkEvents();
    await runWinRelaunch(winner.deps);
    await runWinRelaunch(loser.deps);

    expect(winner.events).toContain('start'); // winner starts exactly one daemon
    expect(loser.events).not.toContain('start'); // loser must NOT start a second
    expect(loser.events.some((e) => e.includes('no relaunch request'))).toBe(true);
  });
});
