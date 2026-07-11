package codex

import "github.com/modelzen/feishu-codex-bridge/internal/agent"

// register.go —— codex 后端注册到 agent registry（init 触发）。
// main 程序 import _ "github.com/modelzen/feishu-codex-bridge/internal/agent/codex" 即注册。

func init() {
	agent.RegisterBackend(agent.DEFAULT_BACKEND_ID, func() agent.AgentBackend {
		return &CodexAppServerBackend{}
	})
}
