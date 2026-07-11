package claude

import (
	"encoding/json"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestMapMsg_SystemInit(t *testing.T) {
	m := createTurnMapper("")
	evs := m.mapMsg(claudeMessage{Type: "system", Subtype: "init", SessionID: "abc", Model: "claude-opus-4-8"})
	if len(evs) != 1 || evs[0].Type != agent.EvSystem || evs[0].ThreadID != "abc" {
		t.Fatalf("wrong: %+v", evs)
	}
}

func TestMapMsg_CompactBoundary(t *testing.T) {
	m := createTurnMapper("")
	evs := m.mapMsg(claudeMessage{Type: "system", Subtype: "compact_boundary"})
	if len(evs) != 1 || evs[0].Type != agent.EvContextCompacted {
		t.Fatalf("wrong: %+v", evs)
	}
}

func TestMapMsg_StreamText(t *testing.T) {
	m := createTurnMapper("")
	m.mapMsg(claudeMessage{Type: "stream_event", Event: raw(`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`)})
	evs := m.mapMsg(claudeMessage{Type: "stream_event", Event: raw(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)})
	if len(evs) != 1 || evs[0].Type != agent.EvTextDelta || evs[0].Delta != "hi" {
		t.Fatalf("wrong: %+v", evs)
	}
	evs2 := m.mapMsg(claudeMessage{Type: "stream_event", Event: raw(`{"type":"content_block_stop","index":0}`)})
	if len(evs2) != 1 || evs2[0].Type != agent.EvText || evs2[0].Text != "hi" {
		t.Fatalf("wrong: %+v", evs2)
	}
}

func TestMapMsg_AssistantToolUse(t *testing.T) {
	m := createTurnMapper("/proj")
	msg := claudeMessage{Type: "assistant", Message: raw(`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/proj/a.go"}}]}`)}
	evs := m.mapMsg(msg)
	if len(evs) != 1 || evs[0].Type != agent.EvToolUse || evs[0].Title != "读取 a.go" {
		t.Fatalf("wrong: %+v", evs)
	}
}

func TestMapMsg_UserToolResult(t *testing.T) {
	m := createTurnMapper("")
	msg := claudeMessage{Type: "user", Message: raw(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":[{"type":"text","text":"out"}]}]}`)}
	evs := m.mapMsg(msg)
	if len(evs) != 1 || evs[0].Type != agent.EvToolResult || evs[0].Output != "out" {
		t.Fatalf("wrong: %+v", evs)
	}
	if evs[0].ExitCode == nil || *evs[0].ExitCode != 0 {
		t.Fatalf("exit code should be 0: %+v", evs[0])
	}
}

func TestMapMsg_UserToolResultError(t *testing.T) {
	m := createTurnMapper("")
	msg := claudeMessage{Type: "user", Message: raw(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"boom"}]}`)}
	evs := m.mapMsg(msg)
	if evs[0].ExitCode == nil || *evs[0].ExitCode != 1 || evs[0].Output != "boom" {
		t.Fatalf("wrong: %+v", evs)
	}
}

func TestMapMsg_ResultUsage(t *testing.T) {
	m := createTurnMapper("")
	msg := claudeMessage{Type: "result", Subtype: "success", Usage: raw(`{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}`)}
	evs := m.mapMsg(msg)
	if len(evs) != 2 {
		t.Fatalf("want usage+context, got %d: %+v", len(evs), evs)
	}
	if evs[0].Type != agent.EvUsage || evs[0].InputTokens != 100 || evs[0].OutputTokens != 50 {
		t.Fatalf("usage wrong: %+v", evs[0])
	}
	if evs[1].Type != agent.EvContextUsage || evs[1].UsedTokens != 115 {
		t.Fatalf("context wrong: %+v", evs[1])
	}
	if evs[1].ContextWindow == nil || *evs[1].ContextWindow != 200_000 {
		t.Fatalf("window wrong: %+v", evs[1])
	}
}

func TestMapMsg_ApiRetry(t *testing.T) {
	m := createTurnMapper("")
	evs := m.mapMsg(claudeMessage{Type: "system_api_retry"})
	if len(evs) != 1 || evs[0].Type != agent.EvError || !evs[0].WillRetry {
		t.Fatalf("wrong: %+v", evs)
	}
}

func TestToolTitle(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		cwd   string
		want  string
	}{
		{"Bash", map[string]any{"command": "ls -la"}, "", "ls -la"},
		{"Write", map[string]any{"file_path": "/proj/a.go"}, "/proj", "写入 a.go"},
		{"WebSearch", map[string]any{"query": "golang"}, "", "联网搜索 golang"},
		{"Unknown", map[string]any{}, "", "Unknown"},
	}
	for _, c := range cases {
		if got := toolTitle(c.name, c.input, c.cwd); got != c.want {
			t.Fatalf("toolTitle(%q)=%q want %q", c.name, got, c.want)
		}
	}
}

func TestContextWindowFor(t *testing.T) {
	// 空模型落到默认 200k（与 MapMsg_ResultUsage 的 usage 窗口一致，驱动仪表盘百分比）。
	if w := contextWindowFor(""); w == nil || *w != 200_000 {
		t.Fatalf("empty model → default 200k window, got %v", w)
	}
	if w := contextWindowFor("claude-opus-4-8"); w == nil || *w != 200_000 {
		t.Fatalf("default window wrong: %v", w)
	}
	if w := contextWindowFor("claude-opus-4-8-1m"); w == nil || *w != 1_000_000 {
		t.Fatalf("1m window wrong: %v", w)
	}
}
