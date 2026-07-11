package clibridge

import (
	"strings"
	"testing"
	"time"
)

func TestParseHookPayloadClaudePermission(t *testing.T) {
	raw := `{"hook_event_name":"PermissionRequest","session_id":"s1","cwd":"/proj","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/x"},"stop_hook_active":true}`
	msg := ParseHookPayload(AgentClaude, raw, nil)
	if msg.Type != MsgTypePermissionRequest {
		t.Fatalf("type=%s want permission_request", msg.Type)
	}
	if msg.Source != AgentClaude || msg.SessionID != "s1" || msg.Cwd != "/proj" || msg.ToolName != "Bash" {
		t.Fatalf("fields wrong: %+v", msg)
	}
	if msg.ToolInput["command"] != "rm -rf /tmp/x" {
		t.Fatalf("toolInput wrong: %+v", msg.ToolInput)
	}
	if !msg.StopHookActive {
		t.Fatalf("stopHookActive should be true")
	}
}

func TestParseHookPayloadCodexStopSummary(t *testing.T) {
	raw := `{"hook_event_name":"Stop","session_id":"s2","cwd":"/p","summary":"all done"}`
	msg := ParseHookPayload(AgentCodex, raw, nil)
	if msg.Type != MsgTypeTaskComplete {
		t.Fatalf("type=%s want task_complete", msg.Type)
	}
	if msg.Summary != "all done" {
		t.Fatalf("summary=%q", msg.Summary)
	}
	if msg.TaskStatus != "completed" {
		t.Fatalf("taskStatus=%q", msg.TaskStatus)
	}
}

func TestParseHookPayloadBridgeOwned(t *testing.T) {
	msg := ParseHookPayload(AgentClaude, `{}`, map[string]string{"FEISHU_CODEX_BRIDGE": "1"})
	if !msg.BridgeOwned {
		t.Fatalf("bridgeOwned should be true")
	}
}

func TestExtractAskUserQuestion(t *testing.T) {
	good := map[string]any{
		"questions": []any{
			map[string]any{"question": "Q1?", "options": []any{
				map[string]any{"label": "A"}, map[string]any{"label": "B"},
			}},
			map[string]any{"question": "Q2?", "multiSelect": true, "options": []any{
				map[string]any{"label": "X"}, map[string]any{"label": "Y"},
			}},
		},
	}
	got := ExtractAskUserQuestion(good)
	if got == nil || len(got.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %+v", got)
	}
	if !got.Questions[1].MultiSelect {
		t.Fatalf("Q2 should be multiselect")
	}

	// 选项不足 2 → 拒绝。
	if ExtractAskUserQuestion(map[string]any{"questions": []any{
		map[string]any{"question": "Q?", "options": []any{map[string]any{"label": "A"}}},
	}}) != nil {
		t.Fatalf("single-option should be rejected")
	}
	// 超过 4 → 拒绝。
	many := map[string]any{"questions": make([]any, 5)}
	for i := range many["questions"].([]any) {
		many["questions"].([]any)[i] = map[string]any{"question": "q", "options": []any{map[string]any{"label": "a"}, map[string]any{"label": "b"}}}
	}
	if ExtractAskUserQuestion(many) != nil {
		t.Fatalf("5 questions should be rejected")
	}
}

func TestBuildHookStdoutClaudeAllow(t *testing.T) {
	msg := CliHookMessage{Type: MsgTypePermissionRequest, Source: AgentClaude, HookEventName: "PermissionRequest"}
	out := buildHookStdout(msg, CliHookResponse{Decision: DecisionAllow})
	if !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("claude allow stdout=%s", out)
	}
	if !strings.Contains(out, `"hookEventName":"PermissionRequest"`) {
		t.Fatalf("missing hookEventName: %s", out)
	}
}

func TestBuildHookStdoutClaudeAllowUpdatedInput(t *testing.T) {
	msg := CliHookMessage{Type: MsgTypePermissionRequest, Source: AgentClaude, HookEventName: "PermissionRequest"}
	out := buildHookStdout(msg, CliHookResponse{Decision: DecisionAllow, UpdatedInput: map[string]any{"answers": "x"}})
	if !strings.Contains(out, `"updatedInput"`) {
		t.Fatalf("updatedInput missing: %s", out)
	}
}

func TestBuildHookStdoutCodexDeny(t *testing.T) {
	msg := CliHookMessage{Type: MsgTypePermissionRequest, Source: AgentCodex, HookEventName: "PermissionRequest"}
	out := buildHookStdout(msg, CliHookResponse{Decision: DecisionDeny, Reason: "nope"})
	if !strings.Contains(out, `"behavior":"deny"`) || !strings.Contains(out, "nope") {
		t.Fatalf("codex deny stdout=%s", out)
	}
}

func TestBuildHookStdoutFallbackLocal(t *testing.T) {
	msg := CliHookMessage{Type: MsgTypePermissionRequest, Source: AgentClaude, HookEventName: "PermissionRequest"}
	if out := buildHookStdout(msg, CliHookResponse{Decision: DecisionFallbackLocal}); out != "" {
		t.Fatalf("fallback should be empty, got %q", out)
	}
}

func TestBuildHookStdoutPostToolUse(t *testing.T) {
	msg := CliHookMessage{Type: MsgTypePostToolUse, Source: AgentClaude, HookEventName: "PostToolUse"}
	if out := buildHookStdout(msg, CliHookResponse{Decision: DecisionAllow}); out != "{}" {
		t.Fatalf("post_tool_use should be {}, got %q", out)
	}
}

func TestStoreCreateWaitResolve(t *testing.T) {
	p := CreatePendingCliInteraction(PendingCliInteraction{Kind: PendingPermission, Source: AgentClaude, Cwd: "/x"})
	if p.ID == "" {
		t.Fatal("empty id")
	}
	// 先 resolve（决策快过 waitFor）→ 缓冲到 settled。
	ok := ResolvePendingCliInteraction(p.ID, CliHookResponse{Decision: DecisionAllow})
	if !ok {
		t.Fatal("resolve failed")
	}
	resp := WaitForPendingCliInteraction(p.ID, 100)
	if resp.Decision != DecisionAllow {
		t.Fatalf("expected buffered allow, got %+v", resp)
	}
}

func TestStoreWaitThenResolve(t *testing.T) {
	p := CreatePendingCliInteraction(PendingCliInteraction{Kind: PendingQuestion, Source: AgentClaude, Cwd: "/x"})
	done := make(chan CliHookResponse, 1)
	go func() { done <- WaitForPendingCliInteraction(p.ID, 5000) }()
	time.Sleep(20 * time.Millisecond)
	if !ResolvePendingCliInteraction(p.ID, CliHookResponse{Decision: DecisionDeny, Reason: "r"}) {
		t.Fatal("resolve failed")
	}
	select {
	case resp := <-done:
		if resp.Decision != DecisionDeny {
			t.Fatalf("got %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not wake")
	}
}

func TestStoreTimeout(t *testing.T) {
	p := CreatePendingCliInteraction(PendingCliInteraction{Kind: PendingPermission, Source: AgentClaude, Cwd: "/x"})
	resp := WaitForPendingCliInteraction(p.ID, 50)
	if resp.Decision != DecisionFallbackLocal || resp.Reason != "timeout" {
		t.Fatalf("expected timeout fallback, got %+v", resp)
	}
}

type fakeProc struct{ killed bool }

func (f *fakeProc) Kill() error { f.killed = true; return nil }

func TestKeepAwakeRefCount(t *testing.T) {
	var spawned int
	spawn := func() KeepAwakeProcess {
		spawned++
		return &fakeProc{}
	}
	c := CreateKeepAwakeController(func() bool { return true }, spawn)
	c.Acquire()
	c.Acquire()
	if spawned != 1 {
		t.Fatalf("expected 1 spawn, got %d", spawned)
	}
	c.Release()
	if !c.IsActive() {
		t.Fatalf("should still be active after 1 release")
	}
	c.Release()
	if c.IsActive() {
		t.Fatalf("should be inactive after 2 releases")
	}
}

func TestKeepAwakeDisabled(t *testing.T) {
	var spawned int
	c := CreateKeepAwakeController(func() bool { return false }, func() KeepAwakeProcess {
		spawned++
		return &fakeProc{}
	})
	c.Acquire()
	if spawned != 0 || c.IsActive() {
		t.Fatalf("disabled keepawake should not spawn")
	}
}
