package card

// command_cards.go —— /model、/resume、/help、建群欢迎卡（对齐 TS card/command-cards）。
// 纯数据构造（CardObject），依赖 element builder + agent types，零飞书 SDK 调用。

import (
	"fmt"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// Action ids。
const (
	MCModel  = "model.set"
	MCEffort = "model.effort"
	RESPick  = "resume.pick"
)

// EffortLabel 推理强度中文标签。
var EffortLabel = map[agent.ReasoningEffort]string{
	agent.EffortNone: "无", agent.EffortMinimal: "极简", agent.EffortLow: "低",
	agent.EffortMedium: "中", agent.EffortHigh: "高", agent.EffortXhigh: "极高",
}

// ── /model ──────────────────────────────────────────────────────

// ModelCardState /model 卡服务端状态。
type ModelCardState struct {
	ChatID, ThreadID, RequesterOpenID string
	Models                            []agent.ModelInfo
	Model                             string
	Effort                            agent.ReasoningEffort
	Backend                           string // 写回会话前复核，防跨后端喂坏 id
	CreatedAt                         int64
	Note                              string
}

// BuildModelCard /model 卡——按后端能力自适应（多模型才给模型下拉、有 efforts 才给 effort 下拉）。
func BuildModelCard(s ModelCardState) CardObject {
	var visible []agent.ModelInfo
	for _, m := range s.Models {
		if !m.Hidden {
			visible = append(visible, m)
		}
	}
	var cur *agent.ModelInfo
	for i := range s.Models {
		if s.Models[i].ID == s.Model {
			cur = &s.Models[i]
			break
		}
	}
	efforts := []agent.ReasoningEffort{}
	if cur != nil {
		efforts = cur.SupportedEfforts
	}
	canPickModel := len(visible) > 1
	canPickEffort := len(efforts) > 0
	curLabel := s.Model
	if cur != nil {
		curLabel = cur.DisplayName
	}

	elements := []CardElement{Md("🧠 **模型 / 推理强度**")}
	if canPickModel || canPickEffort {
		elements = append(elements, Note("选择后下一轮生效"), Hr())
		var controls []CardElement
		if canPickModel {
			opts := make([]SelectOption, 0, len(visible))
			for _, m := range visible {
				opts = append(opts, SelectOption{Label: m.DisplayName, Value: m.ID})
			}
			controls = append(controls, SelectStatic(SelectStaticOpts{
				ActionID: MCModel, Placeholder: "选择模型", Initial: s.Model, Options: opts,
			}))
		}
		if canPickEffort {
			opts := make([]SelectOption, 0, len(efforts))
			for _, e := range efforts {
				opts = append(opts, SelectOption{Label: "effort：" + EffortLabel[e], Value: string(e)})
			}
			controls = append(controls, SelectStatic(SelectStaticOpts{
				ActionID: MCEffort, Placeholder: "effort", Initial: string(s.Effort), Options: opts,
			}))
		}
		elements = append(elements, Actions(controls, ""))
		if canPickModel && !canPickEffort {
			elements = append(elements, Note("该后端不调节推理强度（思考由模型自动调度，无 Codex 那样的 effort 档）"))
		}
	} else {
		elements = append(elements, Hr(), Md(fmt.Sprintf("当前模型：**%s**", curLabel)))
		elements = append(elements, Note("该后端不支持在此切换模型或推理强度。"))
	}
	if s.Note != "" {
		elements = append(elements, Note(s.Note))
	}
	return Card(elements, CardOpts{Summary: "模型设置"})
}

// ── /resume ────────────────────────────────────────────────────

const resumeTitleMax = 30

// ResumeCardState /resume 卡服务端状态。
type ResumeCardState struct {
	ChatID, OriginalMsgID, RequesterOpenID, Cwd, ProjectName string
	Backend                                                  string
	Threads                                                  []agent.ThreadSummary
	CreatedAt                                                int64
	Launching                                                bool
}

// BuildResumeCard /resume 卡：每会话一按钮 ↩️ 时间·标题（时间优先、标题截断单行）。
func BuildResumeCard(s ResumeCardState, now time.Time) CardObject {
	elements := []CardElement{Md("🕘 **恢复历史会话**"), Note(metaNote(s.Cwd, s.ProjectName)), Hr()}
	if len(s.Threads) == 0 {
		elements = append(elements, Md("_该目录下还没有历史会话。直接 @我 即可新建。_"))
	} else {
		elements = append(elements, Note("点一条即恢复 —— 在新话题里打开历史、可直接继续。"))
		for _, t := range s.Threads {
			title := strings.ReplaceAll(strings.TrimSpace(firstNonEmpty(t.Name, t.Preview, "(无摘要)")), "\n", " ")
			title = collapseSpaces(title)
			label := fmt.Sprintf("↩️ %s · %s", PickerTime(latestOf(t.UpdatedAt, t.CreatedAt), now), truncate(title, resumeTitleMax))
			val := ActionValue{"a": RESPick, "t": t.SessionID}
			if s.Backend != "" {
				val["b"] = s.Backend
			}
			elements = append(elements, Actions([]CardElement{Button(label, val, ButtonDefault)}, ""))
		}
	}
	return Card(elements, CardOpts{Summary: "恢复历史会话"})
}

// BuildResumeLaunchingCard 恢复中（去控件防双击）。
func BuildResumeLaunchingCard(s ResumeCardState) CardObject {
	return Card([]CardElement{Md("⏳ 正在恢复历史会话…"), Note(metaNote(s.Cwd, s.ProjectName))}, CardOpts{Summary: "恢复中"})
}

// BuildResumeDoneCard 恢复成功。
func BuildResumeDoneCard(s ResumeCardState) CardObject {
	return Card([]CardElement{Md("✅ 已恢复 —— 已在上方新话题打开，可直接继续。"), Note(metaNote(s.Cwd, s.ProjectName))}, CardOpts{Summary: "已恢复"})
}

// BuildResumeErrorCard 恢复失败。
func BuildResumeErrorCard(s ResumeCardState, message string) CardObject {
	return Card([]CardElement{Md("❌ 恢复失败：" + truncate(message, 200)), Note(metaNote(s.Cwd, s.ProjectName))}, CardOpts{Summary: "恢复失败"})
}

// ── 时间格式化（注入 now 便于测试）────────────────────────────────

// RelativeTime 粗略相对时间（unix 秒或毫秒）。
func RelativeTime(ts int64, now time.Time) string {
	if ts == 0 {
		return "未知时间"
	}
	ms := ts
	if ts < 1e12 {
		ms = ts * 1000
	}
	diff := now.UnixMilli() - ms
	min := diff / 60000
	if min < 1 {
		return "刚刚"
	}
	if min < 60 {
		return fmt.Sprintf("%d 分钟前", min)
	}
	hr := min / 60
	if hr < 24 {
		return fmt.Sprintf("%d 小时前", hr)
	}
	day := hr / 24
	if day < 30 {
		return fmt.Sprintf("%d 天前", day)
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

// PickerTime resume 按钮时间戳：近期友好、远期分钟精确（同年同题会话靠时分区分）。
func PickerTime(ts int64, now time.Time) string {
	if ts == 0 {
		return "未知时间"
	}
	ms := ts
	if ts < 1e12 {
		ms = ts * 1000
	}
	min := (now.UnixMilli() - ms) / 60000
	if min < 1 {
		return "刚刚"
	}
	if min < 60 {
		return fmt.Sprintf("%d分钟前", min)
	}
	d := time.UnixMilli(ms)
	hm := fmt.Sprintf("%02d:%02d", d.Hour(), d.Minute())
	sameDay := d.Year() == now.Year() && d.Month() == now.Month() && d.Day() == now.Day()
	if sameDay {
		return "今天 " + hm
	}
	md := fmt.Sprintf("%02d-%02d", int(d.Month()), d.Day())
	if d.Year() == now.Year() {
		return md + " " + hm
	}
	return fmt.Sprintf("%d-%s %s", d.Year(), md, hm)
}

// ── /help & 欢迎卡 ──────────────────────────────────────────────

// HelpScope 帮助卡场景。
type HelpScope string

const (
	HelpMain   HelpScope = "main"
	HelpTopic  HelpScope = "topic"
	HelpSingle HelpScope = "single"
)

// HelpCaps 后端能力（/help 关心的三项）；nil 字段 = 支持。
type HelpCaps struct {
	Goal    *bool
	Compact *bool
	Resume  *bool
}

func capsShow(c *bool) bool {
	if c == nil {
		return true
	}
	return *c
}

// BuildHelpCard /help 速查卡——按 scope/noMention/isAdmin/caps 裁剪。
func BuildHelpCard(scope HelpScope, noMention, isAdmin bool, caps HelpCaps) CardObject {
	showGoal := capsShow(caps.Goal)
	showCompact := capsShow(caps.Compact)
	showResume := capsShow(caps.Resume)
	goalLine := "· `/goal <目标>` → 自主多轮跑到完成（卡上 ⏹ 终止 / 🎯 结束目标）"
	compactLine := "· `/compact` → 压缩上下文（释放空间）"

	var elements []CardElement
	var lines []string
	switch scope {
	case HelpSingle:
		lines = append(lines, talkLine(noMention, "交给我处理"))
		if showGoal {
			lines = append(lines, goalLine)
		}
		lines = append(lines, "· `/model` → 切换模型 / 推理强度", "· `/context` → 看上下文占比")
		if showCompact {
			lines = append(lines, compactLine)
		}
		if isAdmin {
			lines = append(lines, "· `/settings` → 群设置（免@ 开关）")
		}
		lines = append(lines, "· `/help` → 这张速查卡")
		elements = []CardElement{Md("💬 **单会话群** — 整群就是一个会话，上下文连续。"), Hr(), Md(strings.Join(lines, "\n"))}
	case HelpTopic:
		lines = append(lines, talkLine(noMention, "继续当前会话"))
		if showGoal {
			lines = append(lines, goalLine)
		}
		lines = append(lines, "· `/model` → 切换模型 / 推理强度", "· `/context` → 看上下文占比")
		if showCompact {
			lines = append(lines, compactLine)
		}
		lines = append(lines, "· `/help` → 这张速查卡")
		elements = []CardElement{
			Md("🧵 **话题内** — 每个话题是一个独立会话。"),
			Hr(),
			Md(strings.Join(lines, "\n")),
			Note("开新话题：回到主群区 @我 + 内容。"),
		}
	default: // main
		lines = append(lines, "· **@我 + 内容** → 开一个新话题并开始")
		if showGoal {
			lines = append(lines, goalLine)
		}
		if isAdmin && showResume {
			lines = append(lines, "· `/resume` → 恢复历史会话")
		}
		if isAdmin {
			lines = append(lines, "· `/settings` → 群设置（免@ 开关）")
		}
		lines = append(lines, "· `/model` → 需要在话题里用", "· `/help` → 这张速查卡")
		elements = []CardElement{Md("👥 **主群区** — @我开话题，每个话题是独立会话。"), Hr(), Md(strings.Join(lines, "\n"))}
	}
	return Card(elements, CardOpts{Header: &CardHeader{Title: "🤖 可用命令", Template: HeaderBlue}, Summary: "可用命令"})
}

// BuildWelcomeCard 建群/绑定时的欢迎卡。
func BuildWelcomeCard(kind string, docURL string, noMention bool, caps HelpCaps, agentName string) CardObject {
	if agentName == "" {
		agentName = "Codex"
	}
	showGoal := capsShow(caps.Goal)
	showCompact := capsShow(caps.Compact)
	showResume := capsShow(caps.Resume)
	goalLine := "· `/goal <目标>` → 自主多轮跑到完成（卡上 ⏹ 终止 / 🎯 结束目标）"
	ctxLine := "· `/context` → 看上下文占比"
	if showCompact {
		ctxLine = "· `/context` · `/compact` → 看 / 压缩上下文"
	}
	elements := []CardElement{
		Md(fmt.Sprintf("👋 **欢迎使用 %s Bridge** — 本群已绑定一个项目目录，在群里就能驱动本机 %s 干活。", agentName, agentName)),
		Hr(),
	}
	if kind == "single" {
		lines := []string{talkLine(noMention, "交给我处理")}
		if showGoal {
			lines = append(lines, goalLine)
		}
		lines = append(lines, "· `/model` → 切换模型 / 推理强度", "· `/settings` → 群设置（免@ 开关）", "· `/help` → 命令速查卡")
		elements = append(elements, Md("💬 **单会话群**（整群一个会话，上下文连续）"), Md(strings.Join(lines, "\n")))
	} else {
		lines := []string{"· **@我 + 内容** → 开一个新话题并开始（每话题独立会话）"}
		if showGoal {
			lines = append(lines, goalLine)
		}
		if showResume {
			lines = append(lines, "· `/resume` → 恢复历史会话")
		}
		lines = append(lines, "· `/settings` → 群设置（免@ 开关）")
		elements = append(elements,
			Md("👥 **主群区**"),
			Md(strings.Join(lines, "\n")),
			Md("🧵 **话题内**"),
			Md(strings.Join([]string{"· 直接发消息（免@）→ 继续当前会话", "· `/model` → 切换模型 / 推理强度", ctxLine}, "\n")),
			Note("任意场景发 `/help` 看当前可用命令。"),
		)
	}
	if docURL != "" {
		elements = append(elements, Hr(), Actions([]CardElement{LinkButton("📖 查看完整使用手册", docURL, ButtonPrimary, "")}, ""))
	}
	return Card(elements, CardOpts{Header: &CardHeader{Title: "🤖 本群使用说明", Template: HeaderTurquoise}, Summary: "本群使用说明"})
}

// ── 辅助 ───────────────────────────────────────────────────────

func talkLine(noMention bool, tail string) string {
	if noMention {
		return "· 直接发消息（免@）→ " + tail
	}
	return "· **@我 + 内容** → " + tail + "（本群默认需 @；`/settings` 可开启免@）"
}

func metaNote(cwd, projectName string) string {
	parts := []string{"📂 `" + cwd + "`"}
	if projectName != "" {
		return "📁 " + projectName + "   " + parts[0]
	}
	return parts[0]
}

func truncate(s string, n int) string {
	t := strings.TrimSpace(s)
	runes := []rune(t)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return t
}

func firstNonEmpty(a, b, c string) string {
	if a != "" {
		return a
	}
	if b != "" {
		return b
	}
	return c
}

func latestOf(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func collapseSpaces(s string) string {
	out := strings.Builder{}
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				out.WriteRune(' ')
			}
			prevSpace = true
		} else {
			out.WriteRune(r)
			prevSpace = false
		}
	}
	return out.String()
}
