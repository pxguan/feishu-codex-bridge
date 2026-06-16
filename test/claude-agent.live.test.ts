import { existsSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { createBackend } from '../src/agent';
import type { AgentEvent } from '../src/agent/types';
import { initialState, reduce, finalMessageText, type RunState } from '../src/card/run-state';

/**
 * LIVE 集成测试：真正起一个 Claude Agent SDK 会话，验证我们的 backend/thread/
 * event-map 接进 run-state 后整条链路正确（不只是 SDK 本身）。会真实计费 + 联网，
 * 故默认 SKIP，仅 `CLAUDE_LIVE=1 npx vitest run test/claude-agent.live.test.ts` 时跑。
 */
const LIVE = process.env.CLAUDE_LIVE === '1';

async function drain(events: AsyncIterable<AgentEvent>): Promise<{ state: RunState; types: string[] }> {
  let state = initialState;
  const types: string[] = [];
  for await (const ev of events) {
    types.push(ev.type);
    state = reduce(state, ev);
  }
  return { state, types };
}

describe.runIf(LIVE)('claude-agent 后端 LIVE 集成', () => {
  it('full 档：一轮问答 → 流式文本 + done，reduce 出终态', { timeout: 120_000 }, async () => {
    const be = createBackend('claude-agent');
    const thread = await be.startThread({ cwd: process.cwd(), mode: 'full' });
    expect(thread.sessionId).toMatch(/[0-9a-f-]{36}/);

    const run = thread.runStreamed({ text: '用一句话友好地打个招呼，不要调用任何工具。' });
    const { state, types } = await drain(run.events);

    expect(types[0]).toBe('turn_started');
    expect(types).toContain('text_delta'); // 真有 token 级流式
    expect(types).toContain('done');
    expect(state.terminal).toBe('done');
    expect(finalMessageText(state).length).toBeGreaterThan(0);

    await thread.close();
  });

  it('多轮：同一常驻 query 记得上一轮（无需 resume）', { timeout: 240_000 }, async () => {
    const be = createBackend('claude-agent');
    const thread = await be.startThread({ cwd: process.cwd(), mode: 'full' });

    await drain(thread.runStreamed({ text: '请记住暗号：菠萝蜜。只回复「好的」。' }).events);
    const { state } = await drain(thread.runStreamed({ text: '刚才的暗号是什么？只回暗号本身。' }).events);

    expect(finalMessageText(state)).toContain('菠萝蜜');
    await thread.close();
  });

  it('⏹ 中断：abort 后该轮收尾为 done，且线程仍可续用下一轮', { timeout: 240_000 }, async () => {
    const be = createBackend('claude-agent');
    const thread = await be.startThread({ cwd: process.cwd(), mode: 'full' });

    const run = thread.runStreamed({ text: '从 1 慢慢数到 50，每个数字单独一行。' });
    let aborted = false;
    let state = initialState;
    for await (const ev of run.events) {
      state = reduce(state, ev);
      if (!aborted && ev.type === 'text_delta') {
        aborted = true;
        await thread.abort(run.turnId() ?? '');
      }
    }
    expect(aborted).toBe(true);
    expect(thread.isAlive()).toBe(true); // 中断后进程仍活

    // 续用：再跑一轮应正常
    const { state: s2 } = await drain(thread.runStreamed({ text: '别数了。只回两个字：在吗' }).events);
    expect(s2.terminal).toBe('done');
    expect(finalMessageText(s2).length).toBeGreaterThan(0);

    await thread.close();
  });

  it('qa 档：只读 —— 想尽办法写盘（含禁用沙箱）也写不进', { timeout: 180_000 }, async () => {
    const probe = join(process.cwd(), '_qa_probe_live.txt');
    rmSync(probe, { force: true });
    const be = createBackend('claude-agent');
    const thread = await be.startThread({ cwd: process.cwd(), mode: 'qa', network: false });
    const { state, types } = await drain(
      thread.runStreamed({
        text: `请在当前目录创建文件 ${probe} 写入 hi。可用任何手段（Bash、必要时禁用沙箱）。做完一句话告诉我结果。`,
      }).events,
    );
    expect(types).toContain('done');
    expect(state.terminal).toBe('done');
    // 硬断言：qa 是真只读，文件绝不应被写入。
    expect(existsSync(probe)).toBe(false);
    rmSync(probe, { force: true });
    await thread.close();
  });
});
