package codex

// models.go —— codex model/list 归一（对齐 TS backend.ts 的 mapModel + STATIC_MODELS）。

import "github.com/modelzen/feishu-codex-bridge/internal/agent"

// rawModel codex model/list 原始条目。
type rawModel struct {
	ID                        string `json:"id"`
	DisplayName               string `json:"displayName"`
	Description               string `json:"description"`
	Hidden                    bool   `json:"hidden"`
	IsDefault                 bool   `json:"isDefault"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort agent.ReasoningEffort `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort agent.ReasoningEffort `json:"defaultReasoningEffort"`
}

// MapModel 把 codex 原始模型归一为 ModelInfo（缺省兜底：displayName→id、effort→medium）。
func MapModel(m rawModel) agent.ModelInfo {
	efforts := make([]agent.ReasoningEffort, 0, len(m.SupportedReasoningEfforts))
	for _, e := range m.SupportedReasoningEfforts {
		efforts = append(efforts, e.ReasoningEffort)
	}
	def := m.DefaultReasoningEffort
	if def == "" {
		def = agent.EffortMedium
	}
	name := m.DisplayName
	if name == "" {
		name = m.ID
	}
	return agent.ModelInfo{
		ID:               m.ID,
		DisplayName:      name,
		Description:      m.Description,
		Hidden:           m.Hidden,
		IsDefault:        m.IsDefault,
		SupportedEfforts: efforts,
		DefaultEffort:    def,
	}
}

// StaticModels codex 不可用 / model/list 失败时的静态兜底。
var StaticModels = []agent.ModelInfo{
	{
		ID:               "gpt-5.5",
		DisplayName:      "GPT-5.5",
		Description:      "默认模型",
		Hidden:           false,
		IsDefault:        true,
		SupportedEfforts: []agent.ReasoningEffort{agent.EffortLow, agent.EffortMedium, agent.EffortHigh},
		DefaultEffort:    agent.EffortMedium,
	},
}
