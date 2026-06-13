import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import {
  buildDaemonControlCommand,
  collectHostDoctor,
  toDaemonStatus,
} from '../src/admin/host';
import type { ServiceStatus } from '../src/service/common';

describe('host · toDaemonStatus（service 快照 → daemon 状态，纯函数）', () => {
  const base: ServiceStatus = {
    platformName: 'launchd (macOS)',
    installed: true,
    running: true,
    servicePath: '/x/y.plist',
    stdoutPath: '/x/out.log',
    stderrPath: '/x/err.log',
    pid: '4242',
    lastExit: '0',
    raw: '',
  };

  it('已安装+在跑：归一字段 + uptime 按 startedAt 算 + supported=true', () => {
    const d = toDaemonStatus({ status: base, version: '0.3.11', startedAt: 1000, now: 61_000 });
    expect(d.installed).toBe(true);
    expect(d.running).toBe(true);
    expect(d.pid).toBe(4242); // string → number
    expect(d.version).toBe('0.3.11');
    expect(d.uptimeMs).toBe(60_000);
    expect(d.supported).toBe(true);
    expect(d.platformName).toBe('launchd (macOS)');
  });

  it('未传 startedAt（只读预览进程）→ uptimeMs undefined', () => {
    const d = toDaemonStatus({ status: base, version: '0.3.11' });
    expect(d.uptimeMs).toBeUndefined();
  });

  it('status undefined（未支持平台）→ supported=false、installed/running 兜底 false', () => {
    const d = toDaemonStatus({ status: undefined, version: '0.3.11' });
    expect(d.supported).toBe(false);
    expect(d.installed).toBe(false);
    expect(d.running).toBe(false);
    expect(d.pid).toBeUndefined();
  });

  it('未在跑但已安装：running=false、installed=true', () => {
    const d = toDaemonStatus({ status: { ...base, running: false, pid: undefined }, version: '0.3.11' });
    expect(d.installed).toBe(true);
    expect(d.running).toBe(false);
    expect(d.pid).toBeUndefined();
  });
});

describe('host · buildDaemonControlCommand（detached helper 命令构建，只验生成不真跑）', () => {
  it('restart：node + binPath + __daemon-control restart', () => {
    const { command, args } = buildDaemonControlCommand('restart', '/pkg/bin/feishu-codex-bridge.mjs', '/usr/bin/node');
    expect(command).toBe('/usr/bin/node');
    expect(args).toEqual(['/pkg/bin/feishu-codex-bridge.mjs', '__daemon-control', 'restart']);
  });

  it('update：固定 action 形参，无任意字符串拼接进 shell（无注入面）', () => {
    const { args } = buildDaemonControlCommand('update', '/pkg/bin/x.mjs', '/usr/bin/node');
    expect(args).toEqual(['/pkg/bin/x.mjs', '__daemon-control', 'update']);
  });

  it('缺省 binPath/nodePath 时回退到当前 Node 与解析出的 bin（绝对路径）', () => {
    const { command, args } = buildDaemonControlCommand('restart');
    expect(command).toBe(process.execPath);
    expect(args[0]).toMatch(/feishu-codex-bridge\.mjs$/);
    expect(args.slice(1)).toEqual(['__daemon-control', 'restart']);
  });
});

describe('host · collectHostDoctor（宿主机域聚合，绝不抛错）', () => {
  const tmp = mkdtempSync(join(tmpdir(), 'host-doctor-test-'));
  afterAll(() => rmSync(tmp, { recursive: true, force: true }));

  it('Node/平台/路径形状齐全 + 日志体量递归累加字节', async () => {
    const logsDir = join(tmp, 'logs');
    mkdirSync(join(logsDir, 'sub'), { recursive: true });
    writeFileSync(join(logsDir, 'a.log'), 'x'.repeat(100));
    writeFileSync(join(logsDir, 'sub', 'b.log'), 'y'.repeat(50));
    const h = await collectHostDoctor(logsDir);
    expect(h.node).toBe(process.version);
    expect(typeof h.platform).toBe('string');
    expect(typeof h.arch).toBe('string');
    expect(h.appDir).toContain('.feishu-codex-bridge');
    expect(h.logsDir).toBe(logsDir);
    expect(h.logBytes).toBe(150); // 100 + 50（含子目录）
    expect(h.version).toBeTruthy();
  });

  it('日志目录不存在 → logBytes 0，不抛错', async () => {
    const h = await collectHostDoctor(join(tmp, 'nope'));
    expect(h.logBytes).toBe(0);
  });
});
