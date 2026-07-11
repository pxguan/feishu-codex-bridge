package codex

import "github.com/modelzen/feishu-codex-bridge/internal/agent"

// detect_register.go —— codex detect 探测注入 agent.DetectAgentsFns。
// codex import 后 agent.DetectAgents() 自动探 codex bin + version。

func init() {
	agent.RegisterDetect(detectCodexAgent)
}

// detectCodexAgent 探 codex agent → AgentRuntime。
func detectCodexAgent() []agent.AgentRuntime {
	entry, _ := agent.CatalogByID(agent.DEFAULT_BACKEND_ID)
	bin := ResolveCodexBin(true)
	version := ""
	if bin != "" {
		version = CodexVersion(bin, true)
	}
	installed := bin != "" && version != ""
	reason := ""
	if bin == "" {
		reason = entry.Dep.DetectHint
	} else if version == "" {
		reason = "codex --version 失败"
	}
	return []agent.AgentRuntime{{
		ID:          "codex",
		DisplayName: "Codex",
		Installed:   installed,
		Version:     version,
		InstallHint: ifNot(installed, entry.Dep.DetectHint),
		Backends: []agent.BackendAvailability{{
			BackendID:      agent.DEFAULT_BACKEND_ID,
			Available:      installed,
			Reason:         reason,
			Version:        version,
			SupportedModes: entry.SupportedModes,
			Installable:    false,
		}},
	}}
}

func ifNot(cond bool, s string) string {
	if cond {
		return ""
	}
	return s
}
