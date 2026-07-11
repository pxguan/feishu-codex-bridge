package bot

// completion_reminder.go —— 普通群任务结束提醒的发送（对齐 TS bot/completion-reminder.ts）。
// 在运行卡终态落地后触发：按四档策略判定是否发独立 @ 回复。best-effort：运行态已结算，
// 只补一条通知，绝不改写任务终态。

import (
	"context"
	"fmt"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/feishu"
)

// CompletionReminderReplyInput 一次普通任务终态的提醒输入。
type CompletionReminderReplyInput struct {
	CardMsgID       string
	RequesterOpenID string
	Outcome         config.CompletionReminderOutcome
	RequestedAt     int64 // 发起轮的消息 createTime（含排队/会话等待，用于 long 策略）
	ManuallyRequested bool
	Summary         string
	CardUpdated     bool
	ReplyInThread   bool
}

// CompletionReminderReplyResult 提醒发送结果。
type CompletionReminderReplyResult string

const (
	ReminderSent    CompletionReminderReplyResult = "sent"
	ReminderSkipped CompletionReminderReplyResult = "skipped"
	ReminderFailed  CompletionReminderReplyResult = "failed"
)

// sendCompletionReminder 在运行卡终态落地后发独立 @ 回复（best-effort）。
// 显式产品边界：用户中断 / 排队取消永不通知；只有普通成功、agent 错误、watchdog 超时才通知。
// 终态卡片更新失败无论策略如何都兜底通知（否则用户永远盯着一张"运行中"的卡）。
func (o *Orchestrator) sendCompletionReminder(ctx context.Context, input CompletionReminderReplyInput) CompletionReminderReplyResult {
	if input.RequesterOpenID == "" {
		return ReminderSkipped
	}
	if input.Outcome != config.ReminderDone && input.Outcome != config.ReminderError && input.Outcome != config.ReminderIdleTimeout {
		return ReminderSkipped
	}
	var elapsedMs int64
	if input.RequestedAt > 0 {
		elapsedMs = time.Now().UnixMilli() - input.RequestedAt
		if elapsedMs < 0 {
			elapsedMs = 0
		}
	}
	policyMatch := config.ShouldSendCompletionReminder(o.Cfg, config.CompletionReminderDecision{
		Outcome:           input.Outcome,
		ElapsedMs:         elapsedMs,
		ManuallyRequested: input.ManuallyRequested,
	})
	// 终态卡片已成功更新 + 策略不匹配 → 跳过（无需重复通知）。
	if input.CardUpdated && !policyMatch {
		return ReminderSkipped
	}
	// 同一张卡只发一次（防重入 / 多次回调）。
	dedupeKey := input.CardMsgID + ":" + input.RequesterOpenID
	if _, seen := o.reminderSeen.LoadOrStore(dedupeKey, true); seen {
		return ReminderSkipped
	}
	content := card.BuildCompletionReminderContent(card.CompletionReminderPost{
		RequesterOpenID: input.RequesterOpenID,
		Outcome:         string(input.Outcome),
		ElapsedMs:       elapsedMs,
		Summary:         input.Summary,
		CardUpdated:     input.CardUpdated,
	})
	ch, ok := o.Channel.(*feishu.Channel)
	if !ok {
		core.Warn(ctx, "bot", "reminder-no-channel", "Channel 未实现 feishu.Channel，无法发送完成提醒")
		return ReminderFailed
	}
	if _, err := ch.ReplyMarkdown(ctx, input.CardMsgID, content, input.ReplyInThread); err != nil {
		core.Fail(ctx, "bot", "reminder-failed", err)
		return ReminderFailed
	}
	core.Info(ctx, "bot", "reminder-sent", "完成提醒已发: "+string(input.Outcome))
	return ReminderSent
}

// SetCompletionReminder 设置普通群任务结束提醒策略（bot 级偏好，落 config.json 并热更新 LIVE cfg）。
// 与 TS performSetCompletionReminder 同语义：先校验，再原子落盘；落盘失败运行态仍保留旧值。
func (o *Orchestrator) SetCompletionReminder(mode config.CompletionReminderMode, longTaskMinutes int) error {
	switch mode {
	case config.ReminderManual, config.ReminderLong, config.ReminderFailures, config.ReminderAlways:
	default:
		return fmt.Errorf("未知完成提醒策略「%s」", mode)
	}
	// longTaskMinutes 省略（仅改 mode）→ 沿用当前阈值；显式传入则校验范围。
	cur := config.GetCompletionReminderConfig(o.Cfg)
	minutes := cur.LongTaskMinutes
	if longTaskMinutes != 0 {
		if longTaskMinutes < config.CompletionReminderLongTaskMinMinutes ||
			longTaskMinutes > config.CompletionReminderLongTaskMaxMinutes {
			return fmt.Errorf("长任务阈值必须是 %d–%d 分钟之间的整数",
				config.CompletionReminderLongTaskMinMinutes, config.CompletionReminderLongTaskMaxMinutes)
		}
		minutes = longTaskMinutes
	}
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	o.Cfg.Preferences.CompletionReminder = &config.CompletionReminderConfig{
		Mode:            mode,
		LongTaskMinutes: &minutes,
	}
	if err := o.saveConfig(); err != nil {
		return err
	}
	return nil
}
