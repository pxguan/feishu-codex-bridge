import { describe, expect, it, vi } from 'vitest';

vi.mock('../src/core/logger', () => ({
  log: { info: () => undefined, warn: () => undefined, fail: () => undefined },
  withTrace: async (_ctx: unknown, fn: () => Promise<void> | void) => fn(),
}));

import { createCliBridgeService } from '../src/cli-bridge/service';
import type { AppConfig } from '../src/config/schema';

/** Mock channel that records send() calls (chatId, content, opts) and also fakes
 *  the managed-card rawClient so sendOwnerCard (new-project card) succeeds. */
function mockChannel() {
  const sends: { chatId: string; content: unknown; opts: unknown }[] = [];
  let nextCard = 0;
  let nextMessage = 0;
  const channel = {
    send: vi.fn(async (chatId: string, content: unknown, opts: unknown) => {
      sends.push({ chatId, content, opts });
      nextMessage += 1;
      return { messageId: `message_${nextMessage}` };
    }),
    rawClient: {
      cardkit: { v1: { card: { create: vi.fn(async () => { nextCard += 1; return { data: { card_id: `card_${nextCard}` } }; }), update: vi.fn(async () => ({})) } } },
      im: { v1: { message: { create: vi.fn(async () => { nextMessage += 1; return { data: { message_id: `message_${nextMessage}` } }; }) } } },
    },
  };
  return { channel: channel as never, sends };
}

function cfg(overrides: Record<string, unknown> = {}): AppConfig {
  return {
    accounts: { app: { id: 'app', secret: 'secret', tenant: 'feishu' } },
    preferences: { access: { ownerOpenId: 'ou_owner' }, cliBridge: { enabled: true, delivery: 'always', ...overrides } },
  } as unknown as AppConfig;
}

function taskCompleteMsg(cwd: string, summary = 'all done') {
  return {
    type: 'task_complete' as const,
    source: 'claude' as const,
    sessionId: 's',
    cwd,
    toolInput: {},
    bridgeOwned: false,
    rawPayloadBytes: 2,
    summary,
  };
}

describe('cli bridge completion-sync', () => {
  it('unbound cwd → auto-create project+group, open a topic there with the result (bypasses away)', async () => {
    const { channel, sends } = mockChannel();
    const created: { cwd: string; source: string }[] = [];
    const service = createCliBridgeService({
      cfg: cfg(),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: false, reason: 'local_active' }),
      findProjectByCwd: async () => undefined,
      createProjectForCwd: async (cwd, source) => { created.push({ cwd, source }); return { chatId: 'oc_autocreate', name: 'repo_claude' }; },
    });

    await service.handleMessage(taskCompleteMsg('/unbound/repo', 'the result'));
    await vi.waitFor(() => expect(created).toEqual([{ cwd: '/unbound/repo', source: 'claude' }]));
    expect(sends).toHaveLength(1);
    expect(sends[0]).toMatchObject({ chatId: 'oc_autocreate', opts: { replyInThread: true } });
    expect((sends[0]?.content as { markdown: string }).markdown).toContain('the result');
  });

  it('unbound cwd + auto-create fails → fallback owner DM notice card with the result', async () => {
    const { channel, sends } = mockChannel();
    const service = createCliBridgeService({
      cfg: cfg(),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: false, reason: 'local_active' }),
      findProjectByCwd: async () => undefined,
      createProjectForCwd: async () => undefined, // 建群失败
    });

    await service.handleMessage(taskCompleteMsg('/unbound/fail', 'fallback result'));
    await vi.waitFor(() => expect(sends).toHaveLength(0)); // 不发群
    // 回退发 owner 私聊 notice 卡（走 managed card.create，非 channel.send）
    await new Promise((r) => setTimeout(r, 10));
    expect(sends).toHaveLength(0);
  });

  it('bound multi-project → open a new topic in the group (replyInThread=true)', async () => {
    const { channel, sends } = mockChannel();
    const service = createCliBridgeService({
      cfg: cfg(),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: false, reason: 'local_active' }),
      findProjectByCwd: async () => ({ chatId: 'oc_group1', name: 'my-app', kind: 'multi' }),
    });

    await service.handleMessage(taskCompleteMsg('/bound/my-app', 'finished the task'));
    await vi.waitFor(() => expect(sends).toHaveLength(1));
    expect(sends[0]).toMatchObject({ chatId: 'oc_group1', opts: { replyInThread: true } });
    const md = (sends[0]?.content as { markdown: string }).markdown;
    expect(md).toContain('my-app');
    expect(md).toContain('finished the task');
  });

  it('bound single-project → send to group WITHOUT opening a topic (replyInThread=false)', async () => {
    const { channel, sends } = mockChannel();
    const service = createCliBridgeService({
      cfg: cfg(),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: false, reason: 'local_active' }),
      findProjectByCwd: async () => ({ chatId: 'oc_single', name: 'single-app', kind: 'single' }),
    });

    await service.handleMessage(taskCompleteMsg('/bound/single'));
    await vi.waitFor(() => expect(sends).toHaveLength(1));
    expect(sends[0]).toMatchObject({ chatId: 'oc_single', opts: { replyInThread: false } });
  });

  it('bound project without chatId (blank project) → skips both paths', async () => {
    const { channel, sends } = mockChannel();
    const service = createCliBridgeService({
      cfg: cfg(),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: false, reason: 'local_active' }),
      findProjectByCwd: async () => ({ name: 'blank', kind: 'multi' }), // no chatId
    });

    await service.handleMessage(taskCompleteMsg('/bound/blank'));
    await new Promise((r) => setTimeout(r, 20));
    expect(sends).toHaveLength(0);
  });

  it('completionSync.enabled=false → no sync card (existing 完成卡 still gated by away)', async () => {
    const { channel, sends } = mockChannel();
    const service = createCliBridgeService({
      cfg: cfg({ completionSync: { enabled: false } }),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: false, reason: 'local_active' }),
      findProjectByCwd: async () => undefined,
    });

    const res = await service.handleMessage(taskCompleteMsg('/repo'));
    expect(res.decision).toBe('fallback_local'); // away gate skips 完成卡
    await new Promise((r) => setTimeout(r, 20));
    expect(sends).toHaveLength(0);
  });

  it('stacks with existing 完成卡 when away: both fire', async () => {
    const { channel, sends } = mockChannel();
    const created: string[] = [];
    const service = createCliBridgeService({
      cfg: cfg({ taskCompletion: { enabled: true, replyEnabled: false, replyTimeoutSeconds: 1 } }),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: true, reason: 'away' }),
      localActivity: async () => false,
      findProjectByCwd: async () => undefined,
      createProjectForCwd: async (cwd) => { created.push(cwd); return { chatId: 'oc_away', name: 'repo_claude' }; },
    });

    await service.handleMessage(taskCompleteMsg('/away/repo', 'result'));
    // completion-sync auto-creates a group and opens a topic there with the result
    await vi.waitFor(() => { expect(created).toEqual(['/away/repo']); expect(sends).toHaveLength(1); });
    expect(sends[0]).toMatchObject({ chatId: 'oc_away', opts: { replyInThread: true } });
  });

  it('truncates long summaries to 2000 chars in the group topic', async () => {
    const { channel, sends } = mockChannel();
    const service = createCliBridgeService({
      cfg: cfg(),
      channel,
      socketPath: '/tmp/unused',
      presence: async () => ({ routeToFeishu: false, reason: 'local_active' }),
      findProjectByCwd: async () => ({ chatId: 'oc_g', name: 'p', kind: 'multi' }),
    });

    const long = 'x'.repeat(3000);
    await service.handleMessage(taskCompleteMsg('/p', long));
    await vi.waitFor(() => expect(sends).toHaveLength(1));
    const md = (sends[0]?.content as { markdown: string }).markdown;
    expect(md).toContain('已截断');
    // 2000 + marker, well under the original 3000
    expect(md.length).toBeLessThan(2300);
  });
});
