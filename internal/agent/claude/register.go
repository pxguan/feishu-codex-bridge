package claude

import "github.com/modelzen/feishu-codex-bridge/internal/agent"

// register.go —— claude 后端注册到 agent registry（init 触发）。
// main 程序 import _ "github.com/modelzen/feishu-codex-bridge/internal/agent/claude" 即注册。

func init() {
	agent.RegisterBackend("claude-agent", func() agent.AgentBackend {
		return &ClaudeBackend{}
	})
}
