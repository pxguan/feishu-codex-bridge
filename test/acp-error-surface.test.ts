import { describe, expect, it } from 'vitest';
import { errMsg, startupHint } from '../src/agent/acp/backend';

// ACP「报错只剩 Internal error、日志/飞书都看不出真因」的修复回归：
//  - errMsg 必须把 JSON-RPC 的 code + data.details 带出来（真因如 posix_spawnp failed
//    就藏在 data.details，旧版只取 message → 全塌成 "Internal error"）。
//  - startupHint 对已知底层故障给可操作中文。

describe('errMsg · JSON-RPC 结构化字段不再被吞', () => {
  it('JSON-RPC -32603 + data.details → 带出 code 与真因', () => {
    const rpc = { code: -32603, message: 'Internal error', data: { details: 'posix_spawnp failed.' } };
    const s = errMsg(rpc);
    expect(s).toContain('Internal error');
    expect(s).toContain('code -32603');
    expect(s).toContain('posix_spawnp failed.'); // 真因必须出现
  });

  it('普通 Error → 原样 message（无多余括号）', () => {
    expect(errMsg(new Error('ACP session/new 超时（90s）'))).toBe('ACP session/new 超时（90s）');
  });

  it('data 是字符串细节 → 也带出', () => {
    expect(errMsg({ code: 500, message: 'boom', data: 'spawn ENOENT claude' })).toContain('spawn ENOENT claude');
  });

  it('message 已含 details 时不重复拼接', () => {
    const s = errMsg({ message: 'posix_spawnp failed.', code: -32603, data: { details: 'posix_spawnp failed.' } });
    // details 与 message 相同 → 只保留 code，不把 details 再拼一遍
    expect(s).toBe('posix_spawnp failed.（code -32603）');
  });

  it('非对象（裸字符串/数字）→ String 化', () => {
    expect(errMsg('plain')).toBe('plain');
    expect(errMsg(42)).toBe('42');
  });
});

describe('startupHint · 已知底层故障给可操作中文', () => {
  it('posix_spawnp → 指向 spawn-helper / claude', () => {
    const h = startupHint('ACP session/new 失败（posix_spawnp failed.）');
    expect(h).toContain('spawn-helper');
    expect(h).toContain('claude-acp');
  });

  it('ENOENT → 指向 PATH / CC_CLAUDE_BIN', () => {
    const h = startupHint('spawn ENOENT');
    expect(h).toContain('CC_CLAUDE_BIN');
    expect(h).toContain('PATH');
  });

  it('未知错误 → 空（不画蛇添足）', () => {
    expect(startupHint('某个无关错误')).toBe('');
  });
});
