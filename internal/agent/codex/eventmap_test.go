package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func notif(method string, params any) ServerNotification {
	b, _ := json.Marshal(params)
	return ServerNotification{Method: method, Params: b}
}

func kindJSON(k string) json.RawMessage {
	b, _ := json.Marshal(k)
	return b
}

func kindJSONObject(k string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"type": k})
	return b
}

// ── MapNotification method 分发 ──────────────────────────────────

func TestMapNotification_ThreadStart(t *testing.T) {
	e, ok := MapNotification(notif("thread/started", map[string]any{"thread": map[string]any{"id": "tid"}}), nil)
	if !ok || e.Type != agent.EvSystem || e.ThreadID != "tid" {
		t.Fatalf("wrong: %+v ok=%v", e, ok)
	}
}

func TestMapNotification_TurnStart(t *testing.T) {
	e, ok := MapNotification(notif("turn/started", map[string]any{"turn": map[string]any{"id": "turn1"}}), nil)
	if !ok || e.Type != agent.EvTurnStarted || e.TurnID != "turn1" {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_AgentMessageDelta(t *testing.T) {
	e, ok := MapNotification(notif("item/agentMessage/delta", map[string]any{"itemId": "i1", "delta": "hi"}), nil)
	if !ok || e.Type != agent.EvTextDelta || e.ItemID != "i1" || e.Delta != "hi" {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_ReasoningDelta(t *testing.T) {
	e, ok := MapNotification(notif("item/reasoning/textDelta", map[string]any{"itemId": "i1", "delta": "th"}), nil)
	if !ok || e.Type != agent.EvThinkingDelta || e.Delta != "th" {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_TokenUsage_UsesLastNotTotal(t *testing.T) {
	e, ok := MapNotification(notif("thread/tokenUsage/updated", map[string]any{
		"tokenUsage": map[string]any{
			"last":               map[string]any{"totalTokens": 5000},
			"modelContextWindow": 200000,
		},
	}), nil)
	if !ok || e.Type != agent.EvContextUsage || e.UsedTokens != 5000 {
		t.Fatalf("should use last.totalTokens=5000: %+v", e)
	}
	if e.ContextWindow == nil || *e.ContextWindow != 200000 {
		t.Fatalf("context window wrong: %+v", e)
	}
}

func TestMapNotification_ItemStartCommand(t *testing.T) {
	e, ok := MapNotification(notif("item/started", map[string]any{"item": map[string]any{
		"id": "i1", "type": "commandExecution", "command": "ls -la", "cwd": "/tmp",
	}}), nil)
	if !ok || e.Type != agent.EvToolUse || e.Title != "ls -la" || e.Detail != "/tmp" {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_ItemStartWebSearch(t *testing.T) {
	e, ok := MapNotification(notif("item/started", map[string]any{"item": map[string]any{
		"id": "i1", "type": "webSearch",
	}}), nil)
	if !ok || e.Title != "联网搜索" {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_ItemCompleteAgentMessage(t *testing.T) {
	e, ok := MapNotification(notif("item/completed", map[string]any{"item": map[string]any{
		"id": "i1", "type": "agentMessage", "text": "hello",
	}}), nil)
	if !ok || e.Type != agent.EvText || e.Text != "hello" {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_ItemCompleteCommandResult(t *testing.T) {
	e, ok := MapNotification(notif("item/completed", map[string]any{"item": map[string]any{
		"id": "i1", "type": "commandExecution", "aggregatedOutput": "done", "exitCode": 0,
	}}), nil)
	if !ok || e.Type != agent.EvToolResult || e.Output != "done" || e.ExitCode == nil || *e.ExitCode != 0 {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_ItemCompleteReasoning(t *testing.T) {
	e, ok := MapNotification(notif("item/completed", map[string]any{"item": map[string]any{
		"id": "i1", "type": "reasoning", "content": []string{"thought1", "thought2"},
	}}), nil)
	if !ok || e.Type != agent.EvThinking || e.Text != "thought1\nthought2" {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_CompactedDoneGoal(t *testing.T) {
	e, ok := MapNotification(notif("thread/compacted", map[string]any{}), nil)
	if !ok || e.Type != agent.EvContextCompacted {
		t.Fatalf("compacted wrong: %+v", e)
	}
	e, ok = MapNotification(notif("turn/completed", map[string]any{"turn": map[string]any{"id": "t1"}}), nil)
	if !ok || e.Type != agent.EvDone || e.TurnID != "t1" {
		t.Fatalf("done wrong: %+v", e)
	}
	budget := 1000000
	e, ok = MapNotification(notif("thread/goal/updated", map[string]any{"goal": map[string]any{
		"status": "active", "objective": "do X", "tokensUsed": 100, "timeUsedSeconds": 30, "tokenBudget": budget,
	}}), nil)
	if !ok || e.Type != agent.EvGoalUpdate || e.Status != "active" || e.Objective != "do X" || e.TokenBudget == nil {
		t.Fatalf("goal wrong: %+v", e)
	}
}

func TestMapNotification_Error(t *testing.T) {
	e, ok := MapNotification(notif("error", map[string]any{
		"error": map[string]any{"message": "boom"}, "willRetry": true,
	}), nil)
	if !ok || e.Type != agent.EvError || e.Message != "boom" || !e.WillRetry {
		t.Fatalf("wrong: %+v", e)
	}
}

func TestMapNotification_UnknownReturnsFalse(t *testing.T) {
	if _, ok := MapNotification(notif("some/future/method", map[string]any{}), nil); ok {
		t.Fatal("unknown method should return ok=false")
	}
}

// ── fileChange 标题（kind-aware）──────────────────────────────────

func TestFileChangeTitle_AddNewFile(t *testing.T) {
	changes := []FileUpdateChange{{Path: "foo.md", Diff: "line1\nline2", Kind: kindJSON("add")}}
	if title := FileChangeTitle(changes, ""); title != "新建 foo.md (+2)" {
		t.Fatalf("got %q", title)
	}
}

func TestFileChangeTitle_DeleteNoCount(t *testing.T) {
	changes := []FileUpdateChange{{Path: "foo.md", Diff: "x\ny", Kind: kindJSON("delete")}}
	if title := FileChangeTitle(changes, ""); title != "删除 foo.md" {
		t.Fatalf("got %q", title)
	}
}

func TestFileChangeTitle_UpdateCounts(t *testing.T) {
	changes := []FileUpdateChange{{Path: "foo.ts", Diff: "+a\n-b\n c\n+++f\n---h", Kind: kindJSON("update")}}
	title := FileChangeTitle(changes, "")
	// +a 是 add（+++f 是 file header 不计），-b 是 del（---h 不计）→ (+1 −1)
	if title != "编辑 foo.ts (+1 −1)" {
		t.Fatalf("got %q", title)
	}
}

func TestFileChangeTitle_ObjectKindForm(t *testing.T) {
	// kind 也可以是 {type:"add"} 形态。
	changes := []FileUpdateChange{{Path: "foo.md", Diff: "x", Kind: kindJSONObject("add")}}
	if title := FileChangeTitle(changes, ""); title != "新建 foo.md (+1)" {
		t.Fatalf("object-kind form wrong: %q", title)
	}
}

func TestFileChangeTitle_RelativeToCwd(t *testing.T) {
	changes := []FileUpdateChange{{Path: "/proj/src/a.ts", Diff: "+a", Kind: kindJSON("update")}}
	title := FileChangeTitle(changes, "/proj")
	if !strings.Contains(title, "src/a.ts") {
		t.Fatalf("should relativize to cwd: %q", title)
	}
	if strings.Contains(title, "/proj/src/a.ts") {
		t.Fatalf("should not keep full path: %q", title)
	}
}

func TestFileChangeTitle_MultiFilesWithCount(t *testing.T) {
	changes := []FileUpdateChange{
		{Path: "a.ts", Diff: "+a", Kind: kindJSON("update")},
		{Path: "b.ts", Diff: "+b", Kind: kindJSON("update")},
		{Path: "c.ts", Diff: "+c", Kind: kindJSON("update")},
	}
	title := FileChangeTitle(changes, "")
	if !strings.Contains(title, "等 3 个文件") {
		t.Fatalf("multi-file count wrong: %q", title)
	}
}

func TestFileChangeTitle_EmptyFallback(t *testing.T) {
	if title := FileChangeTitle(nil, ""); title != "编辑文件" {
		t.Fatalf("empty changes fallback wrong: %q", title)
	}
}

// ── fileChange diff（kind-aware）──────────────────────────────────

func TestFileChangeDiffMd_AddPrefixes(t *testing.T) {
	changes := []FileUpdateChange{{Path: "foo.md", Diff: "hello\nworld", Kind: kindJSON("add")}}
	md := FileChangeDiffMd(changes)
	if !strings.HasPrefix(md, "```diff\n") || !strings.Contains(md, "+hello\n+world") {
		t.Fatalf("add should prefix +: %q", md)
	}
}

func TestFileChangeDiffMd_DeletePrefixes(t *testing.T) {
	changes := []FileUpdateChange{{Path: "foo.md", Diff: "x", Kind: kindJSON("delete")}}
	md := FileChangeDiffMd(changes)
	if !strings.Contains(md, "-x") {
		t.Fatalf("delete should prefix -: %q", md)
	}
}

func TestFileChangeDiffMd_UpdatePassThrough(t *testing.T) {
	changes := []FileUpdateChange{{Path: "foo.ts", Diff: "+a\n-b", Kind: kindJSON("update")}}
	md := FileChangeDiffMd(changes)
	if !strings.Contains(md, "+a\n-b") {
		t.Fatalf("update should pass through unified diff: %q", md)
	}
}

func TestFileChangeDiffMd_MultiFileGitHeader(t *testing.T) {
	changes := []FileUpdateChange{
		{Path: "a.ts", Diff: "+a", Kind: kindJSON("update")},
		{Path: "b.ts", Diff: "+b", Kind: kindJSON("update")},
	}
	md := FileChangeDiffMd(changes)
	if !strings.Contains(md, "diff --git a/a.ts b/a.ts") || !strings.Contains(md, "diff --git a/b.ts b/b.ts") {
		t.Fatalf("multi-file should get git headers: %q", md)
	}
}

func TestFileChangeDiffMd_Truncates(t *testing.T) {
	long := strings.Repeat("x", diffMax+100)
	changes := []FileUpdateChange{{Path: "a.ts", Diff: long, Kind: kindJSON("update")}}
	md := FileChangeDiffMd(changes)
	if !strings.Contains(md, "已截断") {
		t.Fatalf("long diff should be truncated: %q (len=%d)", md, len(md))
	}
}

func TestFileChangeDiffMd_EmptyReturnsEmpty(t *testing.T) {
	if md := FileChangeDiffMd(nil); md != "" {
		t.Fatalf("empty changes should return empty, got %q", md)
	}
}
