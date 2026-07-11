package agent_test

// detect_multi_test.go —— 回归：多个后端包（codex + claude）同时 import 时，
// DetectAgents() 必须返回全部，而非被后初始化的包覆盖（DetectAgentsFn 单变量覆盖 bug）。
// 这里 import 触发两个包的 init() 注册 detect 函数。
import (
	"testing"

	_ "github.com/modelzen/feishu-codex-bridge/internal/agent/claude"
	_ "github.com/modelzen/feishu-codex-bridge/internal/agent/codex"
	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestDetectAgents_AllBackends(t *testing.T) {
	agents := agent.DetectAgents()
	seen := map[string]bool{}
	for _, a := range agents {
		seen[a.ID] = true
	}
	if !seen["codex"] {
		t.Fatal("DetectAgents should include codex")
	}
	if !seen["claude"] {
		t.Fatal("DetectAgents should include claude (regression: was overwritten by codex init)")
	}
}

func TestCreateBackend_ClaudeRegistered(t *testing.T) {
	b, err := agent.CreateBackend("claude-agent")
	if err != nil {
		t.Fatalf("CreateBackend(claude-agent) err: %v", err)
	}
	if b == nil {
		t.Fatal("CreateBackend(claude-agent) returned nil backend")
	}
}
