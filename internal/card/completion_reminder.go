package card

// completion_reminder.go —— 普通群任务结束提醒的回复正文构造（对齐 TS card/completion-reminder.ts）。
// 与 TS 用 post + at 节点不同：Go 侧经 Channel.ReplyMarkdown 发 markdown 卡片（含 <at> 提及），
// 走 CardKit 实体路径——既触达飞书真实 @ 通知，又规避 post content 格式坑。

import (
	"fmt"
	"strings"
)

// CompletionReminderPost 一条普通群任务终态的提醒输入。
// Outcome 取 config.CompletionReminderOutcome 的 done/error/idle_timeout 子集。
type CompletionReminderPost struct {
	RequesterOpenID string
	Outcome         string // done | error | idle_timeout
	ElapsedMs       int64
	// Summary 用户自述的简短任务摘要；空/空白回退「本轮任务」。
	Summary string
	// CardUpdated=false 时终态卡片更新失败（重试耗尽），提示用户去查流式内容。
	CardUpdated bool
}

// atMention 用 markdown 的 <at> 标签提及发起人（open_id）。
func atMention(openID string) string {
	if openID == "" {
		return ""
	}
	return fmt.Sprintf("<at user_id=\"%s\">@用户</at>", openID)
}

// BuildCompletionReminderContent 构造普通群任务结束提醒的回复正文（markdown）。
// 结构：@发起人 + 标题行（耗时/成败）+ 详情行（结果在上方卡片 / 卡片失败提示）。
func BuildCompletionReminderContent(input CompletionReminderPost) string {
	task := CompactSummary(input.Summary)
	elapsed := FormatCompletionElapsed(input.ElapsedMs)
	var headline string
	switch input.Outcome {
	case "done":
		headline = fmt.Sprintf("✅「%s」已完成 · 用时 %s", task, elapsed)
	case "idle_timeout":
		headline = fmt.Sprintf("⏱「%s」响应超时 · 等待 %s", task, elapsed)
	default:
		headline = fmt.Sprintf("⚠️「%s」执行失败 · 用时 %s", task, elapsed)
	}
	detail := "结果在上方卡片。"
	if input.Outcome != "done" {
		detail = "详情在上方卡片。"
	}
	if !input.CardUpdated {
		detail = "最终卡片更新失败，请查看上方流式内容或重新发起任务。"
	}
	return fmt.Sprintf("%s\n\n%s", atMention(input.RequesterOpenID)+headline, detail)
}

// FormatCompletionElapsed 把毫秒数格式化成中文可读耗时（向下取整到整秒）。
func FormatCompletionElapsed(elapsedMs int64) string {
	total := int64(0)
	if elapsedMs > 0 {
		total = elapsedMs / 1000
	}
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d 小时", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d 分", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d 秒", seconds))
	}
	return strings.Join(parts, " ")
}

// CompactSummary 任务摘要压缩：合并空白、截断到 32 字符（含 …）。空回退「本轮任务」。
func CompactSummary(s string) string {
	clean := strings.Join(strings.Fields(s), " ")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return "本轮任务"
	}
	runes := []rune(clean)
	if len(runes) > 32 {
		return string(runes[:31]) + "…"
	}
	return clean
}
