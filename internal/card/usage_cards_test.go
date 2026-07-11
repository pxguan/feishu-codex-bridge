package card

import (
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestBuildUsageCard_Loading(t *testing.T) {
	c := BuildUsageCard(UsageCardState{Phase: UsagePhaseLoading})
	if c["header"].(CardElement)["template"] != "wathet" {
		t.Fatal("loading → wathet header")
	}
	if c["config"].(CardElement)["enable_forward"] != false {
		t.Fatal("usage card should disable forward")
	}
}

func TestBuildUsageCard_Error(t *testing.T) {
	c := BuildUsageCard(UsageCardState{Phase: UsagePhaseError, Kind: agent.UsageErrNoAuth})
	if c["header"].(CardElement)["template"] != "orange" {
		t.Fatal("error → orange")
	}
	body := c["body"].(CardElement)
	if !strings.Contains(joinMd(body["elements"].([]CardElement)), "未找到 Codex 登录态") {
		t.Fatal("error card should show no-auth title")
	}
}

func TestBuildUsageCard_Ready(t *testing.T) {
	data := &agent.AccountUsageBundle{
		Profile: agent.AccountProfileStats{DisplayName: "Test", LifetimeTokens: 1000000, DailyBuckets: []agent.DailyBucket{{Date: "2026-07-03", Tokens: 5000}}},
		Usage: agent.AccountUsageSnapshot{
			Main: agent.RateBucket{Primary: &agent.RateWindow{UsedPercent: 30, WindowSeconds: 18000, ResetAt: 1700000000}},
		},
	}
	c := BuildUsageCard(UsageCardState{Phase: UsagePhaseReady, Data: data, NowMs: 1700000000000, Today: "2026-07-03"})
	h := c["header"].(CardElement)
	if h["subtitle"].(CardElement)["content"] != "Test" {
		t.Fatal("ready header subtitle should be displayName")
	}
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	// 应含限额行 + 统计行 + 热力图 + 刷新按钮。
	hasChart := false
	hasButton := false
	for _, e := range els {
		if e["tag"] == "chart" {
			hasChart = true
		}
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						hasButton = true
					}
				}
			}
		}
	}
	if !hasChart {
		t.Fatal("ready card should contain chart (heatmap/progress)")
	}
	if !hasButton {
		t.Fatal("ready card should contain refresh button")
	}
}

func TestBuildShareConfigCard(t *testing.T) {
	c := BuildShareConfigCard(false)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	// 应含 form（多选下拉 + 提交）。
	hasForm := false
	for _, e := range els {
		if e["tag"] == "form" {
			hasForm = true
		}
	}
	if !hasForm {
		t.Fatal("share config should contain form")
	}
}

func TestBuildUsageShareCard_NoButtons(t *testing.T) {
	data := agent.AccountUsageBundle{
		Profile: agent.AccountProfileStats{DisplayName: "X", LifetimeTokens: 1000, StatsAsOf: "2026-07-01"},
	}
	c := BuildUsageShareCard(data, 1700000000000, "2026-07-03", nil)
	// 分享卡零回调按钮（纯展示）。
	body := c["body"].(CardElement)
	for _, e := range body["elements"].([]CardElement) {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						t.Fatal("share card must have NO callback buttons")
					}
				}
			}
		}
	}
	h := c["header"].(CardElement)
	if h["title"].(CardElement)["content"] != "📊 X 的 Codex 用量" {
		t.Fatalf("share card title wrong: %v", h["title"])
	}
}

func TestBuildUsageShareCard_SectionsFilter(t *testing.T) {
	data := agent.AccountUsageBundle{
		Profile: agent.AccountProfileStats{LifetimeTokens: 1000},
		Usage:   agent.AccountUsageSnapshot{Main: agent.RateBucket{Primary: &agent.RateWindow{UsedPercent: 50, WindowSeconds: 18000}}},
	}
	// 只选 stats → 不应含限额（chart progress）。
	c := BuildUsageShareCard(data, 1700000000000, "2026-07-03", map[ShareSectionKey]bool{ShareStats: true})
	body := c["body"].(CardElement)
	for _, e := range body["elements"].([]CardElement) {
		if e["tag"] == "chart" {
			t.Fatal("stats-only share should not contain limits chart")
		}
	}
}
