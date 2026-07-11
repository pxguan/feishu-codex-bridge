package clibridge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/card"
)

func mustMarshal(t *testing.T, c card.CardObject) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	return string(b)
}

func TestBuildApprovalCard(t *testing.T) {
	c := BuildCliBridgeApprovalCard(struct {
		ID           string
		Source       CliBridgeAgent
		Cwd          string
		ToolName     string
		Command      string
		AllowSession bool
		Status       interactionStatus
		HookEventName string
		SessionID    string
		CreatedAt    int64
	}{
		ID: "p1", Source: AgentClaude, Cwd: "/proj", ToolName: "Bash",
		Command: "rm -rf /tmp/x", AllowSession: true,
	})
	s := mustMarshal(t, c)
	for _, want := range []string{CLI.ApproveOnce, CLI.Deny, "允许", "拒绝", "proj"} {
		if !strings.Contains(s, want) {
			t.Fatalf("approval card missing %q: %s", want, s)
		}
	}
	// AllowSession=true → 不应出现「始终允许」。
	if strings.Contains(s, CLI.ApproveSession) {
		t.Fatalf("allow-session should suppress 始终允许: %s", s)
	}
}

func TestBuildQuestionCard(t *testing.T) {
	c := BuildCliBridgeQuestionCard(struct {
		ID        string
		Source    CliBridgeAgent
		Cwd       string
		Questions []CliQuestionItem
		Status    interactionStatus
		Answers   map[string]string
		HookEventName string
		CreatedAt int64
	}{
		ID: "q1", Source: AgentClaude, Cwd: "/p",
		Questions: []CliQuestionItem{
			{Question: "Q1?", Options: []CliQuestionOption{{Label: "A"}, {Label: "B"}}},
		},
	})
	s := mustMarshal(t, c)
	if !strings.Contains(s, CLI.QuestionSubmit) {
		t.Fatalf("question card missing submit action: %s", s)
	}
	if !strings.Contains(s, QuestionChoiceField(0)) {
		t.Fatalf("question card missing choice field: %s", s)
	}
	if !strings.Contains(s, QuestionCustomField(0)) {
		t.Fatalf("question card missing custom field: %s", s)
	}
}

func TestBuildTaskCompletionCardReply(t *testing.T) {
	c := BuildCliBridgeTaskCompletionCard(struct {
		ID             string
		Source         CliBridgeAgent
		Cwd            string
		Status         string
		Summary        string
		ReplyEnabled   bool
		SessionID      string
		HookEventName  string
		CreatedAt      int64
		ReplyExpiresAt int64
		ReplyDoneAt    int64
	}{
		ID: "t1", Source: AgentClaude, Cwd: "/p", Status: "completed",
		Summary: "all done", ReplyEnabled: true,
	})
	s := mustMarshal(t, c)
	if !strings.Contains(s, CLI.TaskCompletionDone) {
		t.Fatalf("task card missing done action: %s", s)
	}
	if !strings.Contains(s, "all done") {
		t.Fatalf("task card missing summary: %s", s)
	}
}

func TestBuildSettingsSection(t *testing.T) {
	input := CliBridgeSettingsSectionInput{Enabled: true, NotifyScope: "all", CanEnable: true}
	input.Statuses = map[CliBridgeAgent]CliHookStatus{
		AgentClaude: {Status: HookInstalled},
		AgentCodex:  {Status: HookInstalled},
	}
	input.Agents.Claude = true
	input.Agents.Codex = true
	input.KeepAwake = true
	els := BuildCliBridgeSettingsSection(input)
	b, err := json.Marshal(els)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	s := string(b)
	for _, want := range []string{CLI.ToggleEnabled, CLI.SetNotifyScope, CLI.ToggleAgent, CLI.RepairHooks, "☕"} {
		if !strings.Contains(s, want) {
			t.Fatalf("settings missing %q: %s", want, s)
		}
	}
}

// TestShortElemFeishuConstraint 回归测试：飞书 CardKit 要求 element_id / form name
// ≤ 20 字符、仅 [a-zA-Z0-9_]、且以字母开头。shortElem 必须把长交互 ID 截断到合法范围。
func TestShortElemFeishuConstraint(t *testing.T) {
	longID := "d101771cde74f877b8da8bf2d712f5ae" // 真实场景里的 32 字符交互 ID
	cases := []struct {
		prefix string
		id     string
	}{
		{"approval_", longID},
		{"question_submit_", longID},
		{"cli_question_", longID},
		{"task_done_", longID},
		{"task_done_disabled_", longID},
		{"a", "weird id/with-dash.and.dots"},
	}
	for _, c := range cases {
		got := shortElem(c.prefix, c.id)
		if len(got) > 20 {
			t.Fatalf("shortElem(%q,%q)=%q 超过 20 字符", c.prefix, c.id, got)
		}
		if got == "" || (got[0] < 'a' || got[0] > 'z') && (got[0] < 'A' || got[0] > 'Z') {
			t.Fatalf("shortElem(%q,%q)=%q 必须以字母开头", c.prefix, c.id, got)
		}
		for _, r := range got {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
			if !ok {
				t.Fatalf("shortElem(%q,%q)=%q 含非法字符 %q", c.prefix, c.id, got, string(r))
			}
		}
	}
}

// TestCardElementIDsWithinLimit 回归测试：用真实长度的交互 ID 渲染三类卡片，
// 断言 JSON 里每个 element_id / name 都 ≤ 20 字符（否则飞书会拒绝建卡）。
func TestCardElementIDsWithinLimit(t *testing.T) {
	longID := "d101771cde74f877b8da8bf2d712f5ae"
	cards := []card.CardObject{
		BuildCliBridgeApprovalCard(struct {
			ID           string
			Source       CliBridgeAgent
			Cwd          string
			ToolName     string
			Command      string
			AllowSession bool
			Status       interactionStatus
			HookEventName string
			SessionID    string
			CreatedAt    int64
		}{ID: longID, Source: AgentClaude, Cwd: "/p", ToolName: "Bash", Command: "rm -rf /x", AllowSession: false}),
		BuildCliBridgeQuestionCard(struct {
			ID        string
			Source    CliBridgeAgent
			Cwd       string
			Questions []CliQuestionItem
			Status    interactionStatus
			Answers   map[string]string
			HookEventName string
			CreatedAt int64
		}{ID: longID, Source: AgentClaude, Cwd: "/p", Questions: []CliQuestionItem{
			{Question: "Q?", Options: []CliQuestionOption{{Label: "A"}, {Label: "B"}}},
		}}),
		BuildCliBridgeTaskCompletionCard(struct {
			ID             string
			Source         CliBridgeAgent
			Cwd            string
			Status         string
			Summary        string
			ReplyEnabled   bool
			SessionID      string
			HookEventName  string
			CreatedAt      int64
			ReplyExpiresAt int64
			ReplyDoneAt    int64
		}{ID: longID, Source: AgentClaude, Cwd: "/p", Status: "completed", Summary: "ok", ReplyEnabled: true}),
	}
	for _, c := range cards {
		s := mustMarshal(t, c)
		var doc any
		if err := json.Unmarshal([]byte(s), &doc); err != nil {
			t.Fatalf("unmarshal card: %v", err)
		}
		walkAndCheckIDs(t, doc, "root")
	}
}

func walkAndCheckIDs(t *testing.T, v any, path string) {
	t.Helper()
	switch node := v.(type) {
	case map[string]any:
		for k, val := range node {
			if k == "element_id" || k == "name" {
				str, ok := val.(string)
				if ok && len(str) > 20 {
					t.Fatalf("element_id/name 超长 @ %s: %q (len=%d)", path, str, len(str))
				}
			}
			walkAndCheckIDs(t, val, path+"."+k)
		}
	case []any:
		for i, item := range node {
			walkAndCheckIDs(t, item, path+"["+itoa(i)+"]")
		}
	}
}
