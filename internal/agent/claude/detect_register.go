package claude

import "github.com/modelzen/feishu-codex-bridge/internal/agent"

// detect_register.go —— claude detect 探测注入 agent.DetectAgentsFns。
// claude import 后 agent.DetectAgents() 自动探 claude bin + version。

func init() {
	agent.RegisterDetect(detectClaudeAgent)
}

// detectClaudeAgent 探 claude agent → AgentRuntime。
func detectClaudeAgent() []agent.AgentRuntime {
	entry, _ := agent.CatalogByID("claude-agent")
	bin := ResolveClaudeBin(true)
	version := ""
	if bin != "" {
		version = ClaudeVersion(bin, true)
	}
	installed := bin != "" && version != ""
	reason := ""
	if bin == "" {
		reason = entry.Dep.DetectHint
	} else if version == "" {
		reason = "claude --version 失败"
	}
	return []agent.AgentRuntime{{
		ID:          "claude",
		DisplayName: "Claude",
		Installed:   installed,
		Version:     version,
		InstallHint: ifNot(installed, entry.Dep.DetectHint),
		Backends: []agent.BackendAvailability{{
			BackendID:      "claude-agent",
			Available:      installed,
			Reason:         reason,
			Version:        version,
			SupportedModes: entry.SupportedModes,
			Installable:    !installed,
		}},
	}}
}

func ifNot(cond bool, s string) string {
	if cond {
		return ""
	}
	return s
}
