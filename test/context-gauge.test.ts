import { describe, expect, it } from 'vitest';
import {
  buildContextCard,
  ctxPercent,
  ctxTier,
  runCardGauge,
  CTX_CRIT,
  CTX_HIGH,
  CTX_WARN,
} from '../src/card/context-gauge';

type Div = { text: { content: string; text_color: string } };

describe('ctxTier', () => {
  it('tiers by usage fraction (boundaries inclusive)', () => {
    expect(ctxTier(0.1).level).toBe(0);
    expect(ctxTier(CTX_WARN).level).toBe(1);
    expect(ctxTier(CTX_HIGH).level).toBe(2);
    expect(ctxTier(CTX_CRIT).level).toBe(3);
    expect(ctxTier(1.5).level).toBe(3); // over 100% still red
  });

  it('runs green → red with a colored dot', () => {
    expect(ctxTier(0).color).toBe('green');
    expect(ctxTier(0).dot).toBe('🟢');
    expect(ctxTier(CTX_CRIT).color).toBe('red');
    expect(ctxTier(CTX_CRIT).dot).toBe('🔴');
  });
});

describe('ctxPercent', () => {
  it('is null when the window is unknown', () => {
    expect(ctxPercent(100, null)).toBeNull();
    expect(ctxPercent(100, 0)).toBeNull();
  });
  it('rounds and caps at 100', () => {
    expect(ctxPercent(50, 100)).toBe(50);
    expect(ctxPercent(200, 100)).toBe(100);
  });
});

describe('runCardGauge', () => {
  it('is null below the warn threshold (run card stays clean)', () => {
    expect(runCardGauge(10, 100)).toBeNull();
    expect(runCardGauge(50, 100)).toBeNull();
  });
  it('is null when the window is unknown (cannot tier)', () => {
    expect(runCardGauge(9999, null)).toBeNull();
  });
  it('renders a colored, /compact-nudging line at/above the threshold', () => {
    const warn = runCardGauge(70, 100) as unknown as Div;
    expect(warn.text.text_color).toBe('yellow');

    const crit = runCardGauge(96, 100) as unknown as Div;
    expect(crit.text.text_color).toBe('red');
    expect(crit.text.content).toContain('96%');
    expect(crit.text.content).toContain('/compact');
  });
});

describe('buildContextCard', () => {
  it('always renders a percentage, even at low usage', () => {
    expect(JSON.stringify(buildContextCard(10, 100))).toContain('10%');
  });
  it('degrades gracefully when the window is unknown', () => {
    expect(JSON.stringify(buildContextCard(0, null))).toContain('还没有用量数据');
    expect(JSON.stringify(buildContextCard(1500, null))).toContain('tokens');
  });
});
