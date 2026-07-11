package card

// usage_cards.go —— Codex 用量卡 + 分享卡（对齐 TS card/usage-cards 的卡片构造）。
// 组装 usage 格式化 + chart spec + element builder + DM action id + agent.AccountUsageBundle。

import (
	"fmt"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// 热力图色带（GitHub 蓝；0 值浅灰）。
var heatRange = []string{"#ebedf0", "#bbdefb", "#64b5f6", "#1e88e5", "#0d47a1"}

// 圆角方块 symbol path（VChart heatmap cell 是 symbol 图元，cornerRadius 无效，用自定义 path）。
const roundedCell = "M -0.5 -0.25 Q -0.5 -0.5 -0.25 -0.5 L 0.25 -0.5 Q 0.5 -0.5 0.5 -0.25 L 0.5 0.25 Q 0.5 0.5 0.25 0.5 L -0.25 0.5 Q -0.5 0.5 -0.5 0.25 Z"

// HeatmapChartEl 热力图 chart 元素（14 列/7 行 GitHub 风格）。
func HeatmapChartEl(buckets []agent.DailyBucket, today string) CardElement {
	h := HeatmapCells(buckets, today, 14)
	values := make([]map[string]any, 0, len(h.Values))
	for _, c := range h.Values {
		values = append(values, map[string]any{"week": c.Week, "day": c.Day, "value": c.Value, "label": c.Label})
	}
	return CardElement{
		"tag":          "chart",
		"aspect_ratio": "2:1",
		"chart_spec": map[string]any{
			"type":    "common",
			"padding": 4,
			"data":    []map[string]any{{"id": "usage", "values": values}},
			"series": []map[string]any{{
				"type":       "heatmap",
				"xField":     "week",
				"yField":     "day",
				"valueField": "label",
				"cell":       map[string]any{"style": map[string]any{"fill": map[string]any{"field": "value", "scale": "color"}, "shape": roundedCell}},
			}},
			"color": map[string]any{"type": "linear", "domain": []map[string]any{{"dataId": "usage", "fields": []string{"value"}}}, "range": heatRange},
			"axes": []map[string]any{
				{"orient": "bottom", "type": "band", "bandPadding": 0.25, "domainLine": map[string]any{"visible": false}, "tick": map[string]any{"visible": false}},
				{"orient": "left", "type": "band", "bandPadding": 0.25, "domainLine": map[string]any{"visible": false}, "tick": map[string]any{"visible": false}, "label": map[string]any{"visible": false}},
			},
			"legends": map[string]any{"visible": false},
			"tooltip": map[string]any{"visible": true, "mark": map[string]any{"title": map[string]any{"visible": false}}},
		},
	}
}

func remainingPct(w agent.RateWindow) int {
	r := 100 - w.UsedPercent
	if r < 0 {
		r = 0
	}
	return r
}

// ProgressChartEl 单条限额进度图（linearProgress，值=剩余比例 0~1）。
func ProgressChartEl(w agent.RateWindow) CardElement {
	label := WindowLabel(w.WindowSeconds) + "剩余"
	return CardElement{
		"tag":    "chart",
		"height": "40px",
		"chart_spec": map[string]any{
			"type":         "linearProgress",
			"data":         []map[string]any{{"id": "p", "values": []map[string]any{{"type": label, "value": float64(remainingPct(w)) / 100}}}},
			"xField":       "value",
			"yField":       "type",
			"cornerRadius": 8,
			"bandWidth":    12,
			"axes": []map[string]any{
				{"orient": "left", "type": "band", "visible": false},
				{"orient": "bottom", "type": "linear", "visible": false},
			},
			"tooltip": map[string]any{
				"visible": true,
				"mark":    map[string]any{"title": map[string]any{"visible": false}, "content": []map[string]any{{"key": label, "value": fmt.Sprintf("%d%%", remainingPct(w))}}},
			},
		},
	}
}

func rateLimitElements(bucket agent.RateBucket, nowMs int64) []CardElement {
	var out []CardElement
	icons := []string{"⚡", "📅"}
	windows := []*agent.RateWindow{bucket.Primary, bucket.Secondary}
	for i, w := range windows {
		if w == nil {
			continue
		}
		reset := ""
		if w.ResetAt != 0 {
			reset = fmt.Sprintf("　<font color='grey'>%s 重置</font>", ResetLabel(w.ResetAt, nowMs))
		}
		out = append(out, Md(fmt.Sprintf("%s **%s限额**　剩余 %d%%%s", icons[i], WindowLabel(w.WindowSeconds), remainingPct(*w), reset)))
		out = append(out, ProgressChartEl(*w))
	}
	if len(out) == 0 {
		return []CardElement{Note("暂无限额数据")}
	}
	return out
}

func statColumns(items []struct{ Value, Label string }) CardElement {
	columns := make([]CardElement, 0, len(items))
	for _, it := range items {
		columns = append(columns, CardElement{
			"tag":   "column",
			"width": "auto",
			"elements": []CardElement{
				{"tag": "markdown", "content": fmt.Sprintf("**%s**", it.Value), "text_size": "heading"},
				NoteMd(it.Label),
			},
		})
	}
	return CardElement{"tag": "column_set", "flex_mode": "flow", "horizontal_spacing": "large", "columns": columns}
}

func profileStatItems(p agent.AccountProfileStats) []struct{ Value, Label string } {
	out := []struct{ Value, Label string }{
		{FormatTokensZh(p.LifetimeTokens), "累计 Token 数"},
		{FormatTokensZh(p.PeakDailyTokens), "峰值 Token 数"},
		{FormatDurationZh(p.LongestTurnSec), "最长任务时长"},
	}
	curStreak := "—"
	if p.CurrentStreakDays != 0 {
		curStreak = fmt.Sprintf("%d 天", p.CurrentStreakDays)
	}
	out = append(out, struct{ Value, Label string }{curStreak, "当前连续天数"})
	longest := "—"
	if p.LongestStreakDays != 0 {
		longest = fmt.Sprintf("%d 天", p.LongestStreakDays)
	}
	out = append(out, struct{ Value, Label string }{longest, "最长连续天数"})
	return out
}

func heatmapElements(p agent.AccountProfileStats, today string) []CardElement {
	return []CardElement{Md("📈 **每日 Token 用量**"), HeatmapChartEl(p.DailyBuckets, today)}
}

func insightsElements(p agent.AccountProfileStats) []CardElement {
	var left []string
	if p.FastModePct != 0 {
		left = append(left, fmt.Sprintf("Fast Mode　**%d%%", roundIntPct(p.FastModePct)))
	}
	if p.MostUsedEffort != "" {
		pct := ""
		if p.MostUsedEffortPct != 0 {
			pct = fmt.Sprintf(" · %d%%", roundIntPct(p.MostUsedEffortPct))
		}
		left = append(left, fmt.Sprintf("最常用推理　**%s%s**", EffortLabelZh(p.MostUsedEffort), pct))
	}
	if p.UniqueSkillsUsed != 0 {
		left = append(left, fmt.Sprintf("使用过的技能　**%d**", p.UniqueSkillsUsed))
	}
	if p.TotalSkillsUsed != 0 {
		left = append(left, fmt.Sprintf("技能调用总数　**%s**", commaInt64(int64(p.TotalSkillsUsed))))
	}
	if p.TotalThreads != 0 {
		left = append(left, fmt.Sprintf("会话总数　**%s**", commaInt64(int64(p.TotalThreads))))
	}
	var right []string
	for i, t := range p.TopInvocations {
		if i >= 5 {
			break
		}
		mark := "$"
		if t.Kind == "plugin" {
			mark = "@"
		}
		right = append(right, fmt.Sprintf("%s%s　**×%d**", mark, t.Name, t.Count))
	}
	col := func(title string, lines []string) CardElement {
		return CardElement{"tag": "column", "width": "weighted", "weight": 1, "elements": []CardElement{
			Md(fmt.Sprintf("**%s**", title)), NoteMd(strings.Join(lines, "\n")),
		}}
	}
	var columns []CardElement
	if len(left) > 0 {
		columns = append(columns, col("活动洞察", left))
	}
	if len(right) > 0 {
		columns = append(columns, col("常用插件 / 技能", right))
	}
	if len(columns) == 0 {
		return nil
	}
	flex := "stretch"
	if len(columns) == 2 {
		flex = "bisect"
	}
	return []CardElement{{"tag": "column_set", "flex_mode": flex, "horizontal_spacing": "large", "columns": columns}}
}

func joinWithHr(blocks ...[]CardElement) []CardElement {
	var present [][]CardElement
	for _, b := range blocks {
		if len(b) > 0 {
			present = append(present, b)
		}
	}
	var out []CardElement
	for i, b := range present {
		if i > 0 {
			out = append(out, Hr())
		}
		out = append(out, b...)
	}
	return out
}

func usageButtons() CardElement {
	return Actions([]CardElement{
		Button("🔄 刷新", ActionValue{"a": DMUsageRefresh}, ButtonDefault),
		Button("📤 生成分享卡", ActionValue{"a": DMUsageShare}, ButtonPrimary),
		Button("⬅️ 菜单", ActionValue{"a": DMMenu}, ButtonDefault),
	}, "")
}

// usageErrorCopy UsageErrorKind 中文文案。
var usageErrorCopy = map[agent.UsageErrorKind]struct{ Title, Hint string }{
	agent.UsageErrNoAuth:      {"未找到 Codex 登录态", "本机没有可读的 `~/.codex/auth.json`，请在宿主机终端运行 `codex login` 后重试。"},
	agent.UsageErrAPIKeyMode:  {"当前是 API-key 登录模式", "用量统计与限额数据仅 **ChatGPT 登录**（`codex login`）可用，API-key 模式没有这份数据。"},
	agent.UsageErrNeedRelogin: {"Codex 登录态已失效", "令牌已无法刷新（过期/被撤销），请在宿主机终端重新运行 `codex login`。"},
	agent.UsageErrTransient:   {"暂时拉不到数据", "网络或 ChatGPT 服务波动，稍后点「🔄 刷新」重试。"},
}

// UsageCardPhase 用量卡阶段。
type UsageCardPhase string

const (
	UsagePhaseLoading UsageCardPhase = "loading"
	UsagePhaseError   UsageCardPhase = "error"
	UsagePhaseReady   UsageCardPhase = "ready"
)

// UsageCardState 用量卡状态。
type UsageCardState struct {
	Phase   UsageCardPhase
	Data    *agent.AccountUsageBundle
	Kind    agent.UsageErrorKind
	Message string
	NowMs   int64
	Today   string
}

// BuildUsageCard DM 控制台用量卡。
func BuildUsageCard(s UsageCardState) CardObject {
	noForward := false
	if s.Phase == UsagePhaseLoading {
		return Card([]CardElement{Md("⏳ 正在拉取 Codex 用量数据…"), Note("查询 ChatGPT 后端，通常 1~3 秒。")}, CardOpts{
			Header: &CardHeader{Title: "📊 Codex 用量", Template: HeaderWathet}, Forward: &noForward,
		})
	}
	if s.Phase == UsagePhaseError {
		copy := usageErrorCopy[s.Kind]
		els := []CardElement{Md("⚠️ **" + copy.Title + "**"), Md(copy.Hint)}
		if s.Kind == agent.UsageErrTransient {
			els = append(els, Note(s.Message))
		}
		els = append(els, usageButtons())
		return Card(els, CardOpts{Header: &CardHeader{Title: "📊 Codex 用量", Template: HeaderOrange}, Forward: &noForward})
	}
	profile, usage := s.Data.Profile, s.Data.Usage
	nowMs := s.NowMs
	elements := joinWithHr(
		rateLimitElements(usage.Main, nowMs),
		[]CardElement{statColumns(profileStatItems(profile))},
		heatmapElements(profile, s.Today),
		insightsElements(profile),
	)
	plan := PlanLabel(usage.PlanType)
	foot := fmt.Sprintf("统计截至 %s", fallback(profile.StatsAsOf, "—"))
	if plan != "" {
		foot += " · " + plan + " 套餐"
	}
	foot += " · 数据来自 Codex 个人资料"
	elements = append(elements, Note(foot), usageButtons())
	opts := CardOpts{Forward: &noForward, Header: &CardHeader{Title: "📊 Codex 用量", Template: HeaderWathet}}
	if profile.DisplayName != "" {
		opts.Header.Subtitle = profile.DisplayName
	}
	return Card(elements, opts)
}

// BuildShareConfigCard 「选择分享内容」表单卡。
func BuildShareConfigCard(done bool) CardObject {
	noForward := false
	options := make([]CardElement, 0, len(ShareSections))
	for _, sec := range ShareSections {
		options = append(options, CardElement{"text": CardElement{"tag": "plain_text", "content": sec.Label}, "value": string(sec.Key)})
	}
	els := []CardElement{
		Md("选择要放进分享卡的内容（**不选 = 全部展示**），生成后长按 / 右键即可转发："),
		{"tag": "form", "name": "shareCfg", "elements": []CardElement{
			{"tag": "multi_select_static", "name": "secs", "placeholder": CardElement{"tag": "plain_text", "content": "默认全部展示，可只挑部分区块"}, "options": options},
			SubmitButton("📤 生成分享卡", ActionValue{"a": DMUsageShareDo}, ButtonPrimary, ""),
		}},
	}
	if done {
		els = append(els, Note("✅ 分享卡已生成（见下方新卡片）。换个组合可再次生成。"))
	}
	els = append(els, Actions([]CardElement{
		Button("⬅️ 返回用量", ActionValue{"a": DMUsage}, ButtonDefault),
		Button("🏠 菜单", ActionValue{"a": DMMenu}, ButtonDefault),
	}, ""))
	return Card(els, CardOpts{Header: &CardHeader{Title: "📤 分享内容选择", Template: HeaderBlue}, Forward: &noForward})
}

// BuildUsageShareCard 分享卡（纯展示、零回调、发后不更新）。
func BuildUsageShareCard(data agent.AccountUsageBundle, nowMs int64, today string, sections map[ShareSectionKey]bool) CardObject {
	profile, usage := data.Profile, data.Usage
	if sections == nil {
		sections = map[ShareSectionKey]bool{}
		for _, s := range ShareSections {
			sections[s.Key] = true
		}
	}
	who := "我的"
	if profile.DisplayName != "" {
		who = profile.DisplayName + " 的"
	}
	plan := PlanLabel(usage.PlanType)
	var blocks [][]CardElement
	if sections[ShareStats] {
		blocks = append(blocks, []CardElement{statColumns(profileStatItems(profile))})
	}
	if sections[ShareHeatmap] {
		blocks = append(blocks, heatmapElements(profile, today))
	}
	if sections[ShareInsights] {
		blocks = append(blocks, insightsElements(profile))
	}
	if sections[ShareLimits] {
		blocks = append(blocks, rateLimitElements(usage.Main, nowMs))
	}
	if sections[SharePlan] && plan != "" {
		blocks = append(blocks, []CardElement{Md("💎 **套餐**　" + plan)})
	}
	elements := joinWithHr(blocks...)
	// 页脚生成时刻。
	stamp := msToTime(nowMs)
	stampStr := fmt.Sprintf("%d月%d日 %02d:%02d", int(stamp.Month()), stamp.Day(), stamp.Hour(), stamp.Minute())
	elements = append(elements, CardElement{
		"tag": "markdown", "content": fmt.Sprintf("<font color='grey'>🤖 由 </font>[feishu-codex-bridge](https://my.feishu.cn/docx/AFKNdf4QaooL5OxSR8bc5H7vn7b)<font color='grey'> 于 %s 生成</font>", stampStr),
		"text_size": "notation", "text_align": "right",
	})
	opts := CardOpts{Header: &CardHeader{Title: fmt.Sprintf("📊 %s Codex 用量", who), Template: HeaderBlue}}
	if profile.StatsAsOf != "" {
		opts.Header.Subtitle = "统计截至 " + profile.StatsAsOf
	}
	return Card(elements, opts)
}

func roundIntPct(n int) int {
	return n
}

func msToTime(ms int64) time.Time { return time.UnixMilli(ms) }
