import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import type { AgentEvent, AgentThread } from '../src/agent/types';
import { AcpBackend, type AcpServerCommand } from '../src/agent/acp/backend';
import { BRIDGE_DEVELOPER_INSTRUCTIONS } from '../src/agent/bridge-instructions';
import { initialState, reduce } from '../src/card/run-state';

/**
 * AcpBackend 契约测试：spawn 真子进程跑 mock ACP server（test/fixtures/
 * acp-mock-server.mjs，零依赖 NDJSON JSON-RPC），全链路覆盖「握手 → session →
 * prompt 流式 → 终态」与权限自动批准 / cancel / loadSession 降级 —— 不花 token、
 * 不依赖 claude-code-acp 装没装。
 */

const FIXTURE = join(dirname(fileURLToPath(import.meta.url)), 'fixtures', 'acp-mock-server.mjs');

function mockServer(...flags: string[]): AcpServerCommand {
  return { command: process.execPath, args: [FIXTURE, ...flags] };
}

async function collect(thread: AgentThread, text: string): Promise<AgentEvent[]> {
  const events: AgentEvent[] = [];
  for await (const ev of thread.runStreamed({ text }).events) events.push(ev);
  return events;
}

async function withThread(
  server: AcpServerCommand,
  fn: (thread: AgentThread) => Promise<void>,
): Promise<void> {
  const be = new AcpBackend(server);
  const thread = await be.startThread({ cwd: '/tmp' });
  try {
    await fn(thread);
  } finally {
    await thread.close();
  }
}

describe('acp backend：mock server 全链路契约', () => {
  it('startThread 握手并拿到 server 分配的 sessionId；golden turn 流式到 done，run-state 不炸', async () => {
    await withThread(mockServer(), async (thread) => {
      expect(thread.sessionId).toBe('mock-sess-1');
      expect(thread.isAlive()).toBe(true);

      const events = await collect(thread, 'ECHO 你好'); // 首轮：ECHO 模式（见下个用例的断言点）
      expect(events[0]).toMatchObject({ type: 'turn_started' });
      expect(events.at(-1)).toMatchObject({ type: 'done' });

      // 第二轮走 golden 序列：思考 → 工具 → 文本 → 用量 → done
      const golden = await collect(thread, '看看目录');
      const types = golden.map((e) => e.type);
      expect(types).toContain('thinking_delta');
      expect(types).toContain('tool_use');
      expect(types).toContain('tool_result');
      expect(types).toContain('context_usage');
      expect(types.at(-1)).toBe('done');

      const state = golden.reduce(reduce, initialState);
      expect(state.terminal).toBe('done');
      const texts = state.blocks.filter((b) => b.kind === 'text');
      expect((texts.at(-1) as Extract<(typeof state.blocks)[number], { kind: 'text' }>).content).toBe('你好，世界');
    });
  });

  it('桥接输出约定只随首条消息注入一次（ACP 无 system 通道）', async () => {
    await withThread(mockServer(), async (thread) => {
      // mock 的 ECHO 模式原样回显收到的 prompt 文本
      const first = await collect(thread, 'ECHO-1');
      const firstText = first
        .filter((e) => e.type === 'text_delta')
        .map((e) => e.delta)
        .join('');
      expect(firstText).toContain(BRIDGE_DEVELOPER_INSTRUCTIONS.slice(0, 20));
      expect(firstText).toContain('ECHO-1');

      const second = await collect(thread, 'ECHO-2');
      const secondText = second
        .filter((e) => e.type === 'text_delta')
        .map((e) => e.delta)
        .join('');
      expect(secondText).toBe('ECHO-2'); // 第二轮不再注入
    });
  });

  it('full 档自动批准 session/request_permission（mock 按决定回显 approved:yes）', async () => {
    await withThread(mockServer('--permission'), async (thread) => {
      const events = await collect(thread, '试试权限');
      const text = events
        .filter((e) => e.type === 'text_delta')
        .map((e) => e.delta)
        .join('');
      expect(text).toBe('approved:yes');
      expect(events.at(-1)).toMatchObject({ type: 'done' });
    });
  });

  it('abort → session/cancel → stopReason cancelled → done（确认点收尾，turn 不悬挂）', async () => {
    await withThread(mockServer(), async (thread) => {
      const run = thread.runStreamed({ text: 'CANCELME' });
      const events: AgentEvent[] = [];
      for await (const ev of run.events) {
        events.push(ev);
        // mock 发出首个 chunk 后扣住响应 —— 此刻发 ⏹
        if (ev.type === 'text_delta') await thread.abort(run.turnId()!);
      }
      expect(events.at(-1)).toMatchObject({ type: 'done' });
    });
  });

  it('resumeThread：loadSession 回放被丢弃（无 active turn），会话续用既有 id', async () => {
    const be = new AcpBackend(mockServer());
    const thread = await be.resumeThread({ cwd: '/tmp', sessionId: 'prev-sess-7' });
    try {
      expect(thread.sessionId).toBe('prev-sess-7');
      const events = await collect(thread, 'ECHO-resume');
      // 回放的 user/agent_message_chunk 没漏进本轮事件流
      const texts = events.filter((e) => e.type === 'text_delta').map((e) => e.delta);
      expect(texts.join('')).not.toContain('历史');
      expect(events.at(-1)).toMatchObject({ type: 'done' });
      // resume 的会话不再注入桥接约定（新建时已发过）
      expect(texts.join('')).not.toContain('飞书桥系统约定');
    } finally {
      await thread.close();
    }
  });

  it('server 未宣告 loadSession → resumeThread 清晰失败（resolveThread 走既有降级）', async () => {
    const be = new AcpBackend(mockServer('--no-loadsession'));
    await expect(be.resumeThread({ cwd: '/tmp', sessionId: 'prev-sess-8' })).rejects.toThrow(/不支持会话恢复/);
  });

  it('doctor：握手轻探活拿到 agentInfo 版本', async () => {
    const be = new AcpBackend(mockServer());
    const probe = await be.doctor();
    expect(probe.ok).toBe(true);
    expect(probe.version).toBe('9.9.9');
    expect(probe.ok).toBe(await be.isAvailable());
  });
});

describe('acp backend：能力守卫（无半实现）', () => {
  // serverCommand 显式 null = 模拟「未检测到 claude-code-acp」（不碰 PATH/配置）
  const be = new AcpBackend(null);

  it('capabilities 全 false，supportedModes 仅 full', () => {
    expect(be.id).toBe('claude-acp');
    expect(be.capabilities).toEqual({
      goal: false,
      steer: false,
      compact: false,
      resume: false,
      approvals: false,
    });
    expect(be.supportedModes).toEqual(['full']);
  });

  it('qa/write 权限档 fail-closed：命令解析/spawn 之前即拒绝，绝不降级', async () => {
    await expect(be.startThread({ cwd: '/tmp', mode: 'qa' })).rejects.toThrow(/仅支持「完全访问」/);
    await expect(be.startThread({ cwd: '/tmp', mode: 'write' })).rejects.toThrow(/绝不静默降级/);
    await expect(be.resumeThread({ cwd: '/tmp', sessionId: 's', mode: 'qa' })).rejects.toThrow(
      /仅支持「完全访问」/,
    );
  });

  it('未检测到 server 命令：doctor 给装法提示，startThread 清晰报错', async () => {
    const probe = await be.doctor();
    expect(probe.ok).toBe(false);
    expect(probe.hint).toContain('npm i -g claude-code-acp');
    expect(probe.hint).toContain('acpCommand');
    await expect(be.startThread({ cwd: '/tmp' })).rejects.toThrow(/未检测到 claude-code-acp/);
  });

  it('listThreads（/resume 选择卡）抛明确「暂不支持」；readHistory 按契约返回空', async () => {
    await expect(be.listThreads('/tmp')).rejects.toThrow(/暂不支持.*resume 历史会话/);
    await expect(be.readHistory('/tmp', 'sess-x')).resolves.toEqual({ turns: [], totalTurns: 0 });
  });

  it('listModels 返回静态项（pickDefault 可用；ACP 无模型切换）', async () => {
    const models = await be.listModels();
    expect(models.some((m) => m.isDefault && !m.hidden)).toBe(true);
  });
});
