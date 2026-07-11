package card

// tool_render.go —— 工具调用头/体渲染（对齐 TS card/tool-render）。
// 纯函数，依赖 run-state.ToolEntry。

import (
	"fmt"
	"strings"
)

const (
	toolHeaderTitleMax = 120 // 头部标题上限（放宽到 120，命令仍完整可读，完整命令放 body）
	toolSummaryLineMax = 400 // 批处理「N 个工具调用」摘要行上限（避免脚本被截到看不全）
	toolOutputMax      = 1200
	toolCmdBlockMax    = 1000 // 命令体 ```bash 块上限（放得下长/多行命令，但仍封顶防爆 body）
	toolBodyTotalMax   = 2500
)

// kindGlyph 类型图标：command/file/search 各一个，通用(mcp)/无类型留空（保持 label 工具干净）。
func kindGlyph(kind string) string {
	switch kind {
	case "command":
		return "🔧"
	case "file":
		return "📄"
	case "search":
		return "🔍"
	default:
		return ""
	}
}

func statusIcon(status ToolStatus) string {
	switch status {
	case ToolDone:
		return "✅"
	case ToolError:
		return "❌"
	default:
		return "⏳"
	}
}

// leadGlyphs 状态 + 类型图标（COT 步骤观感）。
func leadGlyphs(tool ToolEntry) string {
	g := kindGlyph(tool.Kind)
	if g != "" {
		return fmt.Sprintf("%s %s", statusIcon(tool.Status), g)
	}
	return statusIcon(tool.Status)
}

// ToolHeaderText 工具面板头：状态 + 类型图标 + 标题（命令行）。截到一行——完整命令在 body。
func ToolHeaderText(tool ToolEntry) string {
	return fmt.Sprintf("%s **%s**", leadGlyphs(tool), escapeInlineTool(truncateRun(tool.Title, toolHeaderTitleMax)))
}

// ToolSummaryLine 批处理「N 个工具调用」单条摘要行：状态 + 类型图标 + 命令/标签（近乎完整，
// 这是批处理模式下命令唯一可见处，故放宽到 SUMMARY_LINE_MAX）。群降级为单摘要面板时用。
func ToolSummaryLine(tool ToolEntry) string {
	return fmt.Sprintf("- %s **%s**", leadGlyphs(tool), escapeInlineTool(truncateRun(tool.Title, toolSummaryLineMax)))
}

// ToolBodyMd 工具面板体：
//   - command 类：先放完整命令 ```bash 块(+ detail)，再放输出 ```bash 块（非零退出标 Error）；
//   - 预围栏输出（fileChange 的 ```diff）原样透传，保高亮 + 闭合围栏；
//   - search 类无输出：给「搜索结果已用于作答」静默提示，不回传。
func ToolBodyMd(tool ToolEntry) string {
	lead := invocationMd(tool)
	var outputPart string
	if tool.Output != "" {
		outputPart = outputBlock(tool)
	} else if tool.Status == ToolRunning {
		outputPart = "_运行中…_"
	} else if tool.Kind == "search" {
		outputPart = "_（搜索结果已用于作答，不单独回传）_"
	}
	body := strings.TrimSpace(strings.Join([]string{lead, outputPart}, "\n\n"))
	if len(body) <= toolBodyTotalMax {
		return body
	}
	return fmt.Sprintf("%s…\n\n_（内容过长，已截断）_", body[:toolBodyTotalMax])
}

// invocationMd command 类的完整命令 ```bash 块(+ detail)；label 类工具返回空（标题已在头部完整显示）。
func invocationMd(tool ToolEntry) string {
	if tool.Kind != "command" {
		return ""
	}
	cmd := strings.TrimSpace(tool.Title)
	if cmd == "" {
		return ""
	}
	detail := ""
	if tool.Detail != "" {
		detail = fmt.Sprintf("\n`%s`", escapeInlineTool(tool.Detail))
	}
	return fmt.Sprintf("**命令**\n```bash\n%s\n```%s", truncateRun(cmd, toolCmdBlockMax), detail)
}

// outputBlock 有输出工具的输出/错误块。
func outputBlock(tool ToolEntry) string {
	label := "Output"
	if tool.Status == ToolError {
		label = "Error"
	}
	block := tool.Output
	if !strings.HasPrefix(tool.Output, "```") {
		block = bashBlock(tool.Output)
	}
	return fmt.Sprintf("**%s**\n%s", label, block)
}

// bashBlock 裸输出包 ```bash 围栏；超 OUTPUT_MAX 截断 + 大小提示。
func bashBlock(output string) string {
	note := ""
	if len(output) > toolOutputMax {
		note = fmt.Sprintf("\n_（已截断，完整输出 %d 字符）_", len(output))
	}
	return "```bash\n" + truncateRun(output, toolOutputMax) + "\n```" + note
}

func truncateRun(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}

// escapeInlineTool 折叠空白（多行命令保持头一行）。
func escapeInlineTool(s string) string {
	return collapseSpaces(strings.TrimSpace(s))
}
