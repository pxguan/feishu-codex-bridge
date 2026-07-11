package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func ptrInt(v int) *int { return &v }

// TestHandleContextCommand_WithUsage /context 应展示最近一轮的 token 用量。
func TestHandleContextCommand_WithUsage(t *testing.T) {
	o := newTestOrchestrator(t)
	const chatID = "oc_ctx_1"
	win := 200000
	o.sessions.Store(chatID, &SessionEntry{
		Thread: nil, // 测试不依赖真实 backend，仅测用量展示
		LastState: &SessionState{
			Usage: &agent.ContextUsage{UsedTokens: 5000, ContextWindow: &win},
		},
	})
	var sent []byte
	o.SendCardFunc = func(_ context.Context, _ string, cardJSON []byte) (string, error) {
		sent = cardJSON
		return "om_test", nil
	}

	o.handleContextCommand(context.Background(), NormalizedMessage{ChatID: chatID}, &project.Project{Name: "p", ChatID: chatID})

	if sent == nil {
		t.Fatal("未发出 /context 卡片")
	}
	s := string(sent)
	if !strings.Contains(s, "已用") || !strings.Contains(s, "5000") || !strings.Contains(s, "200000") {
		t.Fatalf("/context 卡片未含用量 5000/200000：%s", s)
	}
	if !strings.Contains(s, "2%") {
		t.Fatalf("/context 卡片未算出百分比：%s", s)
	}
}

// TestHandleContextCommand_NoSession /context 无活跃会话时给出引导文案。
func TestHandleContextCommand_NoSession(t *testing.T) {
	o := newTestOrchestrator(t)
	const chatID = "oc_ctx_none"
	var sent []byte
	o.SendCardFunc = func(_ context.Context, _ string, cardJSON []byte) (string, error) {
		sent = cardJSON
		return "om_test", nil
	}

	o.handleContextCommand(context.Background(), NormalizedMessage{ChatID: chatID}, &project.Project{Name: "p", ChatID: chatID})

	if sent == nil {
		t.Fatal("未发出 /context 卡片")
	}
	if !strings.Contains(string(sent), "没有活跃会话") {
		t.Fatalf("/context 应提示无活跃会话：%s", string(sent))
	}
}
