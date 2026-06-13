import { rmSync } from 'node:fs';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { vi } from 'vitest';
import { paths, useBotDir } from '../src/config/paths';
import { saveBots } from '../src/config/bots';
import { addProject } from '../src/project/registry';
import { upsertSession } from '../src/bot/session-store';
import { createReadonlyAdminService, NotWiredYetError, type AdminService } from '../src/admin/service';

// 把整个 ~/.feishu-codex-bridge 指到临时目录（含 useBotDir 的 per-bot 切换语义），
// 绝不碰真实用户数据。fixture 全部通过 bots/registry/session-store 模块的导出写
// 入 —— 服务层读到的就是真实落盘格式。
vi.mock('../src/config/paths', async () => {
  const { mkdtempSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const appDir = mkdtempSync(join(tmpdir(), 'web-admin-service-test-'));
  let currentBotDir = appDir;
  const botDir = (appId: string): string => join(appDir, 'bots', appId);
  return {
    botDir,
    useBotDir: (appId: string): void => {
      currentBotDir = botDir(appId);
    },
    paths: {
      appDir,
      cacheDir: appDir,
      botsFile: join(appDir, 'bots.json'),
      get configFile(): string {
        return join(currentBotDir, 'config.json');
      },
      get sessionsFile(): string {
        return join(currentBotDir, 'sessions.json');
      },
      get projectsFile(): string {
        return join(currentBotDir, 'projects.json');
      },
      get processesFile(): string {
        return join(currentBotDir, 'processes.json');
      },
      projectsRootDir: join(appDir, 'projects'),
    },
  };
});

const BOT_A = 'cli_botA';
const BOT_B = 'cli_botB';
let service: AdminService;

beforeAll(async () => {
  await saveBots({
    version: 1,
    current: BOT_A,
    bots: [
      { name: 'alpha', appId: BOT_A, tenant: 'feishu', botName: '阿尔法', createdAt: 1, active: true },
      { name: 'beta', appId: BOT_B, tenant: 'lark', createdAt: 2, active: false },
    ],
  });

  // bot A：一个项目 + 两个话题（另一个 chatId 的会话不算它的话题数）
  useBotDir(BOT_A);
  await addProject({
    name: 'proj-a',
    chatId: 'oc_aaa',
    cwd: '/tmp/proj-a',
    blank: false,
    createdAt: 100,
    kind: 'multi',
    mode: 'write',
    backend: 'claude-sdk',
    allowedUsers: ['ou_1'],
  });
  for (const [threadId, chatId] of [
    ['t1', 'oc_aaa'],
    ['t2', 'oc_aaa'],
    ['t3', 'oc_other'],
  ] as const) {
    await upsertSession({
      threadId,
      chatId,
      cwd: '/tmp/proj-a',
      sessionId: `sess-${threadId}`,
      backend: 'codex-appserver',
      summary: `话题 ${threadId}`,
      createdAt: 10,
      updatedAt: threadId === 't1' ? 30 : 20,
    });
  }

  // bot B：另一个目录下的项目（验证 per-bot 隔离）
  useBotDir(BOT_B);
  await addProject({
    name: 'proj-b',
    chatId: 'oc_bbb',
    cwd: '/tmp/proj-b',
    blank: true,
    createdAt: 200,
  });

  service = createReadonlyAdminService();
});

afterAll(() => {
  rmSync(paths.appDir, { recursive: true, force: true });
});

describe('createReadonlyAdminService · 只读方法', () => {
  it('listBots：全部 bot + current/active 标记，预览态下 running=false', async () => {
    const bots = await service.listBots();
    expect(bots.map((b) => b.appId).sort()).toEqual([BOT_A, BOT_B]);
    const a = bots.find((b) => b.appId === BOT_A)!;
    const b = bots.find((b) => b.appId === BOT_B)!;
    expect(a.current).toBe(true);
    expect(a.active).toBe(true);
    expect(a.botName).toBe('阿尔法');
    expect(b.current).toBe(false);
    expect(b.active).toBe(false);
    // 没有单实例锁文件 → 未在跑
    expect(a.running).toBe(false);
    expect(b.running).toBe(false);
  });

  it('listProjects：effective 字段已解析（默认值回填）+ 话题数按 chatId 聚合', async () => {
    const projects = await service.listProjects(BOT_A);
    expect(projects).toHaveLength(1);
    const p = projects[0]!;
    expect(p.name).toBe('proj-a');
    expect(p.kind).toBe('multi');
    expect(p.origin).toBe('created'); // 缺省回填
    expect(p.mode).toBe('write');
    expect(p.guestMode).toBe('write'); // guestMode 未设 → 跟管理员档
    expect(p.noMention).toBe(true); // created+multi 默认免@ 开
    expect(p.autoCompact).toBe(true);
    expect(p.network).toBe(false);
    expect(p.backend).toBe('claude-sdk');
    expect(p.allowedUsersCount).toBe(1);
    expect(p.sessionCount).toBe(2); // oc_other 的 t3 不算
  });

  it('listProjects：per-bot 隔离（B 的项目读不到 A 的数据），且 effective 默认后端回填', async () => {
    const projects = await service.listProjects(BOT_B);
    expect(projects).toHaveLength(1);
    expect(projects[0]!.name).toBe('proj-b');
    expect(projects[0]!.mode).toBe('full'); // 老数据无 mode → full
    expect(projects[0]!.backend).toBe('codex-appserver'); // 缺省 → 默认后端
    expect(projects[0]!.sessionCount).toBe(0);
  });

  it('并发跨 bot 读不串目录（withBotDir 串行锁）', async () => {
    const [a, b] = await Promise.all([service.listProjects(BOT_A), service.listProjects(BOT_B)]);
    expect(a.map((p) => p.name)).toEqual(['proj-a']);
    expect(b.map((p) => p.name)).toEqual(['proj-b']);
  });

  it('getProject：命中返回详情，未命中返回 undefined', async () => {
    expect((await service.getProject(BOT_A, 'proj-a'))?.cwd).toBe('/tmp/proj-a');
    expect(await service.getProject(BOT_A, 'nope')).toBeUndefined();
  });

  it('listSessions：按项目 chatId 过滤并按 updatedAt 新→旧排序', async () => {
    const sessions = await service.listSessions(BOT_A, 'proj-a');
    expect(sessions.map((s) => s.threadId)).toEqual(['t1', 't2']);
    expect(sessions[0]!.summary).toBe('话题 t1');
    expect(await service.listSessions(BOT_A, 'nope')).toEqual([]);
  });

  it('eventDiagnosis：配置缺失 → unchecked 降级（绝不抛错）', async () => {
    const d = await service.eventDiagnosis(BOT_A);
    expect(d.state).toBe('unchecked');
    expect(d.reason).toBeTruthy();
  });

  it('tailLogs：返回字符串（文件日志尾部）', async () => {
    expect(typeof (await service.tailLogs({ maxBytes: 4096 }))).toBe('string');
  });
});

describe('createReadonlyAdminService · 写方法（第一棒占位）', () => {
  it('四个写方法一律抛 NotWiredYetError', async () => {
    await expect(service.switchBackend(BOT_A, 'proj-a', 'claude-sdk')).rejects.toBeInstanceOf(NotWiredYetError);
    await expect(service.setPermissionMode(BOT_A, 'proj-a', { mode: 'qa' })).rejects.toBeInstanceOf(NotWiredYetError);
    await expect(service.setNoMention(BOT_A, 'proj-a', true)).rejects.toBeInstanceOf(NotWiredYetError);
    await expect(service.setAutoCompact(BOT_A, 'proj-a', false)).rejects.toBeInstanceOf(NotWiredYetError);
  });

  it('NotWiredYetError 带 code 和第二棒提示文案', async () => {
    const err = await service.switchBackend(BOT_A, 'proj-a', 'x').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(NotWiredYetError);
    expect((err as NotWiredYetError).code).toBe('NOT_WIRED_YET');
    expect((err as NotWiredYetError).message).toContain('第二棒');
  });
});
