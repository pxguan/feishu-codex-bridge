package codex

// history.go —— codex thread/read 历史折叠（对齐 TS backend.ts 的 mapTurn）。
// 把 codex turn 的 items 折叠为 agent.HistoryTurn（user→assistant + reasoning + tools）。

import (
	"encoding/json"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// ReasoningContent 解析 reasoning 条目的 content（[]string）；解析失败返回 nil。
func (i ThreadItem) ReasoningContent() []string {
	if len(i.Content) == 0 {
		return nil
	}
	var s []string
	if json.Unmarshal(i.Content, &s) != nil {
		return nil
	}
	return s
}

// UserText 解析 userMessage 的 content（[]UserContentPart）→ 拼接文本（text 直连、mention→@name）。
func (i ThreadItem) UserText() string {
	if len(i.Content) == 0 {
		return ""
	}
	var parts []UserContentPart
	if json.Unmarshal(i.Content, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "text":
			sb.WriteString(p.Text)
		case "mention":
			sb.WriteString("@" + p.Name)
		}
	}
	return sb.String()
}

// MapTurn 把一个 codex turn 折叠为 HistoryTurn。
// 跳过 codex 注入的样板（<environment_context> / # AGENTS.md instructions）。
func MapTurn(turn Turn) agent.HistoryTurn {
	var userParts, assistantParts, reasoningParts []string
	var tools []agent.HistoryTool
	for _, item := range turn.Items {
		switch item.Type {
		case "userMessage":
			text := strings.TrimSpace(item.UserText())
			if text != "" && !isBoilerplate(text) {
				userParts = append(userParts, text)
			}
		case "agentMessage":
			if strings.TrimSpace(item.Text) != "" {
				assistantParts = append(assistantParts, item.Text)
			}
		case "reasoning":
			c := item.ReasoningContent()
			if len(c) == 0 {
				c = item.Summary
			}
			if r := strings.TrimSpace(strings.Join(c, "\n")); r != "" {
				reasoningParts = append(reasoningParts, r)
			}
		case "commandExecution":
			exit := item.ExitCode
			failed := item.Status == "failed" || item.Status == "declined" || (exit != nil && *exit != 0)
			tools = append(tools, agent.HistoryTool{Title: item.Command, Output: item.AggregatedOutput, ExitCode: exit, Failed: failed})
		case "fileChange":
			tools = append(tools, agent.HistoryTool{Title: "编辑文件", Failed: item.Status == "failed" || item.Status == "declined"})
		case "webSearch":
			tools = append(tools, agent.HistoryTool{Title: "联网搜索：" + item.Query})
		case "mcpToolCall":
			tools = append(tools, agent.HistoryTool{Title: item.Server + " / " + item.Tool, Failed: item.Status == "failed" || item.Err != ""})
		case "dynamicToolCall":
			tools = append(tools, agent.HistoryTool{Title: item.Tool, Failed: item.Status == "failed" || (item.Success != nil && !*item.Success)})
		}
	}
	return agent.HistoryTurn{
		UserText:      strings.Join(userParts, "\n\n"),
		AssistantText: strings.Join(assistantParts, "\n\n"),
		Reasoning:     strings.Join(reasoningParts, "\n\n"),
		Tools:         tools,
		StartedAt:     turn.StartedAt,
	}
}

// isBoilerplate 跳过 codex 注入的样板（不作为用户消息展示）。
func isBoilerplate(text string) bool {
	t := strings.TrimLeft(text, " \t\n\r")
	return strings.HasPrefix(t, "<environment_context>") || strings.HasPrefix(t, "# AGENTS.md instructions")
}
