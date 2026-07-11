package card

// goal_card.go —— goal run 终态卡（对齐 TS card/goal-card）。
// 成功 → 绿「目标已完成」；异常停止（budget/usage/blocked/timeout/error）→ 橙「目标已中止」+ 原因。

import (
	"fmt"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// GoalDoneCardData goal 终态卡数据。
type GoalDoneCardData struct {
	Objective       string
	Status          string // goal status 或 'timeout'/'error' 哨兵
	TokensUsed      int
	TimeUsedSeconds int64
	ErrorMessage    string
}

// abnormalReason 异常停止原因。
var abnormalReason = map[string]string{
	"budgetLimited": "Token 预算用尽",
	"usageLimited":  "账号用量额度用尽",
	"blocked":       "被阻塞，需人工介入",
	"paused":        "已暂停",
	"timeout":       "运行超过时长上限被中止",
	"error":         "运行出错",
}

// fmtTokens 千分位格式（en-US locale）。
func fmtTokens(n int) string {
	if n < 0 {
		n = 0
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// fmtDuration 「约 7 分 41 秒」格式。
func fmtDuration(seconds int64) string {
	s := seconds
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return fmt.Sprintf("约 %d 秒", s)
	}
	m := s / 60
	rem := s % 60
	if m < 60 {
		if rem != 0 {
			return fmt.Sprintf("约 %d 分 %d 秒", m, rem)
		}
		return fmt.Sprintf("约 %d 分", m)
	}
	h := m / 60
	mm := m % 60
	if mm != 0 {
		return fmt.Sprintf("约 %d 时 %d 分", h, mm)
	}
	return fmt.Sprintf("约 %d 时", h)
}

// BuildGoalDoneCard goal 终态卡。
func BuildGoalDoneCard(d GoalDoneCardData) CardObject {
	ok := agent.IsGoalSuccess(d.Status)
	elements := []CardElement{
		Md(strings.TrimSpace(d.Objective)),
		Hr(),
		Note(fmt.Sprintf("用量　%s tokens", fmtTokens(d.TokensUsed))),
		Note(fmt.Sprintf("耗时　%s", fmtDuration(d.TimeUsedSeconds))),
	}
	if !ok {
		reason := strings.TrimSpace(d.ErrorMessage)
		if reason == "" {
			if r, found := abnormalReason[d.Status]; found {
				reason = r
			} else {
				reason = "状态：" + d.Status
			}
		}
		elements = append(elements, Note("原因　"+reason))
	}
	opts := CardOpts{Summary: "目标已完成"}
	if ok {
		opts.Header = &CardHeader{Title: "🎯 目标已完成", Template: HeaderGreen}
	} else {
		opts.Header = &CardHeader{Title: "🎯 目标已中止", Template: HeaderOrange}
		opts.Summary = "目标已中止"
	}
	return Card(elements, opts)
}
