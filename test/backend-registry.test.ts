import { describe, expect, it } from 'vitest';
import { DEFAULT_BACKEND_ID, backendIds, createBackend } from '../src/agent';
import { CodexAppServerBackend } from '../src/agent/codex-appserver/backend';

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
  });
});
