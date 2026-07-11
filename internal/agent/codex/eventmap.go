package codex

import (
	"encoding/json"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// eventmap.go —— codex app-server 通知 → 归一化 AgentEvent（纯函数，对齐 TS event-map）。
//
// 流式：item/agentMessage/delta + item/reasoning/textDelta 给 token 级增量；
// item/completed 给最终文本（reconcile）；commandExecution/fileChange → 工具块。

const (
	titleFilesMax = 2
	diffMax       = 1200
	pathTailMax   = 40
)

// MapContext 映射上下文。Cwd 让 fileChange 标题路径相对化（cwd 外的路径保留绝对，
// 让用户看到 agent 动了项目外的文件）。
type MapContext struct {
	Cwd string
}

// MapNotification 把一条 ServerNotification 映射为 AgentEvent；不关心的通知返回零值 ok=false。
func MapNotification(n ServerNotification, ctx *MapContext) (agent.AgentEvent, bool) {
	switch n.Method {
	case "thread/started":
		var p pThreadID
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return agent.EvSys(p.Thread.ID), true
	case "turn/started":
		var p pTurnID
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return agent.EvTurnStart(p.Turn.ID), true
	case "item/agentMessage/delta":
		var p pItemDelta
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return agent.EvTextD(p.ItemID, p.Delta), true
	case "item/reasoning/textDelta":
		var p pItemDelta
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return agent.EvThinkingD(p.ItemID, p.Delta), true
	case "item/started":
		var p pItem
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return mapItemStart(p.Item, ctx)
	case "item/completed":
		var p pItem
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return mapItemComplete(p.Item)
	case "thread/tokenUsage/updated":
		// 用 last（最近一轮上下文），非 total（累计只增、compact 后不降）。
		var p pTokenUsage
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return agent.EvContext(p.TokenUsage.Last.TotalTokens, p.TokenUsage.ModelContextWindow), true
	case "thread/compacted":
		return agent.EvCompacted(), true
	case "turn/completed":
		var p pTurnID
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return agent.EvDoneT(p.Turn.ID), true
	case "thread/goal/updated":
		var p pGoal
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		g := p.Goal
		return agent.EvGoalUpdateE(g.Status, g.Objective, g.TokensUsed, g.TimeUsedSeconds, g.TokenBudget), true
	case "error":
		var p pError
		if unmarshal(n.Params, &p) != nil {
			return agent.AgentEvent{}, false
		}
		return agent.EvErrorT(p.Error.Message, p.WillRetry), true
	}
	return agent.AgentEvent{}, false
}

func mapItemStart(item ThreadItem, ctx *MapContext) (agent.AgentEvent, bool) {
	switch item.Type {
	case "commandExecution":
		return agent.EvToolUK(item.ID, item.Command, item.Cwd, agent.ToolKindCommand), true
	case "fileChange":
		return agent.EvToolUK(item.ID, FileChangeTitle(item.Changes, ctxOf(ctx)), "", agent.ToolKindFile), true
	case "webSearch":
		q := item.Query
		title := "联网搜索"
		if q != "" {
			title = "联网搜索：" + q
		}
		return agent.EvToolUK(item.ID, title, "", agent.ToolKindSearch), true
	case "mcpToolCall", "dynamicToolCall":
		return agent.EvToolUK(item.ID, "工具调用", "", agent.ToolKindTool), true
	}
	return agent.AgentEvent{}, false
}

func mapItemComplete(item ThreadItem) (agent.AgentEvent, bool) {
	switch item.Type {
	case "agentMessage":
		return agent.EvTextFull(item.ID, item.Text), true
	case "reasoning":
		c := item.ReasoningContent()
		text := strings.Join(c, "\n")
		if text == "" {
			text = strings.Join(item.Summary, "\n")
		}
		return agent.EvThinkingFull(item.ID, text), true
	case "commandExecution":
		return agent.EvToolR(item.ID, item.AggregatedOutput, item.ExitCode), true
	case "fileChange":
		return agent.EvToolR(item.ID, FileChangeDiffMd(item.Changes), nil), true
	case "webSearch", "mcpToolCall", "dynamicToolCall":
		return agent.EvToolR(item.ID, "", nil), true
	}
	return agent.AgentEvent{}, false
}

func ctxOf(ctx *MapContext) string {
	if ctx == nil {
		return ""
	}
	return ctx.Cwd
}

func unmarshal(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// ── fileChange 标题 / diff（kind-aware，见 TS 注释）─────────────────

const (
	kindAdd    = "add"
	kindDelete = "delete"
	kindUpdate = "update"
)

// changeKind 容忍 string 或 {type:string}，默认 update。
func changeKind(c FileUpdateChange) string {
	raw := strings.TrimSpace(string(c.Kind))
	if raw == "" {
		return kindUpdate
	}
	// 先试 string。
	var s string
	if json.Unmarshal(c.Kind, &s) == nil {
		return normalizeKind(s)
	}
	// 再试 {type:string}。
	var obj struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(c.Kind, &obj) == nil {
		return normalizeKind(obj.Type)
	}
	return kindUpdate
}

func normalizeKind(s string) string {
	switch s {
	case kindAdd, kindDelete:
		return s
	}
	return kindUpdate
}

// contentLineCount 裸内容的行数（末尾单个换行不算一行；空=0）。
func contentLineCount(content string) int {
	if content == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(content, "\n"), "\n"))
}

// countChange 一个改动的 +/− 行数（kind-aware）。
func countChange(c FileUpdateChange) (adds, dels int) {
	switch changeKind(c) {
	case kindAdd:
		return contentLineCount(c.Diff), 0
	case kindDelete:
		return 0, contentLineCount(c.Diff)
	}
	for _, line := range strings.Split(c.Diff, "\n") {
		if strings.HasPrefix(line, "+++") {
			continue
		}
		if strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			adds++
		} else if strings.HasPrefix(line, "-") {
			dels++
		}
	}
	return adds, dels
}

// displayPath cwd 内相对化；无 cwd 时长路径只留尾部段（≤pathTailMax）。
func displayPath(p, cwd string) string {
	if cwd != "" {
		sep := "/"
		if strings.Contains(cwd, "\\") {
			sep = "\\"
		}
		root := cwd
		if !strings.HasSuffix(root, sep) {
			root = root + sep
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

// FileChangeTitle 文件改动标题：「新建/删除/编辑 path (+N −M)」。
func FileChangeTitle(changes []FileUpdateChange, cwd string) string {
	if len(changes) == 0 {
		return "编辑文件"
	}
	adds, dels := 0, 0
	kinds := map[string]bool{}
	for _, c := range changes {
		kinds[changeKind(c)] = true
		a, d := countChange(c)
		adds += a
		dels += d
	}
	verb := "编辑"
	switch {
	case len(kinds) > 1:
		verb = "编辑"
	case kinds[kindAdd]:
		verb = "新建"
	case kinds[kindDelete]:
		verb = "删除"
	}
	names := make([]string, 0, titleFilesMax)
	for i := 0; i < len(changes) && i < titleFilesMax; i++ {
		names = append(names, displayPath(changes[i].Path, cwd))
	}
	files := strings.Join(names, "、")
	if len(changes) > titleFilesMax {
		files = namesJoined(names) + " 等 " + itoa(len(changes)) + " 个文件"
	}
	suffix := ""
	switch verb {
	case "删除":
		suffix = ""
	case "新建":
		suffix = " (+" + itoa(adds) + ")"
	default:
		suffix = " (+" + itoa(adds) + " −" + itoa(dels) + ")"
	}
	return verb + " " + files + suffix
}

func namesJoined(names []string) string { return strings.Join(names, "、") }

// FileChangeDiffMd 全部改动合成一个 ```diff 围栏（超长截断）；无内容返回空。
func FileChangeDiffMd(changes []FileUpdateChange) string {
	if len(changes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		body := changeDiffBody(c)
		if len(changes) > 1 {
			parts = append(parts, "diff --git a/"+c.Path+" b/"+c.Path+"\n"+body)
		} else {
			parts = append(parts, body)
		}
	}
	joined := strings.TrimRight(strings.Join(parts, "\n"), "\n")
	if strings.TrimSpace(joined) == "" {
		return ""
	}
	cut := len(joined) > diffMax
	body := joined
	note := ""
	if cut {
		body = joined[:diffMax] + "…"
		note = "\n_（已截断，完整 diff " + itoa(len(joined)) + " 字符）_"
	}
	return "```diff\n" + body + "\n```" + note
}

// changeDiffBody 单个改动的 diff 体：add/delete 合成 +/- 前缀，update 原样。
func changeDiffBody(c FileUpdateChange) string {
	if changeKind(c) == kindUpdate {
		return c.Diff
	}
	content := strings.TrimSuffix(c.Diff, "\n")
	if content == "" {
		return ""
	}
	prefix := "+"
	if changeKind(c) == kindDelete {
		prefix = "-"
	}
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
