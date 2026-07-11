package claude

// eventmap.go —— claude stream-json 消息 → 归一化 AgentEvent（纯函数，港口 TS event-map.ts）。
//
// 与 codex 的无状态 mapNotification 不同，Claude 的 SDK 流需要一点 per-turn 状态：
// 它把 token 级 stream_event 增量（需 includePartialMessages）与完整的 assistant/user
// 消息交错；text/thinking 内容块没有稳定 id。于是我们为每个内容块自造 itemId，并用流的
// index 关联「块开始 → 增量 → 结束」。工具调用带稳定 id，直接从完整消息映射。
//
// 观测到的消息顺序（对齐 TS 注释）：
//   system/init → [每轮 API 消息] message_start → content_block_start(idx) →
//     content_block_delta(idx)* → [该块自身的完整 assistant 消息] → content_block_stop(idx)
//   → … → [携带 tool_result 的 user 消息] → … → result
// 每个内容块作为它【自己的】assistant 消息出现（每块一个）。
//
// 每块的事件产出：
//   text     → text_delta(itemId)*（增量）然后 text(itemId)（结束）
//   thinking → thinking_delta(itemId)* 然后 thinking(itemId)
//   tool_use → tool_use(itemId = block.id)（来自 assistant 消息）
//   tool_result（user 消息）→ tool_result(itemId = tool_use_id)
// turn 的 turn_started/done 由 thread 层合成（SDK 无 turn id）；本 mapper 产出其余一切，
// 并在 result 时产出 usage + context_usage（thread 追加上 done）。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// ── 原始消息解析 ──────────────────────────────────────────────

type claudeMessage struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Model     string          `json:"model"`
	Message   json.RawMessage `json:"message"` // assistant/user
	Event     json.RawMessage `json:"event"`   // stream_event
	Usage     json.RawMessage `json:"usage"`   // result
	Result    string          `json:"result"`  // result.subtype 的附带文本
}

func parseClaudeMessage(raw json.RawMessage) claudeMessage {
	var m claudeMessage
	_ = json.Unmarshal(raw, &m)
	return m
}

// ── 内容块 / 增量形状 ───────────────────────────────────────────

type contentBlock struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Input      map[string]any `json:"input"`
	Text       string         `json:"text"`
	Thinking   string         `json:"thinking"`
	ToolUseID  string         `json:"tool_use_id"`
	IsError    bool           `json:"is_error"`
	Content    json.RawMessage `json:"content"`
}

type contentDelta struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

// ── per-turn mapper ──────────────────────────────────────────

type openBlock struct {
	itemID string
	kind   string // text | thinking | tool_use | other
	acc    string
}

// turnMapper 一个 runStreamed/runGoal turn 一个实例（状态随 turn 重置）。
type turnMapper struct {
	blockSeq      int
	open          map[int]*openBlock
	systemEmitted bool
	initModel     string
	sawPartial    bool // 本 turn 是否见过 stream_event 增量（用于 text/thinking 兜底）
	cwd           string
}

func createTurnMapper(cwd string) *turnMapper {
	return &turnMapper{open: map[int]*openBlock{}, cwd: cwd}
}

// mapMsg 把一条原始消息映射为零到多个 AgentEvent（按顺序）。
func (m *turnMapper) mapMsg(msg claudeMessage) []agent.AgentEvent {
	switch msg.Type {
	case "system":
		if msg.Subtype == "init" {
			if msg.Model != "" {
				m.initModel = msg.Model
			}
			if !m.systemEmitted {
				m.systemEmitted = true
				return []agent.AgentEvent{agent.EvSys(msg.SessionID)}
			}
		}
		// turn 内自动压缩 → 弹出「上下文已压缩」提示。
		if msg.Subtype == "compact_boundary" {
			return []agent.AgentEvent{agent.EvCompacted()}
		}
		return nil

	case "stream_event":
		return m.mapStreamEvent(msg.Event)

	case "assistant":
		return m.mapAssistant(msg.Message)

	case "user":
		return m.mapUser(msg.Message)

	case "result":
		return m.mapResult(msg)

	case "system_api_retry", "api_retry":
		// 瞬态 API 抖动（SDK 自动重试）→ 弹出「重试中」脚注。
		return []agent.AgentEvent{agent.EvErrorT("网络波动，正在重试…", true)}
	}
	return nil
}

func (m *turnMapper) mapStreamEvent(event json.RawMessage) []agent.AgentEvent {
	if len(event) == 0 {
		return nil
	}
	var ev struct {
		Type         string       `json:"type"`
		Index        int          `json:"index"`
		ContentBlock contentBlock `json:"content_block"`
		Delta        contentDelta `json:"delta"`
	}
	if json.Unmarshal(event, &ev) != nil {
		return nil
	}
	switch ev.Type {
	case "content_block_start":
		m.sawPartial = true
		kind := "other"
		switch ev.ContentBlock.Type {
		case "thinking", "redacted_thinking":
			kind = "thinking"
		case "text":
			kind = "text"
		case "tool_use", "server_tool_use":
			kind = "tool_use"
		}
		m.blockSeq++
		m.open[ev.Index] = &openBlock{itemID: fmt.Sprintf("b%d", m.blockSeq), kind: kind}
		return nil

	case "content_block_delta":
		b := m.open[ev.Index]
		if b == nil {
			return nil
		}
		if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
			b.acc += ev.Delta.Text
			return []agent.AgentEvent{agent.EvTextD(b.itemID, ev.Delta.Text)}
		}
		if ev.Delta.Type == "thinking_delta" && ev.Delta.Thinking != "" {
			b.acc += ev.Delta.Thinking
			return []agent.AgentEvent{agent.EvThinkingD(b.itemID, ev.Delta.Thinking)}
		}
		return nil

	case "content_block_stop":
		b := m.open[ev.Index]
		delete(m.open, ev.Index)
		if b == nil || b.acc == "" {
			return nil
		}
		if b.kind == "text" {
			return []agent.AgentEvent{agent.EvTextFull(b.itemID, b.acc)}
		}
		if b.kind == "thinking" {
			return []agent.AgentEvent{agent.EvThinkingFull(b.itemID, b.acc)}
		}
		return nil
	}
	return nil
}

func (m *turnMapper) mapAssistant(message json.RawMessage) []agent.AgentEvent {
	if len(message) == 0 {
		return nil
	}
	var msg struct {
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(message, &msg) != nil {
		return nil
	}
	out := []agent.AgentEvent{}
	for _, b := range msg.Content {
		if b.Type == "tool_use" || b.Type == "server_tool_use" {
			out = append(out, agent.EvToolUK(b.ID, toolTitle(b.Name, b.Input, m.cwd), toolDetail(b.Name, b.Input), toolKind(b.Name)))
			continue
		}
		// text/thinking：仅当本 turn 没见过 stream_event 增量（sawPartial=false）
		// 时才从完整 assistant 消息补发——增量模式下它们已由 content_block_stop 发出。
		if m.sawPartial {
			continue
		}
		if b.Type == "text" && b.Text != "" {
			m.blockSeq++
			out = append(out, agent.EvTextFull(fmt.Sprintf("b%d", m.blockSeq), b.Text))
		} else if (b.Type == "thinking" || b.Type == "redacted_thinking") && b.Thinking != "" {
			m.blockSeq++
			out = append(out, agent.EvThinkingFull(fmt.Sprintf("b%d", m.blockSeq), b.Thinking))
		}
	}
	return out
}

func (m *turnMapper) mapUser(message json.RawMessage) []agent.AgentEvent {
	if len(message) == 0 {
		return nil
	}
	var msg struct {
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(message, &msg) != nil {
		return nil
	}
	out := []agent.AgentEvent{}
	for _, b := range msg.Content {
		if b.Type == "tool_result" {
			ec := 0
			if b.IsError {
				ec = 1
			}
			out = append(out, agent.EvToolR(b.ToolUseID, toolResultText(b.Content), &ec))
		}
	}
	return out
}

func (m *turnMapper) mapResult(msg claudeMessage) []agent.AgentEvent {
	out := []agent.AgentEvent{}
	if len(msg.Usage) == 0 {
		return out
	}
	var u struct {
		InputTokens    int `json:"input_tokens"`
		OutputTokens   int `json:"output_tokens"`
		CacheRead      int `json:"cache_read_input_tokens"`
		CacheCreation  int `json:"cache_creation_input_tokens"`
	}
	if json.Unmarshal(msg.Usage, &u) != nil {
		return out
	}
	out = append(out, agent.EvUsageT(u.InputTokens, u.OutputTokens))
	// 兜底上下文量（thread 用权威读数覆盖）；used = input + cache_read + cache_creation。
	used := u.InputTokens + u.CacheRead + u.CacheCreation
	if used > 0 {
		out = append(out, agent.EvContext(used, contextWindowFor(m.initModel)))
	}
	return out
}

// ── 工具渲染助手 ──────────────────────────────────────────────

// toolTitle 把 Claude 内置工具调用渲染成简短中文标题（对齐 codex 的「命令即标题」约定）。
func toolTitle(name string, input map[string]any, cwd string) string {
	s := func(k string) string {
		if v, ok := input[k]; ok {
			if str, ok := v.(string); ok {
				return str
			}
		}
		return ""
	}
	switch name {
	case "Bash", "BashOutput":
		return orDefault(s("command"), "Shell 命令")
	case "Read":
		return "读取 " + displayPath(s("file_path"), cwd)
	case "Write":
		return "写入 " + displayPath(s("file_path"), cwd)
	case "Edit", "MultiEdit":
		return "编辑 " + displayPath(s("file_path"), cwd)
	case "NotebookEdit":
		return "编辑笔记本 " + displayPath(s("notebook_path"), cwd)
	case "Glob":
		return "查找 " + s("pattern")
	case "Grep":
		return "搜索 " + s("pattern")
	case "WebFetch":
		return "抓取网页 " + s("url")
	case "WebSearch":
		return "联网搜索 " + s("query")
	case "Task":
		return "子任务：" + orDefault(s("description"), s("subagent_type"))
	case "TodoWrite":
		return "更新待办清单"
	case "ExitPlanMode":
		return "提交方案"
	default:
		return orDefault(name, "工具调用")
	}
}

// toolDetail 工具块的副标题（如 Bash 命令的 description）。
func toolDetail(name string, input map[string]any) string {
	if name == "Bash" {
		if d, ok := input["description"].(string); ok && d != "" {
			return d
		}
	}
	return ""
}

// toolKind 工具的粗分类（对齐 TS event-map.ts toolKind），供卡片按类别渲染：
// command=Bash 类（标题即完整命令，渲染为 bash 代码块）；file=文件读/写/编辑；search=grep/glob/web；
// 其余 → tool。与 toolTitle 的 switch 保持一致。
func toolKind(name string) agent.ToolKind {
	switch name {
	case "Bash", "BashOutput":
		return agent.ToolKindCommand
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		return agent.ToolKindFile
	case "Glob", "Grep", "WebFetch", "WebSearch":
		return agent.ToolKindSearch
	default:
		return agent.ToolKindTool
	}
}

// toolResultText tool_result.content 是 string 或 {type:'text',text} 数组（含图片）→ 纯文本。
func toolResultText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var raw any
	if json.Unmarshal(content, &raw) != nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var sb strings.Builder
		for _, b := range v {
			obj, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if obj["type"] == "text" {
				if t, ok := obj["text"].(string); ok {
					sb.WriteString(t)
				}
			} else if obj["type"] == "image" {
				sb.WriteString("[图片]")
			}
		}
		return strings.TrimSpace(sb.String())
	}
	return ""
}

const pathTailMax = 40

// displayPath cwd 内相对化；cwd 外保留绝对（让越项目改动可见）；无 cwd 时长路径只留尾部段。
func displayPath(p, cwd string) string {
	if p == "" {
		return "文件"
	}
	if cwd != "" {
		sep := "/"
		if strings.Contains(cwd, "\\") {
			sep = "\\"
		}
		root := cwd
		if !strings.HasSuffix(root, sep) {
			root += sep
		}
		if strings.HasPrefix(p, root) && len(p) > len(root) {
			return p[len(root):]
		}
		return p
	}
	if len(p) <= pathTailMax || !strings.Contains(p, "/") {
		return p
	}
	segs := strings.Split(p, "/")
	out := segs[len(segs)-1]
	for i := len(segs) - 2; i >= 0; i-- {
		cand := segs[i] + "/" + out
		if len(cand) > pathTailMax {
			break
		}
		out = cand
	}
	return "…/" + out
}

// contextWindowFor 模型上下文窗口启发式——result 消息不报窗口。
// [1m] 后缀 / 1m 上下文模型 → 1,000,000；否则（含空模型）200k 默认。仅驱动仪表盘百分比。
func contextWindowFor(model string) *int {
	id := strings.ToLower(model)
	var w int
	if strings.Contains(id, "1m") || strings.Contains(id, "[1m]") {
		w = 1_000_000
	} else {
		w = 200_000
	}
	return &w
}

// resultErrorText 非成功 result 消息的人类可读错误（turn 失败时 thread 使用）。
func resultErrorText(m claudeMessage) string {
	if strings.TrimSpace(m.Result) != "" {
		return strings.TrimSpace(m.Result)
	}
	switch m.Subtype {
	case "error_max_turns":
		return "已达到最大轮次限制"
	case "error_max_budget_usd":
		return "已达到预算上限"
	}
	if m.Subtype != "" {
		return "运行出错（" + m.Subtype + "）"
	}
	return "运行出错"
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return def
}
