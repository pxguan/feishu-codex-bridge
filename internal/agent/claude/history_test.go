package claude

import (
	"encoding/json"
	"testing"
)

func histMsg(typ, content string) claudeHistoryMessage {
	m := claudeHistoryMessage{Type: typ}
	m.Message.Content = json.RawMessage(content)
	return m
}

func TestFoldSessionMessages_SingleTurn(t *testing.T) {
	msgs := []claudeHistoryMessage{
		histMsg("user", `[{"type":"text","text":"hi"}]`),
		histMsg("assistant", `[{"type":"text","text":"hello there"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]`),
		histMsg("user", `[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":[{"type":"text","text":"out"}]}]`),
	}
	h := foldSessionMessages(msgs, 10, "")
	if h.TotalTurns != 1 {
		t.Fatalf("total=%d", h.TotalTurns)
	}
	turn := h.Turns[0]
	if turn.UserText != "hi" {
		t.Fatalf("userText=%q", turn.UserText)
	}
	if turn.AssistantText != "hello there" {
		t.Fatalf("assistantText=%q", turn.AssistantText)
	}
	if len(turn.Tools) != 1 || turn.Tools[0].Title != "ls" {
		t.Fatalf("tools=%+v", turn.Tools)
	}
	if turn.Tools[0].Output != "out" {
		t.Fatalf("tool output=%q", turn.Tools[0].Output)
	}
}

func TestFoldSessionMessages_SkipsBoilerplate(t *testing.T) {
	msgs := []claudeHistoryMessage{
		histMsg("user", `[{"type":"text","text":"<environment_context>secret</environment_context>"}]`),
		histMsg("user", `[{"type":"text","text":"real question"}]`),
		histMsg("assistant", `[{"type":"text","text":"answer"}]`),
	}
	h := foldSessionMessages(msgs, 10, "")
	if h.TotalTurns != 1 {
		t.Fatalf("total=%d", h.TotalTurns)
	}
	if h.Turns[0].UserText != "real question" {
		t.Fatalf("boilerplate not skipped: %q", h.Turns[0].UserText)
	}
}

func TestFoldSessionMessages_Truncates(t *testing.T) {
	var msgs []claudeHistoryMessage
	for i := 0; i < 5; i++ {
		msgs = append(msgs,
			histMsg("user", `[{"type":"text","text":"q`+string(rune('0'+i))+`"}]`),
			histMsg("assistant", `[{"type":"text","text":"a`+string(rune('0'+i))+`"}]`),
		)
	}
	h := foldSessionMessages(msgs, 2, "")
	if h.TotalTurns != 5 {
		t.Fatalf("total should be 5, got %d", h.TotalTurns)
	}
	if len(h.Turns) != 2 {
		t.Fatalf("kept should be 2, got %d", len(h.Turns))
	}
	if h.Turns[0].UserText != "q3" {
		t.Fatalf("should keep last 2, got %q", h.Turns[0].UserText)
	}
}

func TestIsBoilerplateUserText(t *testing.T) {
	if !isBoilerplateUserText("<environment_context>x") {
		t.Fatal("should be boilerplate")
	}
	if !isBoilerplateUserText("# AGENTS.md instructions") {
		t.Fatal("should be boilerplate")
	}
	if isBoilerplateUserText("real question") {
		t.Fatal("should NOT be boilerplate")
	}
}
