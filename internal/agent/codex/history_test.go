package codex

import (
	"encoding/json"
	"testing"
)

func turnWithItems(items ...ThreadItem) Turn {
	return Turn{Items: items, StartedAt: 1700000000}
}

func rawMsg(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMapTurn_UserAssistantReasoning(t *testing.T) {
	turn := turnWithItems(
		ThreadItem{Type: "userMessage", Content: rawMsg(t, []map[string]string{{"type": "text", "text": "你好"}})},
		ThreadItem{Type: "reasoning", Content: rawMsg(t, []string{"想一下"})},
		ThreadItem{Type: "agentMessage", Text: "你好！"},
	)
	ht := MapTurn(turn)
	if ht.UserText != "你好" {
		t.Fatalf("userText=%q", ht.UserText)
	}
	if ht.AssistantText != "你好！" {
		t.Fatalf("assistantText=%q", ht.AssistantText)
	}
	if ht.Reasoning != "想一下" {
		t.Fatalf("reasoning=%q", ht.Reasoning)
	}
	if ht.StartedAt != 1700000000 {
		t.Fatalf("startedAt=%d", ht.StartedAt)
	}
}

func TestMapTurn_BoilerplateFiltered(t *testing.T) {
	turn := turnWithItems(
		ThreadItem{Type: "userMessage", Content: rawMsg(t, []map[string]string{{"type": "text", "text": "<environment_context>...</environment_context>"}})},
		ThreadItem{Type: "userMessage", Content: rawMsg(t, []map[string]string{{"type": "text", "text": "# AGENTS.md instructions\nfoo"}})},
		ThreadItem{Type: "userMessage", Content: rawMsg(t, []map[string]string{{"type": "text", "text": "真实问题"}})},
	)
	ht := MapTurn(turn)
	if ht.UserText != "真实问题" {
		t.Fatalf("boilerplate should be filtered, userText=%q", ht.UserText)
	}
}

func TestMapTurn_Mention(t *testing.T) {
	turn := turnWithItems(
		ThreadItem{Type: "userMessage", Content: rawMsg(t, []map[string]string{
			{"type": "text", "text": "hi "},
			{"type": "mention", "name": "codex"},
		})},
	)
	if ht := MapTurn(turn); ht.UserText != "hi @codex" {
		t.Fatalf("mention should render @name: %q", ht.UserText)
	}
}

func TestMapTurn_CommandExecutionFailed(t *testing.T) {
	code := 1
	turn := turnWithItems(ThreadItem{Type: "commandExecution", Command: "ls", AggregatedOutput: "err", ExitCode: &code, Status: "completed"})
	ht := MapTurn(turn)
	if len(ht.Tools) != 1 || ht.Tools[0].Title != "ls" || !ht.Tools[0].Failed {
		t.Fatalf("command tool wrong: %+v", ht.Tools)
	}
	code = 0
	turn2 := turnWithItems(ThreadItem{Type: "commandExecution", Command: "ls", ExitCode: &code, Status: "completed"})
	ht2 := MapTurn(turn2)
	if ht2.Tools[0].Failed {
		t.Fatal("exit 0 + completed should not be failed")
	}
	turn3 := turnWithItems(ThreadItem{Type: "commandExecution", Command: "ls", Status: "failed"})
	if !MapTurn(turn3).Tools[0].Failed {
		t.Fatal("status failed should be failed")
	}
}

func TestMapTurn_FileChangeWebSearchMCPDynamic(t *testing.T) {
	successFalse := false
	turn := turnWithItems(
		ThreadItem{Type: "fileChange", Status: "failed"},
		ThreadItem{Type: "webSearch", Query: "golang"},
		ThreadItem{Type: "mcpToolCall", Server: "srv", Tool: "tl", Err: "boom"},
		ThreadItem{Type: "dynamicToolCall", Tool: "dt", Success: &successFalse},
	)
	ht := MapTurn(turn)
	if len(ht.Tools) != 4 {
		t.Fatalf("want 4 tools, got %d: %+v", len(ht.Tools), ht.Tools)
	}
	if ht.Tools[0].Title != "编辑文件" || !ht.Tools[0].Failed {
		t.Fatalf("fileChange wrong: %+v", ht.Tools[0])
	}
	if ht.Tools[1].Title != "联网搜索：golang" {
		t.Fatalf("webSearch wrong: %+v", ht.Tools[1])
	}
	if ht.Tools[2].Title != "srv / tl" || !ht.Tools[2].Failed {
		t.Fatalf("mcpToolCall wrong: %+v", ht.Tools[2])
	}
	if ht.Tools[3].Title != "dt" || !ht.Tools[3].Failed {
		t.Fatalf("dynamicToolCall wrong: %+v", ht.Tools[3])
	}
}

func TestReasoningContentHelper(t *testing.T) {
	item := ThreadItem{Content: rawMsg(t, []string{"a", "b"})}
	if got := item.ReasoningContent(); len(got) != 2 || got[1] != "b" {
		t.Fatalf("ReasoningContent wrong: %v", got)
	}
}

func TestEventMap_ReasoningRegression(t *testing.T) {
	// Content 改 RawMessage 后，event-map reasoning 仍应正确解析 []string。
	e, ok := MapNotification(notif("item/completed", map[string]any{"item": map[string]any{
		"id": "i1", "type": "reasoning", "content": []string{"t1", "t2"},
	}}), nil)
	if !ok || e.Text != "t1\nt2" {
		t.Fatalf("reasoning regression: %+v ok=%v", e, ok)
	}
}
