package card

import (
	"strings"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestBuildModelCard_BothPickers(t *testing.T) {
	s := ModelCardState{
		Models: []agent.ModelInfo{
			{ID: "a", DisplayName: "A"},
			{ID: "b", DisplayName: "B", SupportedEfforts: []agent.ReasoningEffort{agent.EffortLow, agent.EffortHigh}},
		},
		Model:  "b",
		Effort: agent.EffortHigh,
	}
	c := BuildModelCard(s)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	// 应含 2 个 selectStatic（模型 + effort）。
	selectCount := 0
	for _, e := range els {
		for _, sub := range flattenActions(e) {
			if sub["tag"] == "select_static" {
				selectCount++
			}
		}
	}
	if selectCount != 2 {
		t.Fatalf("want 2 selects (model+effort), got %d", selectCount)
	}
}

func TestBuildModelCard_NoPickersInfoCard(t *testing.T) {
	// 单模型 + 无 effort → 信息卡（无 select）。
	s := ModelCardState{
		Models: []agent.ModelInfo{{ID: "only", DisplayName: "Only"}},
		Model:  "only",
	}
	c := BuildModelCard(s)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	for _, e := range els {
		for _, sub := range flattenActions(e) {
			if sub["tag"] == "select_static" {
				t.Fatal("single model + no effort should have NO select")
			}
		}
	}
}

func TestBuildResumeCard_ButtonPerSession(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	s := ResumeCardState{
		Cwd: "/proj",
		Threads: []agent.ThreadSummary{
			{SessionID: "s1", Preview: "first task", UpdatedAt: 1699999000},
			{SessionID: "s2", Name: "named", UpdatedAt: 1699998000},
		},
		Backend: "codex-appserver",
	}
	c := BuildResumeCard(s, now)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	buttons := 0
	for _, e := range els {
		for _, sub := range flattenActions(e) {
			if sub["tag"] == "button" {
				buttons++
				behaviors := sub["behaviors"].([]CardElement)
				val := behaviors[0]["value"].(ActionValue)
				if val["b"] != "codex-appserver" {
					t.Fatal("button value should carry backend b")
				}
			}
		}
	}
	if buttons != 2 {
		t.Fatalf("want 1 button per session (2), got %d", buttons)
	}
}

func TestBuildResumeCard_Empty(t *testing.T) {
	c := BuildResumeCard(ResumeCardState{Cwd: "/p"}, time.Now())
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	found := false
	for _, e := range els {
		if c, ok := e["content"].(string); ok && strings.Contains(c, "还没有历史会话") {
			found = true
		}
	}
	if !found {
		t.Fatal("empty threads should show no-history hint")
	}
}

func TestBuildHelpCard_MainScopeAdmin(t *testing.T) {
	c := BuildHelpCard(HelpMain, true, true, HelpCaps{})
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	joined := joinMd(els)
	if !strings.Contains(joined, "/resume") || !strings.Contains(joined, "/settings") {
		t.Fatalf("admin main scope should list /resume /settings: %q", joined)
	}
}

func TestBuildHelpCard_CapsHideGoal(t *testing.T) {
	no := false
	c := BuildHelpCard(HelpSingle, true, false, HelpCaps{Goal: &no})
	joined := joinMd(c["body"].(CardElement)["elements"].([]CardElement))
	if strings.Contains(joined, "/goal") {
		t.Fatal("caps.Goal=false should hide /goal")
	}
}

func TestPickerTime_Recent(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	// 5 分钟前（秒）。
	got := PickerTime(now.UnixMilli()/1000-300, now)
	if !strings.Contains(got, "分钟前") {
		t.Fatalf("5min ago should be 分钟前, got %q", got)
	}
	// 刚刚。
	if got := PickerTime(now.UnixMilli()/1000-10, now); got != "刚刚" {
		t.Fatalf("10s ago should be 刚刚, got %q", got)
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	if RelativeTime(0, now) != "未知时间" {
		t.Fatal("0 → 未知时间")
	}
	if RelativeTime(now.UnixMilli()/1000-120, now) != "2 分钟前" {
		t.Fatalf("2min → 2 分钟前")
	}
}

// ── 辅助 ───────────────────────────────────────────────────────

// flattenActions 把 actions(column_set) 里的控件拍平。
func flattenActions(e CardElement) []CardElement {
	if e["tag"] != "column_set" {
		return nil
	}
	columns, _ := e["columns"].([]CardElement)
	var out []CardElement
	for _, col := range columns {
		els, _ := col["elements"].([]CardElement)
		out = append(out, els...)
	}
	return out
}

func joinMd(els []CardElement) string {
	out := ""
	for _, e := range els {
		if c, ok := e["content"].(string); ok {
			out += c
		}
		// Note/div 元素的 content 嵌在 text.content 里。
		if t, ok := e["text"].(CardElement); ok {
			if c, ok := t["content"].(string); ok {
				out += c
			}
		}
	}
	return out
}
