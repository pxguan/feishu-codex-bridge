package card

// run_state.go —— 运行卡的结构化状态 + AgentEvent 归约（对齐 TS card/run-state）。
// 纯函数：reduce(state, evt) → 新 state。卡片渲染（buildRunCard，飞书 SDK）消费它。

import (
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// ToolStatus 工具状态。
type ToolStatus string

const (
	ToolRunning ToolStatus = "running"
	ToolDone    ToolStatus = "done"
	ToolError   ToolStatus = "error"
)

// ToolEntry 一次工具调用（commandExecution/fileChange/webSearch/...）。
type ToolEntry struct {
	ID       string
	Title    string
	Detail   string
	Kind     string // ToolKind：command|file|search|tool（驱动卡片分类渲染）
	Status   ToolStatus
	Output   string
	ExitCode *int
}

// Block 一个渲染块（text 或 tool，按到达顺序交错）。
type Block struct {
	Kind      string    // "text" | "tool"
	ID        string    // text 的 itemId
	Content   string    // text 内容
	Streaming bool      // text 是否仍在流式（item/completed 后 false）
	Tool      ToolEntry // kind=="tool" 时有效
}

// ReasoningItem 一段推理（codex 一轮可能 emit 多段）。
type ReasoningItem struct {
	ID   string
	Text string
}

// FooterStatus 底部状态指示。
type FooterStatus string

const (
	FooterThinking    FooterStatus = "thinking"
	FooterToolRunning FooterStatus = "tool_running"
	FooterStreaming   FooterStatus = "streaming"
	FooterRetrying    FooterStatus = "retrying"
)

// Terminal 终态。
type Terminal string

const (
	TermRunning     Terminal = "running"
	TermDone        Terminal = "done"
	TermInterrupted Terminal = "interrupted"
	TermError       Terminal = "error"
	TermIdleTimeout Terminal = "idle_timeout"
)

// CtxUsage 上下文占用（context_usage 事件）。
type CtxUsage struct {
	Used   int
	Window *int // nil=codex 未上报窗口
}

// RunState 运行卡状态。
type RunState struct {
	Blocks             []Block
	Reasoning          []ReasoningItem
	ReasoningActive    bool
	Footer             FooterStatus
	Terminal           Terminal
	ErrorMsg           string
	IdleTimeoutSeconds int
	Usage              *CtxUsage
}

// InitialState 初始状态。
func InitialState() RunState {
	return RunState{Footer: FooterThinking, Terminal: TermRunning}
}

// ReasoningContent 拼接全部推理文本（非空）。
func ReasoningContent(s RunState) string {
	parts := []string{}
	for _, r := range s.Reasoning {
		if strings.TrimSpace(r.Text) != "" {
			parts = append(parts, r.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// FinalMessageText 最后一条非空 text block（agent 的最终答案）。
func FinalMessageText(s RunState) string {
	for i := len(s.Blocks) - 1; i >= 0; i-- {
		if s.Blocks[i].Kind == "text" && strings.TrimSpace(s.Blocks[i].Content) != "" {
			return strings.TrimSpace(s.Blocks[i].Content)
		}
	}
	return ""
}

// Reduce 把一个 AgentEvent 折进 state（纯函数，返回新 state）。
func Reduce(state RunState, evt agent.AgentEvent) RunState {
	switch evt.Type {
	case agent.EvTextDelta:
		state.Blocks = upsertText(state.Blocks, evt.ItemID, func(prev string) string { return prev + evt.Delta })
		state.ReasoningActive = false
		state.Footer = FooterStreaming
	case agent.EvText:
		state.Blocks = replaceText(state.Blocks, evt.ItemID, evt.Text)
		state.ReasoningActive = false
	case agent.EvThinkingDelta:
		state.Reasoning = upsertReasoning(state.Reasoning, evt.ItemID, func(prev string) string { return prev + evt.Delta })
		state.ReasoningActive = true
		if state.Footer != FooterStreaming {
			state.Footer = FooterThinking
		}
	case agent.EvThinking:
		state.Reasoning = upsertReasoning(state.Reasoning, evt.ItemID, func(string) string { return evt.Text })
	case agent.EvToolUse:
		state.Blocks = append(closeStreamingText(state.Blocks), Block{Kind: "tool", Tool: ToolEntry{
			ID: evt.ItemID, Title: evt.Title, Detail: evt.Detail, Kind: evt.Kind, Status: ToolRunning,
		}})
		state.ReasoningActive = false
		state.Footer = FooterToolRunning
	case agent.EvToolResult:
		isErr := evt.ExitCode != nil && *evt.ExitCode != 0
		status := ToolDone
		if isErr {
			status = ToolError
		}
		for i := range state.Blocks {
			if state.Blocks[i].Kind == "tool" && state.Blocks[i].Tool.ID == evt.ItemID {
				state.Blocks[i].Tool.Status = status
				state.Blocks[i].Tool.Output = evt.Output
				state.Blocks[i].Tool.ExitCode = evt.ExitCode
			}
		}
	case agent.EvContextUsage:
		w := evt.ContextWindow
		state.Usage = &CtxUsage{Used: evt.UsedTokens, Window: w}
	case agent.EvError:
		if evt.WillRetry {
			// codex 瞬断会自重试——NOT terminal（翻 error 会闪假失败 + 丢 ⏹）。
			state.Footer = FooterRetrying
		} else {
			state.Terminal = TermError
			state.ErrorMsg = evt.Message
			state.Footer = ""
		}
	case agent.EvDone:
		state.Blocks = closeStreamingText(state.Blocks)
		state.ReasoningActive = false
		state.Terminal = TermDone
		state.Footer = ""
	}
	return state
}

// MarkInterrupted 标记 ⏹ 中断终态。
func MarkInterrupted(state RunState) RunState {
	state.Blocks = closeStreamingText(state.Blocks)
	state.ReasoningActive = false
	state.Terminal = TermInterrupted
	state.Footer = ""
	return state
}

// MarkIdleTimeout 标记 idle watchdog 超时终态。
func MarkIdleTimeout(state RunState, seconds int) RunState {
	state.Blocks = closeStreamingText(state.Blocks)
	state.ReasoningActive = false
	state.Terminal = TermIdleTimeout
	state.Footer = ""
	state.IdleTimeoutSeconds = seconds
	return state
}

// FinalizeIfRunning 若仍在 running，收尾为 done（流意外结束时兜底）。
func FinalizeIfRunning(state RunState) RunState {
	if state.Terminal != TermRunning {
		return state
	}
	state.Blocks = closeStreamingText(state.Blocks)
	state.ReasoningActive = false
	state.Terminal = TermDone
	state.Footer = ""
	return state
}

// ── 辅助（纯函数：复制 slice 后修改）─────────────────────────────

func closeStreamingText(blocks []Block) []Block {
	out := make([]Block, len(blocks))
	for i, b := range blocks {
		if b.Kind == "text" && b.Streaming {
			b.Streaming = false
		}
		out[i] = b
	}
	return out
}

func upsertText(blocks []Block, id string, mutate func(string) string) []Block {
	for i, b := range blocks {
		if b.Kind == "text" && b.ID == id {
			out := make([]Block, len(blocks))
			copy(out, blocks)
			out[i].Content = mutate(out[i].Content)
			return out
		}
	}
	out := make([]Block, 0, len(blocks)+1)
	out = append(out, blocks...)
	out = append(out, Block{Kind: "text", ID: id, Content: mutate(""), Streaming: true})
	return out
}

func replaceText(blocks []Block, id, content string) []Block {
	for i, b := range blocks {
		if b.Kind == "text" && b.ID == id {
			out := make([]Block, len(blocks))
			copy(out, blocks)
			out[i].Content = content
			out[i].Streaming = false
			return out
		}
	}
	out := make([]Block, 0, len(blocks)+1)
	out = append(out, blocks...)
	out = append(out, Block{Kind: "text", ID: id, Content: content, Streaming: false})
	return out
}

func upsertReasoning(items []ReasoningItem, id string, mutate func(string) string) []ReasoningItem {
	for i, r := range items {
		if r.ID == id {
			out := make([]ReasoningItem, len(items))
			copy(out, items)
			out[i].Text = mutate(out[i].Text)
			return out
		}
	}
	out := make([]ReasoningItem, 0, len(items)+1)
	out = append(out, items...)
	out = append(out, ReasoningItem{ID: id, Text: mutate("")})
	return out
}
