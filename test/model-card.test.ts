import { describe, expect, it } from 'vitest';
import { buildModelCard, type ModelCardState } from '../src/card/command-cards';
import type { ModelInfo } from '../src/agent/types';

// /model 卡按后端能力自适应（对齐 Codex 的诚实体验）：
//  - effort 下拉只在当前模型真有 supportedEfforts 时出现（修旧版「空 effort 回退假
//    low/medium/high」→ claude-sdk/acp 显示点了没用的 effort 下拉）。
//  - 模型下拉只在有多个可见模型时出现。
//  - 都不可调（ACP）→ 信息卡 + 明确「不支持在此切换」。

const model = (over: Partial<ModelInfo> & Pick<ModelInfo, 'id' | 'displayName'>): ModelInfo => ({
  description: '',
  supportedEfforts: [],
  defaultEffort: 'medium',
  isDefault: false,
  hidden: false,
  ...over,
});

const state = (models: ModelInfo[], cur: string): ModelCardState => ({
  chatId: 'c',
  threadId: 't',
  requesterOpenId: 'u',
  models,
  model: cur,
  effort: 'medium',
  createdAt: 0,
});

const codex = [
  model({ id: 'gpt-5.5', displayName: 'GPT-5.5', supportedEfforts: ['low', 'medium', 'high'], isDefault: true }),
  model({ id: 'gpt-5-codex', displayName: 'GPT-5 Codex', supportedEfforts: ['low', 'medium', 'high'] }),
];
const sdk = [
  model({ id: 'sonnet', displayName: 'Claude Sonnet', isDefault: true }),
  model({ id: 'opus', displayName: 'Claude Opus' }),
];
const acp = [model({ id: 'claude-acp-default', displayName: 'Claude Code（订阅）', isDefault: true })];

const json = (s: ModelCardState): string => JSON.stringify(buildModelCard(s));

describe('buildModelCard · 按后端能力自适应', () => {
  it('Codex（多模型 + 真 effort）→ 模型下拉 + effort 下拉都在', () => {
    const j = json(state(codex, 'gpt-5.5'));
    expect(j).toContain('model.set'); // 模型下拉
    expect(j).toContain('model.effort'); // effort 下拉
    expect(j).toContain('effort：中');
  });

  it('claude-sdk（多模型，无 effort）→ 有模型下拉，无 effort 下拉，有说明', () => {
    const j = json(state(sdk, 'sonnet'));
    expect(j).toContain('model.set'); // 仍可切模型
    expect(j).not.toContain('model.effort'); // 不再有假 effort 下拉
    expect(j).not.toContain('effort：'); // 不出现 effort 选项
    expect(j).toContain('不调节推理强度'); // 给出说明
  });

  it('claude-acp（单模型，无 effort）→ 无任何下拉，信息卡 + 不支持说明', () => {
    const j = json(state(acp, 'claude-acp-default'));
    expect(j).not.toContain('model.set');
    expect(j).not.toContain('model.effort');
    expect(j).toContain('Claude Code（订阅）'); // 当前模型以文字呈现
    expect(j).toContain('不支持在此切换'); // 明确说明
  });

  it('hidden 模型不计入「是否多模型」', () => {
    const oneVisible = [
      model({ id: 'a', displayName: 'A', isDefault: true }),
      model({ id: 'b', displayName: 'B', hidden: true }),
    ];
    const j = json(state(oneVisible, 'a'));
    expect(j).not.toContain('model.set'); // 只有 1 个可见 → 不给模型下拉
  });
});
