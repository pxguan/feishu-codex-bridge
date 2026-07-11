package card

// history_card.go —— resume 历史卡（对齐 TS card/history-card）。
// 纯摘要：每轮一个折叠面板（Q 在标题、A 在体、思考/工具更深一层折叠）；18KB 预算从新到老分配。

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// 截断/预算常量（对齐 TS）。
const (
	histUserMax       = 300
	histAssistMax     = 800
	histReasonMax     = 600
	histToolTitleMax  = 90
	histToolsBodyMax  = 700
	histToolsMaxLines = 12
	histPreviewMax    = 160
	histTitleQMax     = 56
	panelShell        = 360
	histBodyBudget    = 18000
)

// HistoryCardState 历史卡状态。
type HistoryCardState struct {
	Cwd         string
	ProjectName string
	History     agent.ThreadHistory
}

// BuildHistoryCard 历史卡（now 用于 relativeTime）。
func BuildHistoryCard(s HistoryCardState, now time.Time) CardObject {
	elements := []CardElement{histMetaNote(s.Cwd, s.ProjectName)}
	if len(s.History.Turns) == 0 {
		elements = append(elements, Hr(), Md("_这个会话还没有可显示的历史（可能是空会话或刚创建）。_"), Hr(), resumedFooter())
		return Card(elements, CardOpts{Header: histHeader(s, now), Summary: "已恢复历史会话"})
	}
	dropped := s.History.TotalTurns - len(s.History.Turns)
	if dropped > 0 {
		elements = append(elements, Note(fmt.Sprintf("仅显示最近 %d 轮，更早的 %d 轮 Codex 仍保留在上下文中。", len(s.History.Turns), dropped)))
	}
	elements = append(elements, Hr())

	// 预算从新到老分配；emit 倒 oldest→newest。
	panels := []CardElement{}
	used := 0
	for i := len(s.History.Turns) - 1; i >= 0; i-- {
		turn := s.History.Turns[i]
		title := turnTitle(turn)
		body := turnBody(turn)
		size := estimateSize(body) + panelShell
		if used+size > histBodyBudget && len(panels) > 0 {
			stub := "_（内容已省略，历史较长）_"
			panels = append(panels, CollapsiblePanel(CollapsiblePanelOpts{Title: title, Expanded: false, Border: "grey", Body: stub}))
			used += len(title) + len(stub) + panelShell
		} else {
			panels = append(panels, CollapsiblePanelEl(title, false, "grey", body))
			used += size
		}
	}
	// reverse
	for i, j := 0, len(panels)-1; i < j; i, j = i+1, j-1 {
		panels[i], panels[j] = panels[j], panels[i]
	}
	elements = append(elements, panels...)

	last := s.History.Turns[len(s.History.Turns)-1]
	leftOff := ""
	if last.AssistantText != "" {
		leftOff = last.AssistantText
	} else if last.UserText != "" {
		leftOff = last.UserText
	} else if len(last.Tools) > 0 {
		leftOff = last.Tools[len(last.Tools)-1].Title
	}
	if strings.TrimSpace(leftOff) != "" {
		elements = append(elements, Hr(), NoteMd(fmt.Sprintf("📍 **上次停在**：%s", truncateTail(leftOff, histPreviewMax))))
	} else {
		elements = append(elements, Hr())
	}
	elements = append(elements, resumedFooter())
	return Card(elements, CardOpts{Header: histHeader(s, now), Summary: fmt.Sprintf("已恢复历史会话 · 共 %d 轮", s.History.TotalTurns)})
}

func histHeader(s HistoryCardState, now time.Time) *CardHeader {
	bits := []string{}
	name := strings.TrimSpace(s.History.Name)
	if name == "" {
		name = s.ProjectName
	}
	if name != "" {
		bits = append(bits, name)
	}
	bits = append(bits, fmt.Sprintf("共 %d 轮", s.History.TotalTurns))
	if s.History.UpdatedAt != 0 {
		bits = append(bits, RelativeTime(s.History.UpdatedAt, now))
	}
	return &CardHeader{Title: "🕘 已恢复历史会话", Template: HeaderTurquoise, Subtitle: strings.Join(bits, " · ")}
}

func histMetaNote(cwd, projectName string) CardElement {
	parts := []string{"📂 `" + cwd + "`"}
	if projectName != "" {
		return Note("📁 " + projectName + "   " + parts[0])
	}
	return Note(parts[0])
}

func resumedFooter() CardElement {
	return Md("✅ **会话已恢复** —— 直接发消息即可继续。")
}

func turnTitle(turn agent.HistoryTurn) string {
	if strings.TrimSpace(turn.UserText) != "" {
		return "👤 " + escapeInline(truncate(oneLine(turn.UserText), histTitleQMax))
	}
	return "⚙️ 系统 / 工具调用"
}

func turnBody(turn agent.HistoryTurn) []CardElement {
	var out []CardElement
	if strings.TrimSpace(turn.UserText) != "" {
		out = append(out, Md(fmt.Sprintf("**👤 你**\n%s", truncate(turn.UserText, histUserMax))))
	}
	if strings.TrimSpace(turn.AssistantText) != "" {
		out = append(out, Md(fmt.Sprintf("**🤖 Codex**\n%s", truncate(turn.AssistantText, histAssistMax))))
	}
	if strings.TrimSpace(turn.AssistantText) == "" && strings.TrimSpace(turn.UserText) == "" && len(turn.Tools) > 0 {
		out = append(out, NoteMd("_（仅工具调用，无文本回复）_"))
	}
	var detail []CardElement
	if strings.TrimSpace(turn.Reasoning) != "" {
		detail = append(detail, Md(fmt.Sprintf("🧠 **思考**\n%s", truncate(turn.Reasoning, histReasonMax))))
	}
	if len(turn.Tools) > 0 {
		detail = append(detail, Md(toolsBlock(turn.Tools)))
	}
	if len(detail) > 0 {
		out = append(out, CollapsiblePanelEl(detailTitle(turn), false, "blue", detail))
	}
	return out
}

func detailTitle(turn agent.HistoryTurn) string {
	parts := []string{}
	if strings.TrimSpace(turn.Reasoning) != "" {
		parts = append(parts, "🧠 思考")
	}
	if len(turn.Tools) > 0 {
		parts = append(parts, fmt.Sprintf("🧰 %d 个工具", len(turn.Tools)))
	}
	return "🔎 " + strings.Join(parts, " · ")
}

func toolsBlock(tools []agent.HistoryTool) string {
	lines := []string{fmt.Sprintf("🧰 **工具调用（%d）**", len(tools))}
	body := 0
	shown := 0
	for _, t := range tools {
		if shown >= histToolsMaxLines || body >= histToolsBodyMax {
			lines = append(lines, fmt.Sprintf("_…还有 %d 个_", len(tools)-shown))
			break
		}
		line := toolLine(t)
		lines = append(lines, line)
		body += len(line)
		shown++
	}
	return strings.Join(lines, "\n")
}

func toolLine(t agent.HistoryTool) string {
	title := escapeInline(truncate(oneLine(t.Title), histToolTitleMax))
	mark := ""
	if t.Failed {
		mark = " ✗"
	}
	exit := ""
	if t.ExitCode != nil && *t.ExitCode != 0 {
		exit = fmt.Sprintf(" (exit %d)", *t.ExitCode)
	}
	return fmt.Sprintf("- `%s`%s%s", title, mark, exit)
}

func estimateSize(els []CardElement) int {
	n := 0
	for _, el := range els {
		b, _ := json.Marshal(el)
		n += len(b)
	}
	return n
}

func oneLine(s string) string { return collapseSpaces(strings.TrimSpace(s)) }

func escapeInline(s string) string { return strings.ReplaceAll(s, "`", "") }

func truncateTail(s string, n int) string {
	t := oneLine(s)
	runes := []rune(t)
	if len(runes) > n {
		return "…" + string(runes[len(runes)-n:])
	}
	return t
}
