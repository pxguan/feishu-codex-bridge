package card

import (
	"strings"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestBuildHistoryCard_Empty(t *testing.T) {
	c := BuildHistoryCard(HistoryCardState{Cwd: "/p", History: agent.ThreadHistory{}}, time.Now())
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	joined := joinMd(els)
	if !strings.Contains(joined, "还没有可显示的历史") {
		t.Fatalf("empty history should show hint: %q", joined)
	}
}

func TestBuildHistoryCard_TurnsAsPanels(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	s := HistoryCardState{
		Cwd: "/proj",
		History: agent.ThreadHistory{
			TotalTurns: 2,
			Turns: []agent.HistoryTurn{
				{UserText: "问题1", AssistantText: "答案1"},
				{UserText: "问题2", AssistantText: "答案2", Reasoning: "想了想", Tools: []agent.HistoryTool{{Title: "ls"}}},
			},
		},
	}
	c := BuildHistoryCard(s, now)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	panelCount := 0
	for _, e := range els {
		if e["tag"] == "collapsible_panel" {
			panelCount++
		}
	}
	if panelCount != 2 {
		t.Fatalf("want 2 panels (1 per turn), got %d", panelCount)
	}
}

func TestBuildHistoryCard_HeaderDropped(t *testing.T) {
	c := BuildHistoryCard(HistoryCardState{
		Cwd:     "/p",
		History: agent.ThreadHistory{TotalTurns: 5, Turns: []agent.HistoryTurn{{UserText: "x"}}},
	}, time.Now())
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	if !strings.Contains(joinMd(els), "仅显示最近 1 轮") {
		t.Fatal("should note dropped turns")
	}
}

func TestBuildGoalDoneCard_Success(t *testing.T) {
	c := BuildGoalDoneCard(GoalDoneCardData{Objective: "做完X", Status: "complete", TokensUsed: 12345, TimeUsedSeconds: 461})
	h := c["header"].(CardElement)
	if h["template"] != "green" {
		t.Fatalf("success → green, got %v", h["template"])
	}
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !strings.Contains(joined, "12,345") {
		t.Fatalf("tokens should be comma-formatted: %q", joined)
	}
	if !strings.Contains(joined, "约 7 分 41 秒") {
		t.Fatalf("duration wrong: %q", joined)
	}
}

func TestBuildGoalDoneCard_Abnormal(t *testing.T) {
	c := BuildGoalDoneCard(GoalDoneCardData{Objective: "X", Status: "budgetLimited", TokensUsed: 0, TimeUsedSeconds: 30})
	h := c["header"].(CardElement)
	if h["template"] != "orange" {
		t.Fatal("abnormal → orange")
	}
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !strings.Contains(joined, "Token 预算用尽") {
		t.Fatalf("should show budget reason: %q", joined)
	}
}

func TestFmtDuration(t *testing.T) {
	cases := map[int64]string{
		45:   "约 45 秒",
		60:   "约 1 分",
		461:  "约 7 分 41 秒",
		3600: "约 1 时",
		3780: "约 1 时 3 分",
	}
	for in, want := range cases {
		if got := fmtDuration(in); got != want {
			t.Errorf("fmtDuration(%d)=%q want %q", in, got, want)
		}
	}
}
