import { describe, expect, it } from 'vitest';
import { DEFAULT_BACKEND_ID, backendIds, createBackend } from '../src/agent';
import { CodexAppServerBackend } from '../src/agent/codex-appserver/backend';
import { ClaudeSdkBackend, sanitizeClaudeModel } from '../src/agent/claude-sdk/backend';
import { AcpBackend } from '../src/agent/acp/backend';

describe('agent backend registry', () => {
  it('defaults to the codex app-server backend (zero-arg call = legacy path)', () => {
    expect(DEFAULT_BACKEND_ID).toBe('codex-appserver');
    const be = createBackend();
    expect(be).toBeInstanceOf(CodexAppServerBackend);
    expect(be.id).toBe('codex-appserver');
  });

  it('resolves an explicit codex-appserver id to the same implementation', () => {
    expect(createBackend('codex-appserver')).toBeInstanceOf(CodexAppServerBackend);
  });

  it('codex backend declares no capabilities object (undefined ⇒ full feature set)', () => {
    expect(createBackend().capabilities).toBeUndefined();
  });

  it('throws a clear error for an unknown backend id, listing what is registered', () => {
    expect(() => createBackend('no-such-backend')).toThrow(/未知 agent 后端/);
    expect(() => createBackend('no-such-backend')).toThrow(/codex-appserver/);
  });

  it('backendIds lists every registered backend', () => {
    expect(backendIds()).toContain('codex-appserver');
    expect(backendIds()).toContain('claude-sdk');
    expect(backendIds()).toContain('claude-acp');
  });

  it('claude-acp 解析到 AcpBackend（订阅·ACP），仅 full 档（细节见 acp-backend.test）', () => {
    const be = createBackend('claude-acp');
    expect(be).toBeInstanceOf(AcpBackend);
    expect(be.id).toBe('claude-acp');
    expect(be.displayName).toBe('Claude（订阅·ACP）');
    expect(be.supportedModes).toEqual(['full']);
  });
});

describe('claude-sdk backend：能力守卫（无半实现）', () => {
  const be = createBackend('claude-sdk') as ClaudeSdkBackend;

  it('注册表解析到 ClaudeSdkBackend，capabilities：compact 已实现(true)，goal/steer/resume 仍未支持', () => {
    expect(be).toBeInstanceOf(ClaudeSdkBackend);
    expect(be.id).toBe('claude-sdk');
    expect(be.capabilities).toEqual({
      goal: false,
      steer: false,
      // 手动 /compact 经 SDK 实现（推 "/compact" → status[compact_result]+compact_boundary）。
      compact: true,
      resume: false,
      approvals: false,
    });
  });

  it('doctor() 返回探测结构，且 ok 与 isAvailable 一致（不耦合 node_modules 里 SDK 装没装）', async () => {
    // SDK 是按需依赖（dev 可能没装、生产靠 Web 下载），所以不硬断言 ok=true——只验
    // 「doctor.ok 必须等于 isAvailable」（装了都 true、没装都 false），装了再验 location。
    const probe = await be.doctor();
    expect(probe.ok).toBe(await be.isAvailable());
    if (probe.ok) expect(probe.location).toContain('claude-agent-sdk');
  });

  it('listThreads（/resume 选择卡）抛明确的「暂不支持」错误', async () => {
    await expect(be.listThreads('/tmp')).rejects.toThrow(/暂不支持.*resume 历史会话/);
  });

  it('readHistory 按接口契约 never-throws：返回空转写', async () => {
    await expect(be.readHistory('/tmp', 'sess-x')).resolves.toEqual({ turns: [], totalTurns: 0 });
  });

  it('qa/write 权限档 fail-closed：spawn 之前即拒绝，绝不降级为完全访问', async () => {
    await expect(be.startThread({ cwd: '/tmp', mode: 'qa' })).rejects.toThrow(/仅支持「完全访问」/);
    await expect(be.startThread({ cwd: '/tmp', mode: 'write' })).rejects.toThrow(/绝不静默降级/);
    // resumeThread（重启恢复路径）同样 fail-closed —— 守卫在 spawn 之前
    await expect(be.resumeThread({ cwd: '/tmp', sessionId: 'sess-x', mode: 'qa' })).rejects.toThrow(
      /仅支持「完全访问」/,
    );
  });

  it('listModels 返回静态 claude 别名，含默认项（pickDefault 可用）', async () => {
    const models = await be.listModels();
    expect(models.some((m) => m.isDefault && !m.hidden)).toBe(true);
    expect(models.map((m) => m.id)).toContain('sonnet');
  });
});

describe('claude-sdk backend：跨后端 model id 自愈（sanitizeClaudeModel）', () => {
  it('本后端别名原样放行', () => {
    expect(sanitizeClaudeModel({ model: 'sonnet' }).model).toBe('sonnet');
    expect(sanitizeClaudeModel({ model: 'opus' }).model).toBe('opus');
    expect(sanitizeClaudeModel({ model: undefined }).model).toBeUndefined();
  });

  it('跨后端污染的 codex model id 被忽略（落回 CLI 默认，存量坏记录自愈）', () => {
    const opts = { cwd: '/p', model: 'gpt-5.5' };
    const safe = sanitizeClaudeModel(opts);
    expect(safe.model).toBeUndefined();
    expect(safe.cwd).toBe('/p'); // 其余字段不动
    expect(opts.model).toBe('gpt-5.5'); // 入参不被原地修改
  });
});
