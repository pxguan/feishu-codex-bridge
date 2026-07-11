package card

// usage_format.go —— Codex 用量卡的纯格式化函数（对齐 TS card/usage-cards 的导出纯函数）。
// 卡片构造（buildUsageCard 等）依赖 DM action id（dm-cards），后续接上。

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// FormatTokensZh 中文单位缩写：4271434092 → 42.7亿；258804367 → 2.6亿；448568 → 44.9万；9530 → 9,530。
func FormatTokensZh(n int64) string {
	if n >= 100_000_000 {
		return fmtZhUnit(float64(n)/1e8) + "亿"
	}
	if n >= 10_000 {
		s := fmtZhUnit(float64(n) / 1e4)
		if s == "10000" {
			return "1亿" // 边界进位：99,999,999 → 1亿 而非 10000万
		}
		return s + "万"
	}
	return commaInt64(n)
}

func fmtZhUnit(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}

func commaInt64(n int64) string {
	if n < 0 {
		return "-" + commaInt64(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
	}
	for i := rem; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// WindowLabel 窗口时长：18000s → 5 小时；604800s → 7 天。
func WindowLabel(seconds int64) string {
	if seconds == 0 {
		return "限额"
	}
	if seconds == 18000 {
		return "5 小时"
	}
	if seconds == 604800 {
		return "7 天"
	}
	if seconds < 86400 {
		return fmt.Sprintf("%d 小时", roundInt(seconds/3600))
	}
	return fmt.Sprintf("%d 天", roundInt(seconds/86400))
}

// ResetLabel 重置时刻 → 「今天 00:28 / 明天 08:41 / 6月11日 08:41」（宿主机本地时区）。
func ResetLabel(resetAtSec, nowMs int64) string {
	d := time.Unix(resetAtSec, 0)
	hm := fmt.Sprintf("%02d:%02d", d.Hour(), d.Minute())
	now := time.UnixMilli(nowMs)
	dayKey := func(t time.Time) string {
		return fmt.Sprintf("%d-%d-%d", t.Year(), int(t.Month()), t.Day())
	}
	if dayKey(d) == dayKey(now) {
		return "今天 " + hm
	}
	tomorrow := now.AddDate(0, 0, 1)
	if dayKey(d) == dayKey(tomorrow) {
		return "明天 " + hm
	}
	return fmt.Sprintf("%d月%d日 %s", int(d.Month()), d.Day(), hm)
}

// LocalDateStr 本地日期 YYYY-MM-DD。
func LocalDateStr(t time.Time) string {
	return fmt.Sprintf("%d-%02d-%02d", t.Year(), int(t.Month()), t.Day())
}

// HeatmapCell 热力图单元格。
type HeatmapCell struct {
	Week  string // 列标签（周一 M/D）
	Day   string // 行标签（一~日）
	Value int64  // 当日 token
	Label string // 悬停
}

// HeatmapData 热力图网格。
type HeatmapData struct {
	Values    []HeatmapCell
	StartDate string
	EndDate   string
	Weeks     int
}

var dayLabels = []string{"一", "二", "三", "四", "五", "六", "日"}

// HeatmapCells 热力图数据：列=周（周一起始）、行=星期。buckets 稀疏（缺失补 0），
// 固定 weeks 列（默认 14），today 之后不产格子。
func HeatmapCells(buckets []agent.DailyBucket, today string, weeks int) HeatmapData {
	if weeks <= 0 {
		weeks = 14
	}
	todayDay := toEpochDay(today)
	tokensByDay := map[int64]int64{}
	for _, b := range buckets {
		tokensByDay[toEpochDay(b.Date)] = int64(b.Tokens)
	}
	startMonday := mondayOf(todayDay) - int64((weeks-1)*7)

	weekLabel := func(c int) string {
		d := time.Unix((startMonday+int64(c*7))*86400, 0).UTC()
		return fmt.Sprintf("%d/%d", int(d.Month()), d.Day())
	}

	var values []HeatmapCell
	for c := 0; c < weeks; c++ {
		for r := 0; r < 7; r++ {
			day := startMonday + int64(c*7+r)
			if day > todayDay {
				continue
			}
			v := tokensByDay[day]
			d := time.Unix(day*86400, 0).UTC()
			dateStr := fmt.Sprintf("%d月%d日", int(d.Month()), d.Day())
			label := fmt.Sprintf("%s 无用量", dateStr)
			if v > 0 {
				label = fmt.Sprintf("%s 使用了 %s Token", dateStr, FormatTokensZh(v))
			}
			dayLabel := ""
			if r < len(dayLabels) {
				dayLabel = dayLabels[r]
			}
			values = append(values, HeatmapCell{Week: weekLabel(c), Day: dayLabel, Value: v, Label: label})
		}
	}
	return HeatmapData{Values: values, StartDate: fromEpochDay(startMonday), EndDate: today, Weeks: weeks}
}

func toEpochDay(date string) int64 {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0
	}
	return t.Unix() / 86400
}

func fromEpochDay(day int64) string {
	return time.Unix(day*86400, 0).UTC().Format("2006-01-02")
}

func mondayOf(day int64) int64 {
	dow := time.Unix(day*86400, 0).UTC().Weekday() // 0=Sun..6=Sat
	return day - int64((int(dow)+6)%7)
}

// PlanLabel plan_type → 展示名（未知值首字母大写）。
func PlanLabel(plan string) string {
	if plan == "" {
		return ""
	}
	m := map[string]string{
		"free": "Free", "go": "Go", "plus": "Plus", "pro": "Pro", "prolite": "Pro Lite",
		"team": "Team", "business": "Business", "enterprise": "Enterprise",
		"edu": "Edu", "education": "Edu",
	}
	if v, ok := m[plan]; ok {
		return v
	}
	if plan == "" {
		return ""
	}
	return strings.ToUpper(plan[:1]) + plan[1:]
}

// FormatDurationZh 秒 → 「1 小时 15 分 / 42 分」。
func FormatDurationZh(seconds int64) string {
	if seconds < 0 {
		return "—"
	}
	mins := roundInt(seconds / 60)
	if mins < 60 {
		return fmt.Sprintf("%d 分", mins)
	}
	h := mins / 60
	rem := mins % 60
	if rem != 0 {
		return fmt.Sprintf("%d 小时 %d 分", h, rem)
	}
	return fmt.Sprintf("%d 小时", h)
}

// EffortLabelZh 推理强度 → 官方中文口径（usage 活动洞察用；与 command-cards 的 EffortLabel map 区分）。
func EffortLabelZh(effort string) string {
	m := map[string]string{
		"minimal": "极低", "low": "低", "medium": "中", "high": "高", "xhigh": "超高",
	}
	if v, ok := m[effort]; ok {
		return v
	}
	return effort
}

// ShareSectionKey 分享卡可选区块。
type ShareSectionKey string

const (
	ShareStats    ShareSectionKey = "stats"
	ShareHeatmap  ShareSectionKey = "heatmap"
	ShareInsights ShareSectionKey = "insights"
	ShareLimits   ShareSectionKey = "limits"
	SharePlan     ShareSectionKey = "plan"
)

// ShareSections 分享卡区块清单（顺序即卡面顺序）。
var ShareSections = []struct {
	Key   ShareSectionKey
	Label string
}{
	{ShareStats, "核心统计（累计 / 峰值 / 连续天数）"},
	{ShareHeatmap, "每日用量热力图"},
	{ShareInsights, "活动洞察与常用技能"},
	{ShareLimits, "限额进度（5 小时 / 7 天）"},
	{SharePlan, "套餐信息"},
}

// ParseShareSections 多选提交值 → 区块集合。不选 = 全部。
func ParseShareSections(v any) map[ShareSectionKey]bool {
	all := map[ShareSectionKey]bool{}
	for _, s := range ShareSections {
		all[s.Key] = true
	}
	var raw []string
	switch vv := v.(type) {
	case []string:
		raw = vv
	case []any:
		for _, x := range vv {
			raw = append(raw, fmt.Sprintf("%v", x))
		}
	case string:
		if vv != "" {
			raw = strings.Split(vv, ",")
		}
	}
	valid := map[string]bool{}
	for _, s := range ShareSections {
		valid[string(s.Key)] = true
	}
	picked := map[ShareSectionKey]bool{}
	for _, x := range raw {
		x = strings.TrimSpace(x)
		if valid[x] {
			picked[ShareSectionKey(x)] = true
		}
	}
	if len(picked) == 0 {
		return all
	}
	return picked
}

func roundInt(n int64) int64 {
	return n
}

// 排序工具（heatmap 可能用到）。
func sortStrings(ss []string) { sort.Strings(ss) }

var _ = sortStrings

// fallback 空串返回默认值（card 包自洽，不 import utils）。
func fallback(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}
