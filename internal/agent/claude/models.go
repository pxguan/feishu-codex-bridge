package claude

// models.go —— Claude 静态模型目录（对齐 TS backend.ts 的 STATIC_MODELS）。
// `claude` CLI 接受这些 id（--model）。effort 在 thread 创建时通过 Options 施加。

import "github.com/modelzen/feishu-codex-bridge/internal/agent"

var claudeEfforts = []agent.ReasoningEffort{
	agent.EffortLow, agent.EffortMedium, agent.EffortHigh, agent.EffortXhigh,
}

// StaticModels Claude 模型选择器用的静态目录。
var StaticModels = []agent.ModelInfo{
	{
		ID:               "claude-opus-4-8",
		DisplayName:      "Claude Opus 4.8",
		Description:      "最强，复杂推理 / 长程 agentic",
		SupportedEfforts: claudeEfforts,
		DefaultEffort:    agent.EffortHigh,
		IsDefault:        true,
		Hidden:           false,
	},
	{
		ID:               "claude-sonnet-4-6",
		DisplayName:      "Claude Sonnet 4.6",
		Description:      "均衡，日常编码",
		SupportedEfforts: claudeEfforts,
		DefaultEffort:    agent.EffortMedium,
		IsDefault:        false,
		Hidden:           false,
	},
	{
		ID:               "claude-haiku-4-5",
		DisplayName:      "Claude Haiku 4.5",
		Description:      "最快，轻量任务",
		SupportedEfforts: []agent.ReasoningEffort{agent.EffortLow, agent.EffortMedium, agent.EffortHigh},
		DefaultEffort:    agent.EffortLow,
		IsDefault:        false,
		Hidden:           false,
	},
}
