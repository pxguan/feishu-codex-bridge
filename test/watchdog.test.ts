import { afterEach, describe, expect, it, vi } from 'vitest';
import { Semaphore, withIdleTimeout } from '../src/bot/watchdog';

async function* delayedValues<T>(values: Array<{ delayMs: number; value: T }>): AsyncGenerator<T> {
  for (const { delayMs, value } of values) {
    await new Promise<void>((resolve) => setTimeout(resolve, delayMs));
    yield value;
  }
}

async function* neverEnding<T>(first: T): AsyncGenerator<T> {
  yield first;
  await new Promise<never>(() => {});
}

describe('Semaphore', () => {
  it('blocks acquire calls beyond max until release is called', async () => {
    const sem = new Semaphore(2);
    const release1 = await sem.acquire();
    const release2 = await sem.acquire();

    let acquired = false;
    const third = sem.acquire().then((release) => {
      acquired = true;
      return release;
    });
    await Promise.resolve();
    expect(acquired).toBe(false);

    release1();
    const release3 = await third;
    expect(acquired).toBe(true);

    release2();
    release3();
  });

  it('releases waiters in FIFO order', async () => {
    const sem = new Semaphore(1);
    const release1 = await sem.acquire();
    const order: string[] = [];

    const second = sem.acquire().then((release) => {
      order.push('second');
      return release;
    });
    const third = sem.acquire().then((release) => {
      order.push('third');
      return release;
    });
    await Promise.resolve();
    expect(order).toEqual([]);

    release1();
    const release2 = await second;
    expect(order).toEqual(['second']);

    release2();
    const release3 = await third;
    expect(order).toEqual(['second', 'third']);
    release3();
  });
});

describe('withIdleTimeout', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('passes through every source value when each arrives before the idle timeout', async () => {
    vi.useFakeTimers();
    const onTimeout = vi.fn();
    const out: string[] = [];
    const done = (async () => {
      for await (const value of withIdleTimeout(
        delayedValues([
          { delayMs: 10, value: 'a' },
          { delayMs: 10, value: 'b' },
        ]),
        50,
        onTimeout,
      )) {
        out.push(value);
      }
    })();

    await vi.advanceTimersByTimeAsync(10);
    expect(out).toEqual(['a']);
    await vi.advanceTimersByTimeAsync(10);
    await done;

    expect(out).toEqual(['a', 'b']);
    expect(onTimeout).not.toHaveBeenCalled();
  });

  it('calls onTimeout and ends the generator when the source goes idle', async () => {
    vi.useFakeTimers();
    const onTimeout = vi.fn();
    const iter = withIdleTimeout(neverEnding('first'), 50, onTimeout)[Symbol.asyncIterator]();

    await expect(iter.next()).resolves.toEqual({ done: false, value: 'first' });
    const second = iter.next();
    await vi.advanceTimersByTimeAsync(50);

    await expect(second).resolves.toEqual({ done: true, value: undefined });
    expect(onTimeout).toHaveBeenCalledTimes(1);
  });

  it('directly passes through the source when idleMs is disabled', async () => {
    const onTimeout = vi.fn();
    const values: string[] = [];

    for await (const value of withIdleTimeout(delayedValues([{ delayMs: 1, value: 'a' }]), 0, onTimeout)) {
      values.push(value);
    }

    expect(values).toEqual(['a']);
    expect(onTimeout).not.toHaveBeenCalled();
  });

  it('ends the generator when the stop signal resolves (⏹), without firing onTimeout', async () => {
    const onTimeout = vi.fn();
    let resolveStop!: () => void;
    const stop = new Promise<void>((res) => {
      resolveStop = res;
    });
    const out: string[] = [];
    const iter = withIdleTimeout(neverEnding('first'), 0, onTimeout, stop)[Symbol.asyncIterator]();

    await expect(iter.next()).resolves.toEqual({ done: false, value: 'first' });
    const second = iter.next();
    resolveStop();

    await expect(second).resolves.toEqual({ done: true, value: undefined });
    expect(onTimeout).not.toHaveBeenCalled();
  });

  // QW-5: 活性与渲染解耦 — event-map 丢弃的原始通知（如命令输出 delta）也算活着。
  it('does not time out while raw activity continues, even with mapped events silent for 150s', async () => {
    vi.useFakeTimers();
    const onTimeout = vi.fn();
    let lastRaw = Date.now();
    let rawAlive = true; // 模拟原始通知持续到达（长命令输出），但没有可映射事件
    const heartbeat = setInterval(() => {
      if (rawAlive) lastRaw = Date.now();
    }, 1_000);
    const iter = withIdleTimeout(neverEnding('first'), 120_000, onTimeout, undefined, () => lastRaw)[
      Symbol.asyncIterator
    ]();

    await expect(iter.next()).resolves.toEqual({ done: false, value: 'first' });
    const second = iter.next();
    // 映射事件停了 150s（> 120s idle），但原始活动一直在 → 绝不超时
    await vi.advanceTimersByTimeAsync(150_000);
    expect(onTimeout).not.toHaveBeenCalled();

    // 原始活动也停了 → 距最后一次真实活动满 120s 才超时
    rawAlive = false;
    await vi.advanceTimersByTimeAsync(120_000);
    await expect(second).resolves.toEqual({ done: true, value: undefined });
    expect(onTimeout).toHaveBeenCalledTimes(1);
    clearInterval(heartbeat);
  });

  it('re-arming keeps waiting on the same pending next() — a late value is not dropped', async () => {
    vi.useFakeTimers();
    const onTimeout = vi.fn();
    let lastRaw = Date.now();
    // 值在 80ms 后才到；idle 50ms 会先触发一次，但原始活动 30ms 时刷新过 → 重置后值必须照常产出
    async function* lateValue(): AsyncGenerator<string> {
      await new Promise<void>((res) => setTimeout(res, 80));
      yield 'late';
    }
    setTimeout(() => {
      lastRaw = Date.now();
    }, 30);
    const out: string[] = [];
    const done = (async () => {
      for await (const v of withIdleTimeout(lateValue(), 50, onTimeout, undefined, () => lastRaw)) out.push(v);
    })();

    await vi.advanceTimersByTimeAsync(80);
    await done;
    expect(out).toEqual(['late']);
    expect(onTimeout).not.toHaveBeenCalled();
  });

  it('still passes through values when a stop signal is provided but unresolved', async () => {
    const onTimeout = vi.fn();
    const stop = new Promise<void>(() => {}); // never resolves
    const out: string[] = [];

    for await (const value of withIdleTimeout(
      delayedValues([
        { delayMs: 1, value: 'a' },
        { delayMs: 1, value: 'b' },
      ]),
      0,
      onTimeout,
      stop,
    )) {
      out.push(value);
    }

    expect(out).toEqual(['a', 'b']);
    expect(onTimeout).not.toHaveBeenCalled();
  });
});

describe('Semaphore.hasFree', () => {
  it('reports free slots until max is reached, then again after release', async () => {
    const sem = new Semaphore(1);
    expect(sem.hasFree()).toBe(true);
    const release = await sem.acquire();
    expect(sem.hasFree()).toBe(false);
    release();
    expect(sem.hasFree()).toBe(true);
  });
});
