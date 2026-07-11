package card

import (
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestFormatTokensZh(t *testing.T) {
	cases := map[int64]string{
		9530:       "9,530",
		448568:     "44.9万",
		258804367:  "2.6亿",
		4271434092: "42.7亿",
		99999999:   "1亿", // 边界进位
		0:          "0",
	}
	for in, want := range cases {
		if got := FormatTokensZh(in); got != want {
			t.Errorf("FormatTokensZh(%d)=%q want %q", in, got, want)
		}
	}
}

func TestWindowLabel(t *testing.T) {
	if WindowLabel(0) != "限额" {
		t.Fatal("0 → 限额")
	}
	if WindowLabel(18000) != "5 小时" {
		t.Fatal("18000 → 5 小时")
	}
	if WindowLabel(604800) != "7 天" {
		t.Fatal("604800 → 7 天")
	}
	if WindowLabel(7200) != "2 小时" {
		t.Fatal("7200 → 2 小时")
	}
}

func TestResetLabel(t *testing.T) {
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.Local)
	nowMs := now.UnixMilli()
	// 同一天。
	today := time.Date(2026, 7, 3, 0, 28, 0, 0, time.Local)
	if got := ResetLabel(today.Unix(), nowMs); got != "今天 00:28" {
		t.Fatalf("today wrong: %q", got)
	}
	// 明天。
	tomorrow := time.Date(2026, 7, 4, 8, 41, 0, 0, time.Local)
	if got := ResetLabel(tomorrow.Unix(), nowMs); got != "明天 08:41" {
		t.Fatalf("tomorrow wrong: %q", got)
	}
}

func TestPlanLabel(t *testing.T) {
	if PlanLabel("plus") != "Plus" {
		t.Fatal("plus → Plus")
	}
	if PlanLabel("") != "" {
		t.Fatal("empty → empty")
	}
	if PlanLabel("unknown") != "Unknown" {
		t.Fatal("unknown → Unknown (capitalize)")
	}
}

func TestFormatDurationZh(t *testing.T) {
	if FormatDurationZh(42*60) != "42 分" {
		t.Fatal("42m")
	}
	if FormatDurationZh(75*60) != "1 小时 15 分" {
		t.Fatal("1h15m")
	}
	if FormatDurationZh(120*60) != "2 小时" {
		t.Fatal("2h")
	}
}

func TestEffortLabelZh(t *testing.T) {
	if EffortLabelZh("xhigh") != "超高" {
		t.Fatal("xhigh → 超高")
	}
	if EffortLabelZh("custom") != "custom" {
		t.Fatal("unknown passthrough")
	}
}

func TestHeatmapCells_GridShape(t *testing.T) {
	// today=2026-07-03（周五），buckets 一条。
	h := HeatmapCells([]agent.DailyBucket{{Date: "2026-07-03", Tokens: 100}}, "2026-07-03", 14)
	if h.Weeks != 14 {
		t.Fatalf("weeks=%d want 14", h.Weeks)
	}
	if h.EndDate != "2026-07-03" {
		t.Fatalf("endDate=%q", h.EndDate)
	}
	// 每周 7 行，但今天之后截断；总格子 ≤ 14*7。
	if len(h.Values) > 14*7 {
		t.Fatalf("too many cells: %d", len(h.Values))
	}
	// 含今日的格子（value=100）。
	hasToday := false
	for _, c := range h.Values {
		if c.Value == 100 {
			hasToday = true
		}
	}
	if !hasToday {
		t.Fatal("should contain today's bucket")
	}
}

func TestParseShareSections_DefaultAll(t *testing.T) {
	// 空 = 全部。
	got := ParseShareSections(nil)
	if len(got) != len(ShareSections) {
		t.Fatalf("empty should select all: %d", len(got))
	}
}

func TestParseShareSections_Picked(t *testing.T) {
	got := ParseShareSections([]string{"stats", "heatmap"})
	if len(got) != 2 || !got[ShareStats] || !got[ShareHeatmap] {
		t.Fatalf("picked wrong: %+v", got)
	}
}

func TestParseShareSections_UnknownDropped(t *testing.T) {
	// 全部未知值 → 回退全部（picked 空）。
	got := ParseShareSections([]string{"bogus"})
	if len(got) != len(ShareSections) {
		t.Fatalf("all-unknown should fall back to all: %d", len(got))
	}
}
