package card

// run_card.go —— 运行卡 + 排队卡构造（对齐 TS card/run-card）。
// 把 RunState 渲染成飞书卡：RUNNING 双布局（推理/工具/答案流式 + ⏹ 底部）/ TERMINAL（过程折叠 + 最终答案）。
// 依赖 tool-render + run-state + element builder + markdown-render + context-gauge，全部就绪。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// 稳定 element_id（typewriter / 孤儿卡自愈用）。
const (
	AnswerEID   = "answer"
	ControlsEID = "controls"
)

const (
	runReasoningMax       = 1500
	collapseToolThreshold = 3
	processBodyBudget     = 22000
)

// RunCardState 运行卡渲染输入。
type RunCardState struct {
	RS              RunState
	CardKey         string // ⏹ 路由 key（卡 messageId）
	ThreadID        string
	RequesterOpenID string
	ShowTools       *bool // nil=默认 true
	Model           string
	Effort          agent.ReasoningEffort
	ModelOnTerminal bool
	HideStop        bool
	GoalControls    bool
	GoalEnding      bool
	Images          map[string]string // 终态答案图片 src→imgKey
}

// BuildRunCard 渲染运行卡。
func BuildRunCard(rc RunCardState) CardObject {
	state := rc.RS
	running := state.Terminal == TermRunning
	var elements []CardElement
	if running {
		elements = renderRunning(state, rc)
	} else {
		elements = renderTerminal(state, rc)
	}
	return Card(elements, CardOpts{Streaming: running, Summary: summaryText(state)})
}

// BuildRunCardPlain 去按钮版（历史卡降级）。
func BuildRunCardPlain(rc RunCardState) CardObject {
	rc.CardKey = ""
	return BuildRunCard(rc)
}

// QueuedCardState 排队占位卡。
type QueuedCardState struct {
	Position  int
	CardKey   string
	Cancelled bool
	Dropped   int
	Started   bool
}

// BuildQueuedCard 排队卡。
func BuildQueuedCard(qc QueuedCardState) CardObject {
	if qc.Cancelled {
		els := []CardElement{NoteMd("_⏹ 已取消排队_")}
		if qc.Dropped > 0 {
			els = append(els, NoteMd(fmt.Sprintf("_⚠️ %d 条排队消息已丢弃，请重发。_", qc.Dropped)))
		}
		return Card(els, CardOpts{Summary: "已取消排队"})
	}
	if qc.Started {
		return Card([]CardElement{NoteMd("_🎯 排队结束，目标已开始执行_")}, CardOpts{Summary: "已开始执行"})
	}
	pos := qc.Position
	if pos == 0 {
		pos = 1
	}
	els := []CardElement{
		Md(fmt.Sprintf("⏳ 排队中（第 **%d** 位）", pos)),
		NoteMd("全局并发池已满（所有群/话题共享），轮到后自动开始。"),
	}
	if qc.CardKey != "" {
		els = append(els, Actions([]CardElement{Button("⏹ 取消", ActionValue{"a": RCStop, "m": qc.CardKey}, ButtonDanger)}, ControlsEID))
	}
	return Card(els, CardOpts{Summary: "排队中"})
}

// ── RUNNING 布局 ────────────────────────────────────────────────

func renderRunning(state RunState, rc RunCardState) []CardElement {
	var elements []CardElement
	reasoning := ReasoningContent(state)
	if reasoning != "" {
		elements = append(elements, reasoningPanel(reasoning, state.ReasoningActive))
	}
	showTools := true
	if rc.ShowTools != nil {
		showTools = *rc.ShowTools
	}
	var tools []ToolEntry
	var textParts []string
	for _, b := range state.Blocks {
		if b.Kind == "tool" {
			if showTools {
				tools = append(tools, b.Tool)
			}
		} else if strings.TrimSpace(b.Content) != "" {
			textParts = append(textParts, b.Content)
		}
	}
	if len(tools) > 0 {
		elements = append(elements, renderToolGroupRunning(tools)...)
	}
	answer := strings.Join(textParts, "\n\n")
	if answer != "" {
		elements = append(elements, MdStream(answer, AnswerEID))
	}
	mEl := modelEl(rc)
	if state.Footer != "" && mEl != nil {
		elements = append(elements, SplitRow(footerStatus(state.Footer), mEl, ""))
	} else if state.Footer != "" {
		elements = append(elements, footerStatus(state.Footer))
	} else if mEl != nil {
		elements = append(elements, mEl)
	}
	if g := gaugeEl(state); g != nil {
		elements = append(elements, g)
	}
	// ⏹ 控件行（底部）。
	if rc.CardKey != "" && rc.GoalControls {
		if rc.GoalEnding {
			elements = append(elements, NoteMd("_🎯 目标已解除，本轮输出完成后停止_"))
			elements = append(elements, Actions([]CardElement{Button("⏹ 终止", ActionValue{"a": RCStop, "m": rc.CardKey}, ButtonDanger)}, ControlsEID))
		} else {
			elements = append(elements, Actions([]CardElement{
				Button("⏹ 终止", ActionValue{"a": RCStop, "m": rc.CardKey}, ButtonDanger),
				Button("🎯 结束目标", ActionValue{"a": RCEndGoal, "m": rc.CardKey}, ButtonDefault),
			}, ControlsEID))
		}
	} else if rc.CardKey != "" && !rc.HideStop {
		elements = append(elements, Actions([]CardElement{Button("⏹ 终止", ActionValue{"a": RCStop, "m": rc.CardKey}, ButtonDanger)}, ControlsEID))
	}
	return elements
}

// ── TERMINAL 布局 ───────────────────────────────────────────────

func renderTerminal(state RunState, rc RunCardState) []CardElement {
	var elements []CardElement
	answerIdx := lastTextIndex(state.Blocks)
	answer := ""
	if answerIdx >= 0 {
		answer = strings.TrimSpace(state.Blocks[answerIdx].Content)
	}
	var processBlocks []Block
	for i, b := range state.Blocks {
		if i != answerIdx {
			processBlocks = append(processBlocks, b)
		}
	}
	if rc.ShowTools != nil && !*rc.ShowTools {
		var filtered []Block
		for _, b := range processBlocks {
			if b.Kind != "tool" {
				filtered = append(filtered, b)
			}
		}
		processBlocks = filtered
	}
	reasoning := ReasoningContent(state)
	processEls := buildProcessBody(reasoning, processBlocks)
	if len(processEls) > 0 {
		toolCount := 0
		for _, b := range processBlocks {
			if b.Kind == "tool" {
				toolCount++
			}
		}
		elements = append(elements, CollapsiblePanelEl(processTitle(reasoning != "", toolCount), false, "grey", processEls))
	}
	if answer != "" {
		elements = append(elements, RenderRichText(answer, rc.Images)...)
	}
	switch state.Terminal {
	case TermInterrupted:
		elements = append(elements, NoteMd("_⏹ 已被中断_"))
	case TermIdleTimeout:
		s := state.IdleTimeoutSeconds
		idleLabel := fmt.Sprintf("%d 秒", s)
		if s > 0 && s%60 == 0 {
			idleLabel = fmt.Sprintf("%d 分钟", s/60)
		}
		elements = append(elements, NoteMd(fmt.Sprintf("_⏱ %s无响应，已自动终止_", idleLabel)))
	case TermError:
		if state.ErrorMsg != "" {
			elements = append(elements, NoteMd("⚠️ agent 失败："+state.ErrorMsg))
			if advice := errorAdvice(state.ErrorMsg); advice != "" {
				elements = append(elements, NoteMd(advice))
			}
		}
	case TermDone:
		if answer == "" {
			elements = append(elements, NoteMd("_（未返回内容）_"))
		}
	}
	if g := gaugeEl(state); g != nil {
		elements = append(elements, g)
	}
	if rc.ModelOnTerminal {
		if mEl := modelEl(rc); mEl != nil {
			elements = append(elements, mEl)
		}
	}
	return elements
}

func gaugeEl(state RunState) CardElement {
	if state.Usage == nil {
		return nil
	}
	if !RunCardGaugeVisible(state.Usage.Used, state.Usage.Window) {
		return nil
	}
	pct, _ := CtxPercent(state.Usage.Used, state.Usage.Window)
	t := CtxTierFor(float64(state.Usage.Used) / float64(*state.Usage.Window))
	return ColorNote(fmt.Sprintf("%s 上下文 %d%% · %s/%s · %s", t.Dot, pct, K(state.Usage.Used), K(*state.Usage.Window), t.Advice), t.Color)
}

// ── 工具组渲染 ──────────────────────────────────────────────────

type toolGroup struct {
	kind  string // "tools" | "text"
	tools []ToolEntry
	text  string
}

func groupBlocks(blocks []Block) []toolGroup {
	var groups []toolGroup
	var toolBuf []ToolEntry
	flushTools := func() {
		if len(toolBuf) > 0 {
			groups = append(groups, toolGroup{kind: "tools", tools: toolBuf})
			toolBuf = nil
		}
	}
	for _, b := range blocks {
		if b.Kind == "tool" {
			toolBuf = append(toolBuf, b.Tool)
		} else {
			flushTools()
			groups = append(groups, toolGroup{kind: "text", text: b.Content})
		}
	}
	flushTools()
	return groups
}

func renderToolGroupRunning(tools []ToolEntry) []CardElement {
	if len(tools) == 0 {
		return nil
	}
	if len(tools) < collapseToolThreshold {
		var out []CardElement
		for _, t := range tools {
			out = append(out, toolPanel(t, false))
		}
		return out
	}
	// running: 折叠前面的，留最新一个可见。
	prior := tools[:len(tools)-1]
	latest := tools[len(tools)-1]
	var out []CardElement
	if len(prior) > 0 {
		out = append(out, collapsedToolSummary(prior, false))
	}
	out = append(out, toolPanel(latest, true))
	return out
}

func renderToolGroupFinal(tools []ToolEntry, compact bool) []CardElement {
	if len(tools) == 0 {
		return nil
	}
	if compact {
		return []CardElement{collapsedToolSummary(tools, true)}
	}
	if len(tools) < collapseToolThreshold {
		var out []CardElement
		for _, t := range tools {
			out = append(out, toolPanel(t, false))
		}
		return out
	}
	return []CardElement{collapsedToolSummary(tools, true)}
}

func reasoningPanel(content string, active bool) CardElement {
	title := "🧠 **思考完成，点击查看**"
	if active {
		title = "🧠 **思考中**"
	}
	return CollapsiblePanel(CollapsiblePanelOpts{Title: title, Expanded: active, Border: "grey", Body: truncateRun(content, runReasoningMax)})
}

func toolPanel(tool ToolEntry, expanded bool) CardElement {
	border := "grey"
	if tool.Status == ToolError {
		border = "red"
	}
	body := ToolBodyMd(tool)
	if body == "" {
		body = "_无输出_"
	}
	return CollapsiblePanel(CollapsiblePanelOpts{Title: ToolHeaderText(tool), Expanded: expanded, Border: border, Body: body})
}

func collapsedToolSummary(tools []ToolEntry, finalized bool) CardElement {
	suffix := ""
	if finalized {
		suffix = "（已结束）"
	}
	var lines []string
	for _, t := range tools {
		lines = append(lines, "- "+ToolHeaderText(t))
	}
	return CollapsiblePanel(CollapsiblePanelOpts{
		Title:    fmt.Sprintf("☕ **%d 个工具调用%s**", len(tools), suffix),
		Expanded: false, Border: "blue", Body: strings.Join(lines, "\n"),
	})
}

// ── 过程面板（TERMINAL）──────────────────────────────────────────

func buildProcessBody(reasoning string, blocks []Block) []CardElement {
	rich := processElements(reasoning, blocks, false)
	if estimateJSONSize(rich) <= processBodyBudget {
		return rich
	}
	return processElements(reasoning, blocks, true)
}

func processElements(reasoning string, blocks []Block, compactTools bool) []CardElement {
	var out []CardElement
	if reasoning != "" {
		out = append(out, reasoningPanel(reasoning, false))
	}
	for _, g := range groupBlocks(blocks) {
		if g.kind == "text" {
			if strings.TrimSpace(g.text) != "" {
				out = append(out, Md(g.text))
			}
		} else {
			out = append(out, renderToolGroupFinal(g.tools, compactTools)...)
		}
	}
	return out
}

func processTitle(hasReasoning bool, toolCount int) string {
	var parts []string
	if hasReasoning {
		parts = append(parts, "🧠 思考")
	}
	if toolCount > 0 {
		parts = append(parts, fmt.Sprintf("🧰 %d 个工具调用", toolCount))
	}
	detail := ""
	if len(parts) > 0 {
		detail = "：" + strings.Join(parts, " · ")
	}
	return fmt.Sprintf("🗂 **过程%s**（点击展开）", detail)
}

func estimateJSONSize(els []CardElement) int {
	n := 0
	for _, el := range els {
		b, _ := json.Marshal(el)
		n += len(b)
	}
	return n
}

// ── footer / model ──────────────────────────────────────────────

func footerStatusText(status FooterStatus) string {
	switch status {
	case FooterThinking:
		return "🧠 正在思考"
	case FooterToolRunning:
		return "🧰 正在调用工具"
	case FooterRetrying:
		return "⚠️ 瞬断，自动重试中…"
	}
	return "✍️ 正在输出"
}

func footerStatus(status FooterStatus) CardElement { return NoteMd(footerStatusText(status)) }

var effortTier = map[agent.ReasoningEffort]struct{ Label, Color string }{
	agent.EffortNone:    {"无", "grey"},
	agent.EffortMinimal: {"极简", "grey"},
	agent.EffortLow:     {"低", "yellow"},
	agent.EffortMedium:  {"中", "green"},
	agent.EffortHigh:    {"高", "violet"},
	agent.EffortXhigh:   {"极高", "purple"},
}

func modelEffortMd(model string, effort agent.ReasoningEffort) string {
	if effort == "" {
		return model
	}
	t, ok := effortTier[effort]
	if !ok {
		return model + " · " + string(effort)
	}
	return fmt.Sprintf("%s · <font color='%s'>%s</font>", model, t.Color, t.Label)
}

func modelEl(rc RunCardState) CardElement {
	if rc.Model == "" {
		return nil
	}
	return CardElement{
		"tag": "markdown", "content": modelEffortMd(rc.Model, rc.Effort),
		"text_size": "notation", "text_color": "grey", "text_align": "right",
	}
}

func summaryText(state RunState) string {
	switch state.Terminal {
	case TermInterrupted:
		return "已中断"
	case TermIdleTimeout:
		return "已超时"
	case TermError:
		return "出错"
	case TermDone:
		return "已完成"
	}
	switch state.Footer {
	case FooterToolRunning:
		return "正在调用工具"
	case FooterStreaming:
		return "正在输出"
	case FooterRetrying:
		return "自动重试中"
	}
	return "思考中"
}

var errorAdviceRe = map[string]string{
	`(?i)401|unauthor|not.?logged.?in|login|credential|token.*(expired|invalid)`:             "🔑 凭证可能已失效：请在部署机上运行 `codex login` 重新登录后重试",
	`(?i)usage.?limit|quota|rate.?limit|429|too many requests`:                               "📊 可能触达用量上限：发送 /usage 查看用量，稍后再试",
	`(?i)network|timed?.?out|econn|epipe|enotfound|eai_again|socket|fetch failed|disconnect`: "🌐 网络波动：重发本条消息即可重试",
}

var compiledErrorAdvice []compiledAdvice

type compiledAdvice struct {
	re  *regexp.Regexp
	msg string
}

func init() {
	for pat, msg := range errorAdviceRe {
		compiledErrorAdvice = append(compiledErrorAdvice, compiledAdvice{re: regexp.MustCompile(pat), msg: msg})
	}
}

func errorAdvice(msg string) string {
	for _, a := range compiledErrorAdvice {
		if a.re.MatchString(msg) {
			return a.msg
		}
	}
	return ""
}

func lastTextIndex(blocks []Block) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind == "text" && strings.TrimSpace(blocks[i].Content) != "" {
			return i
		}
	}
	return -1
}
