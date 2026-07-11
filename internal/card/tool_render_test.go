package card

import (
	"strings"
	"testing"
)

func TestToolHeaderText_StatusIcons(t *testing.T) {
	cases := map[ToolStatus]string{
		ToolRunning: "⏳",
		ToolDone:    "✅",
		ToolError:   "❌",
	}
	for status, icon := range cases {
		h := ToolHeaderText(ToolEntry{Title: "ls", Status: status})
		if !strings.HasPrefix(h, icon) {
			t.Errorf("status %v should start with %s: %q", status, icon, h)
		}
	}
}

func TestToolHeaderText_TruncatesLongTitle(t *testing.T) {
	long := strings.Repeat("a", toolHeaderTitleMax+50)
	h := ToolHeaderText(ToolEntry{Title: long, Status: ToolDone})
	if !strings.HasSuffix(h, "…**") {
		t.Fatalf("long title should be truncated: %q", h)
	}
}

func TestToolBodyMd_EmptyRunning(t *testing.T) {
	if got := ToolBodyMd(ToolEntry{Status: ToolRunning}); got != "_运行中…_" {
		t.Fatalf("empty running body wrong: %q", got)
	}
	if got := ToolBodyMd(ToolEntry{Status: ToolDone}); got != "" {
		t.Fatalf("empty done body should be empty: %q", got)
	}
}

func TestToolBodyMd_BashFence(t *testing.T) {
	body := ToolBodyMd(ToolEntry{Status: ToolDone, Output: "hello world"})
	if !strings.Contains(body, "```bash") || !strings.Contains(body, "hello world") {
		t.Fatalf("should wrap in bash fence: %q", body)
	}
	if !strings.HasPrefix(body, "**Output**") {
		t.Fatalf("should label Output: %q", body)
	}
}

func TestToolBodyMd_PreFencedPassthrough(t *testing.T) {
	// 预围栏（```diff）原样透传，不二次包裹 bash。
	pre := "```diff\n+a\n-b\n```"
	body := ToolBodyMd(ToolEntry{Status: ToolDone, Output: pre})
	if strings.Contains(body, "```bash") {
		t.Fatalf("pre-fenced output should NOT be re-wrapped: %q", body)
	}
	if !strings.Contains(body, "```diff") {
		t.Fatalf("pre-fenced diff should pass through: %q", body)
	}
}

func TestToolBodyMd_ErrorLabel(t *testing.T) {
	body := ToolBodyMd(ToolEntry{Status: ToolError, Output: "boom"})
	if !strings.HasPrefix(body, "**Error**") {
		t.Fatalf("error should label Error: %q", body)
	}
}

func TestToolBodyMd_TruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("x", toolBodyTotalMax+500)
	body := ToolBodyMd(ToolEntry{Status: ToolDone, Output: long})
	if !strings.Contains(body, "已截断") {
		t.Fatalf("over-budget body should be truncated: len=%d", len(body))
	}
}
