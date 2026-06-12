/**
 * Wrap an async iterable with a per-event idle timeout and an optional external
 * stop signal. If no event arrives within `idleMs`, calls `onTimeout()` and
 * ends the stream (the caller's onTimeout should abort the underlying turn).
 * If `stop` resolves, the stream ends immediately (used by ⏹ 终止 — codex does
 * not emit a mappable terminal on turn/interrupt, so we must end the consume
 * loop locally instead of waiting on the backend). `idleMs <= 0` disables the
 * idle timer.
 *
 * `lastActivity` decouples LIVENESS from RENDERING: the event map drops raw
 * notifications it doesn't surface (e.g. command output deltas), so a long
 * shell command can stream output for minutes while yielding nothing here.
 * When the idle timer fires we check the backend's real activity clock — if it
 * moved within `idleMs`, re-arm for the remainder instead of killing the turn.
 */
export async function* withIdleTimeout<T>(
  source: AsyncIterable<T>,
  idleMs: number,
  onTimeout: () => void,
  stop?: Promise<unknown>,
  lastActivity?: () => number,
): AsyncGenerator<T> {
  if ((!idleMs || idleMs <= 0) && !stop) {
    yield* source;
    return;
  }
  const iter = source[Symbol.asyncIterator]();
  const stopRace = stop?.then(() => '__stop__' as const);
  // Re-arming must keep waiting on the SAME pending next() — an async generator
  // queues a second next() behind the first, so racing a fresh one each lap
  // would silently drop the value the abandoned call eventually resolves with.
  let pendingNext: Promise<IteratorResult<T>> | undefined;
  let timerMs = idleMs;
  while (true) {
    let timer: ReturnType<typeof setTimeout> | undefined;
    pendingNext ??= iter.next();
    const races: Promise<IteratorResult<T> | '__idle__' | '__stop__'>[] = [pendingNext];
    if (idleMs && idleMs > 0) {
      races.push(new Promise<'__idle__'>((res) => {
        timer = setTimeout(() => res('__idle__'), timerMs);
      }));
    }
    if (stopRace) races.push(stopRace);
    const raced = await Promise.race(races);
    if (timer) clearTimeout(timer);
    if (raced === '__idle__') {
      const sinceActivity = lastActivity ? Date.now() - lastActivity() : Infinity;
      if (sinceActivity < idleMs) {
        timerMs = idleMs - sinceActivity; // real activity recently — re-arm for the remainder
        continue;
      }
      onTimeout();
      return;
    }
    if (raced === '__stop__') return;
    pendingNext = undefined;
    timerMs = idleMs;
    const r = raced as IteratorResult<T>;
    if (r.done) return;
    yield r.value;
  }
}

/** Minimal FIFO semaphore for the global concurrent-run cap. */
export class Semaphore {
  private active = 0;
  private waiters: (() => void)[] = [];
  constructor(private readonly max: number) {}

  /** True if acquire() would grant a slot without queueing. */
  hasFree(): boolean {
    return this.active < this.max;
  }

  async acquire(): Promise<() => void> {
    if (this.active >= this.max) {
      await new Promise<void>((res) => this.waiters.push(res));
    }
    this.active++;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.active--;
      const next = this.waiters.shift();
      if (next) next();
    };
  }
}
