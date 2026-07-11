package clibridge

// cards.go —— cli-bridge 飞书卡片（对齐 TS cli-bridge/cards.ts）。
// 所有卡片走 card 包 builder（schema 2.0），forward:false（不转发）。

import (
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/card"
)

// shortElem 生成飞书合法的 element_id / form name：≤20 字符，仅 [a-zA-Z0-9_]，且以字母开头。
// prefix 必须本身合法；id 取末尾若干字符拼到 prefix 后，超出 20 则截断（element_id 仅作 DOM id，
// 与实际交互 ID 解耦——交互靠 value.id 回查）。
func shortElem(prefix, id string) string {
	const maxLen = 20
	if len(prefix) >= maxLen {
		return prefix[:maxLen]
	}
	room := maxLen - len(prefix)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	clean := b.String()
	if len(clean) > room {
		clean = clean[len(clean)-room:] // 取后缀，保短暂+唯一
	}
	return prefix + clean
}

// CLI 卡片回调 action id（dispatcher 按 value.a 路由）。
var CLI = struct {
	ToggleEnabled    string
	SetDelivery      string
	SetNotifyScope   string
	ToggleAgent      string
	ToggleKeepAwake  string
	ToggleIncludeBridge string
	RepairHooks      string
	ApproveOnce      string
	ApproveSession   string
	Deny             string
	QuestionSubmit   string
	TaskCompletionDone string
}{
	ToggleEnabled:       "cli.toggle.enabled",
	SetDelivery:         "cli.set.delivery",
	SetNotifyScope:      "cli.set.notifyScope",
	ToggleAgent:         "cli.toggle.agent",
	ToggleKeepAwake:     "cli.toggle.keepAwake",
	ToggleIncludeBridge: "cli.toggle.includeBridge",
	RepairHooks:         "cli.hooks.repair",
	ApproveOnce:         "cli.approve.once",
	ApproveSession:      "cli.approve.session",
	Deny:                "cli.deny",
	QuestionSubmit:      "cli.question.submit",
	TaskCompletionDone:  "cli.taskCompletion.done",
}

// BRAND 每张桥卡品牌前缀。
const BRAND = "🌈 Vonvon Bridge"

var agentLabel = map[CliBridgeAgent]string{
	AgentClaude: "Claude Code",
	AgentCodex:  "Codex",
}

var statusLabel = map[string]string{
	"installed":            "已安装",
	"not_installed":        "未安装",
	"needs_repair":         "需修复",
	"conflict_agent2lark":  "与 agent2lark 冲突",
}

type interactionStatus = string

const (
	stPending interactionStatus = "pending"
	stApproved interactionStatus = "approved"
	stDenied   interactionStatus = "denied"
	stTimeout  interactionStatus = "timeout"
	stLocal    interactionStatus = "local"
)

const taskOutputChunkSize = 2800

// COPY 活泼轮换文案（按 key 确定性选取，测试可断言成员）。
var COPY = struct {
	Away       []copyLine
	Permission []string
	Question   []string
	Completion []string
	FooterAway []string
	FooterReply []string
}{
	Away: []copyLine{
		{Title: "你溜啦?桥我先给你架上", Body: "本地还在跑活儿 —— 接下来要审批 / 提问 / 收尾,我都顺着桥递给你。"},
		{Title: "人走桥不断,我接管了", Body: "检测到你离开。本机 Claude / Codex 的大小事我接着,要紧的就喊你。"},
		{Title: "你忙你的,这头交给我", Body: "你不在键盘前这段,本地的审批 / 提问 / 完成,我都送到你手上。"},
		{Title: "桥已就位,接管成功", Body: "本机的活儿还跑着呢,我替你守在这头,该你拍板的一个都不漏。"},
	},
	Permission: []string{
		"桥那头想动手,先问你一声",
		"有条命令想跑,等你点个头",
		"它举手了:这个操作能放行吗?",
		"本地要执行点东西,你来拍板",
	},
	Question: []string{
		"桥那头卡了个选择,等你定",
		"有道选择题送到你面前啦",
		"它拿不准,想听听你的",
		"帮你接住一个选择,选哪个?",
	},
	Completion: []string{
		"桥那头收工了,瞄一眼?",
		"活儿干完了,等你一句话",
		"搞定!想接着支使就回我一句",
		"这一轮结束,看看成果?",
	},
	FooterAway: []string{
		"你一回电脑(解锁 / 动键鼠),我立刻收桥,绝不打扰。",
		"回到键盘我就把桥撤了,半点不烦你。",
		"人在桌前我就闭嘴,一切回归终端。",
	},
		FooterReply: []string{
			"💬 直接回复这条消息,就能接着支使它干活;或点「收工」让它退出。",
			"💬 回我一句话,它立刻接着跑;不想继续就点「收工」。",
		},
}

type copyLine struct {
	Title string
	Body  string
}

// PickCopy 按 key 确定性选取（同 key → 同文案，不同 key 分散）。
func PickCopy[T any](pool []T, key string) T {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return pool[h%uint32(len(pool))]
}

func sessionLabel(cwd string) string {
	s := strings.TrimRight(cwd, "/\\")
	parts := strings.Split(s, "/")
	if len(parts) > 1 {
		s = parts[len(parts)-1]
	} else if len(parts) == 1 && parts[0] != "" {
		s = parts[0]
	}
	if s == "" {
		return "session"
	}
	return s
}

func titleEl(verb string) card.CardElement {
	return card.Md("**" + BRAND + " · " + verb + "**")
}

func metaLine(source CliBridgeAgent, cwd string, extra ...string) card.CardElement {
	chips := "🤖 `" + agentLabel[source] + "`　💬 `" + sessionLabel(cwd) + "`"
	if len(extra) > 0 && extra[0] != "" {
		chips += "　" + extra[0]
	}
	return card.Note(chips)
}

// Clip 截断自由文本（防飞书卡片体积上限 ~30KB 导致发送失败）。
func Clip(text string, max int) string {
	if max <= 0 {
		max = 3000
	}
	if len(text) > max {
		return text[:max] + "\n…（已截断 / truncated）"
	}
	return text
}

func codeBlock(content, language string) string {
	fence := "```"
	if strings.Contains(content, "```") {
		fence = "````"
	}
	return fence + language + "\n" + content + "\n" + fence
}

func splitIntoChunks(value string, chunkSize int) []string {
	if chunkSize <= 0 {
		chunkSize = taskOutputChunkSize
	}
	var chunks []string
	for i := 0; i < len(value); i += chunkSize {
		end := i + chunkSize
		if end > len(value) {
			end = len(value)
		}
		chunks = append(chunks, value[i:end])
	}
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	return chunks
}

func disabledButton(label string, btnType card.ButtonType) card.CardElement {
	return card.CardElement{
		"tag":  "button",
		"text": card.CardElement{"tag": "plain_text", "content": label},
		"type": btnType,
		"disabled": true,
	}
}

// BuildCliBridgeAwayNoticeCard 离开时一次性「我接管了」抬头卡（仅 away 路由发，显完整 cwd）。
func BuildCliBridgeAwayNoticeCard(input struct {
	Source CliBridgeAgent
	Cwd    string
	Key    string
}) card.CardObject {
	k := input.Key
	if k == "" {
		k = input.Cwd
	}
	c := PickCopy(COPY.Away, k)
	f := false
	return card.Card(
		[]card.CardElement{
			titleEl(c.Title),
			metaLine(input.Source, input.Cwd),
			card.Note(c.Body),
			card.Md("📂 **当前项目**\n" + input.Cwd),
			card.Note(PickCopy(COPY.FooterAway, k)),
		},
		card.CardOpts{Forward: &f},
	)
}

// BuildCliBridgeNoticeCard 极简通知卡（完成同步建群失败时的 owner 兜底）。
func BuildCliBridgeNoticeCard(input struct {
	Source CliBridgeAgent
	Cwd    string
	Title  string
	Body   string
}) card.CardObject {
	f := false
	return card.Card(
		[]card.CardElement{
			titleEl(input.Title),
			metaLine(input.Source, input.Cwd),
			card.Md(Clip(input.Body, 3000)),
		},
		card.CardOpts{Forward: &f},
	)
}

// BuildCliBridgeApprovalCard 审批卡。
func BuildCliBridgeApprovalCard(input struct {
	ID        string
	Source    CliBridgeAgent
	Cwd       string
	ToolName  string
	Command   string
	AllowSession bool
	Status    interactionStatus
	HookEventName string
	SessionID string
	CreatedAt int64
}) card.CardObject {
	status := input.Status
	if status == "" {
		status = stPending
	}
	verb := func() string {
		switch status {
		case stApproved:
			return "✅ 已允许"
		case stDenied:
			return "⛔ 已拒绝"
		case stLocal:
			return "↩️ 已转交本机"
		case stTimeout:
			return "⏰ 已超时"
		default:
			return PickCopy(COPY.Permission, input.ID+input.Cwd)
		}
	}()
	tool := ""
	if input.ToolName != "" {
		tool = "🛠️ `" + input.ToolName + "`"
	}
	elements := []card.CardElement{
		titleEl(verb),
		metaLine(input.Source, input.Cwd, tool),
	}
	if input.Command != "" {
		elements = append(elements, card.Md("💻 **命令**\n"+codeBlock(Clip(input.Command, 3000), "bash")))
	} else {
		elements = append(elements, card.Note("（hook 未带命令文本）"))
	}
	if status == stPending {
		acts := []card.CardElement{
			card.Button("✅ 允许", card.ActionValue{"a": CLI.ApproveOnce, "id": input.ID}, card.ButtonPrimary),
		}
		if !input.AllowSession {
			acts = append(acts, card.Button("🔁 始终允许", card.ActionValue{"a": CLI.ApproveSession, "id": input.ID}, card.ButtonDefault))
		}
		acts = append(acts, card.Button("⛔ 拒绝", card.ActionValue{"a": CLI.Deny, "id": input.ID}, card.ButtonDanger))
		elements = append(elements, card.Actions(acts, shortElem("approval_", input.ID)))
	}
	f := false
	return card.Card(elements, card.CardOpts{Forward: &f})
}

// QuestionChoiceField / QuestionCustomField 表单字段名（与 resolve 端对齐）。
func QuestionChoiceField(index int) string { return "q" + itoa(index) + "_choice" }
func QuestionCustomField(index int) string { return "q" + itoa(index) + "_custom" }

func optionDisplay(o CliQuestionOption) string {
	if o.Description == "" {
		return o.Label
	}
	desc := o.Description
	if len(desc) > 36 {
		desc = desc[:36] + "…"
	}
	return o.Label + " — " + desc
}

// BuildCliBridgeQuestionCard AskUserQuestion 多问题表单卡。
func BuildCliBridgeQuestionCard(input struct {
	ID        string
	Source    CliBridgeAgent
	Cwd       string
	Questions []CliQuestionItem
	Status    interactionStatus
	Answers   map[string]string
	HookEventName string
	CreatedAt int64
}) card.CardObject {
	status := input.Status
	if status == "" {
		status = stPending
	}
	questions := input.Questions
	numbered := len(questions) > 1
	verb := func() string {
		switch status {
		case stApproved:
			return "✅ 已回答"
		case stDenied:
			return "⛔ 已拒绝"
		case stLocal:
			return "↩️ 已转交本机"
		case stTimeout:
			return "⏰ 已超时"
		default:
			return PickCopy(COPY.Question, input.ID+input.Cwd)
		}
	}()
	elements := []card.CardElement{
		titleEl(verb),
		metaLine(input.Source, input.Cwd),
	}
	if status == stPending {
		formEls := make([]card.CardElement, 0, len(questions)*3+1)
		for i, q := range questions {
			head := q.Header
			if head == "" {
				head = "请你定一下"
			}
			if numbered {
				head = itoa(i+1) + ". " + head
			}
			formEls = append(formEls, card.Md("🧩 **"+head+"**\n"+Clip(q.Question, 600)+(func() string {
				if q.MultiSelect {
					return "　_(可多选)_"
				}
				return ""
			})()))
			opts := make([]card.SelectOption, 0, len(q.Options))
			for _, o := range q.Options {
				opts = append(opts, card.SelectOption{Label: optionDisplay(o), Value: o.Label})
			}
			if q.MultiSelect {
				formEls = append(formEls, card.MultiSelectMenu(QuestionChoiceField(i), "可多选…", opts))
			} else {
				formEls = append(formEls, card.SelectMenu(QuestionChoiceField(i), "选一个…", opts, ""))
			}
			formEls = append(formEls, card.Input(card.InputOpts{
				Name:        QuestionCustomField(i),
				Placeholder: "都不合适？直接写这里（填了就用你写的）",
			}))
		}
		formEls = append(formEls, card.Actions([]card.CardElement{
			card.SubmitButton("✅ 提交", card.ActionValue{"a": CLI.QuestionSubmit, "id": input.ID}, card.ButtonPrimary, "submit"),
		}, shortElem("question_submit_", input.ID)))
		elements = append(elements, card.Form(shortElem("cli_question_", input.ID), formEls))
		elements = append(elements, card.Note("🐙 选项和「自己写」都在卡片里，答完点「提交」即可 —— 不用回到电脑。"))
	} else if status == stApproved {
		ans := input.Answers
		if ans == nil {
			ans = map[string]string{}
		}
		var lines string
		if len(questions) > 0 {
			parts := make([]string, 0, len(questions))
			for i, q := range questions {
				label := q.Header
				if label == "" {
					label = "回答"
				}
				if numbered {
					label = itoa(i+1) + ". " + label
				}
				v := ans[q.Question]
				if v == "" {
					v = "（未答）"
				}
				parts = append(parts, "**"+label+"**："+v)
			}
			lines = strings.Join(parts, "\n")
		} else {
			parts := make([]string, 0, len(ans))
			for k, v := range ans {
				parts = append(parts, "**"+k+"**："+v)
			}
			lines = strings.Join(parts, "\n")
		}
		elements = append(elements, card.Md("✅ 你的回答\n"+lines))
	}
	f := false
	return card.Card(elements, card.CardOpts{Forward: &f})
}

// BuildCliBridgeTaskCompletionCard 完成卡（含可选的续聊回复入口）。
func BuildCliBridgeTaskCompletionCard(input struct {
	ID             string
	Source         CliBridgeAgent
	Cwd            string
	Status         string // completed|failed
	Summary        string
	ReplyEnabled   bool
	SessionID      string
	HookEventName  string
	CreatedAt      int64
	ReplyExpiresAt int64
	ReplyDoneAt    int64
}) card.CardObject {
	verb := func() string {
		if input.ReplyDoneAt != 0 {
			return "✅ 已确认完成"
		}
		if input.Status == "failed" {
			return "❌ 任务失败"
		}
		return PickCopy(COPY.Completion, input.ID+input.Cwd)
	}()
	elements := []card.CardElement{
		titleEl(verb),
		metaLine(input.Source, input.Cwd),
	}
	summary := strings.TrimSpace(input.Summary)
	if summary != "" {
		clipped := Clip(summary, 5600)
		chunks := splitIntoChunks(clipped, taskOutputChunkSize)
		for i, chunk := range chunks {
			title := "Agent 输出"
			if len(clipped) > taskOutputChunkSize {
				title = "Agent 输出（" + itoa(i+1) + "）"
			}
			elements = append(elements, card.Md("📝 **"+title+"**\n"+codeBlock(chunk, "text")))
		}
	} else {
		elements = append(elements, card.Note("（hook 未带最终回答）"))
	}
	if input.ReplyEnabled {
		expires := ""
		if input.ReplyExpiresAt != 0 {
			expires = "（有效期至 " + time.Unix(input.ReplyExpiresAt/1000, 0).Format("2006-01-02 15:04:05") + "）"
		}
		elements = append(elements, card.Actions([]card.CardElement{
			card.Button("✅ 收工", card.ActionValue{"a": CLI.TaskCompletionDone, "id": input.ID}, card.ButtonPrimary),
		}, shortElem("task_done_", input.ID)))
		elements = append(elements, card.Note(PickCopy(COPY.FooterReply, input.ID+input.Cwd)+expires))
	} else if input.ReplyDoneAt != 0 {
		elements = append(elements, card.Actions([]card.CardElement{
			disabledButton("✅ 已收工", card.ButtonPrimary),
		}, shortElem("task_done_disabled_", input.ID)))
	}
	f := false
	return card.Card(elements, card.CardOpts{Forward: &f})
}

// CliBridgeSettingsSectionInput 「☕ 咖啡一下」设置区输入。
type CliBridgeSettingsSectionInput struct {
	Enabled    bool
	Statuses   map[CliBridgeAgent]CliHookStatus
	CanEnable  bool
	NotifyScope string
	Agents     struct {
		Claude bool
		Codex  bool
	}
	KeepAwake bool
}

// BuildCliBridgeSettingsSection 「☕ 咖啡一下」设置区元素（内联进设置卡）。
func BuildCliBridgeSettingsSection(input CliBridgeSettingsSectionInput) []card.CardElement {
	scopeButton := func(label, value string) card.CardElement {
		primary := card.ButtonDefault
		if input.NotifyScope == value {
			primary = card.ButtonPrimary
		}
		return card.Button(label, card.ActionValue{"a": CLI.SetNotifyScope, "v": value}, primary)
	}
	agentButton := func(label string, agent CliBridgeAgent, on bool) card.CardElement {
		primary := card.ButtonDefault
		if on {
			primary = card.ButtonPrimary
		}
		onStr := "on"
		if !on {
			onStr = "off"
		}
		return card.Button(label+"："+(map[bool]string{true: "开", false: "关"})[on],
			card.ActionValue{"a": CLI.ToggleAgent, "agent": agent, "v": onStr}, primary)
	}
	claudeStatus := input.Statuses[AgentClaude].Status
	codexStatus := input.Statuses[AgentCodex].Status
	elements := []card.CardElement{
		card.Hr(),
		card.Md("**☕ 咖啡一下**"),
		card.Note("去倒杯咖啡的工夫，我替你盯着本机的 Claude Code / Codex —— 它要审批、要问你、或跑完了，都推到这个私聊，你在手机上接着拍板就行。"),
		card.Actions([]card.CardElement{
			card.Button(func() string {
				if input.Enabled {
					return "咖啡一下：开"
				}
				return "咖啡一下：关"
			}(), card.ActionValue{"a": CLI.ToggleEnabled, "v": boolToOnOff(!input.Enabled)}, btnTypeOf(input.Enabled)),
		}, "cb_toggle"),
		card.Note("锁屏、或键鼠空闲超过设定时长，就当你去接咖啡了 → 自动接管；回到电脑/解锁立即收手。"),
		card.Md("**📣 通知范围**"),
		card.Note("离开时把哪些会话推到飞书。"),
		card.Actions([]card.CardElement{
			scopeButton("全部", "all"),
			scopeButton("仅绑定项目", "bound_projects"),
			scopeButton("不通知", "none"),
		}, "cb_scope"),
		card.Md("**🤖 转发哪些后端**"),
		card.Actions([]card.CardElement{
			agentButton("Claude Code", AgentClaude, input.Agents.Claude),
			agentButton("Codex", AgentCodex, input.Agents.Codex),
		}, "cb_agents"),
		card.Md("**🔋 离开保活**"),
		card.Note("离开且有任务在跑时自动顶住系统休眠（屏幕照常熄灭），回到电脑/解锁即关。仅 macOS。"),
		card.Actions([]card.CardElement{
			card.Button(func() string {
				if input.KeepAwake {
					return "离开保活：开"
				}
				return "离开保活：关"
			}(), card.ActionValue{"a": CLI.ToggleKeepAwake, "v": boolToOnOff(!input.KeepAwake)}, btnTypeOf(input.KeepAwake)),
		}, "cb_keepawake"),
		card.Md("**🔧 hooks**　Claude Code：**" + statusLabel[string(claudeStatus)] + "**　Codex：**" + statusLabel[string(codexStatus)] + "**"),
	}
	if claudeStatus == HookConflictAgent2Lark || codexStatus == HookConflictAgent2Lark {
		elements = append(elements, card.Note("⚠️ 检测到 agent2lark 的 hook；点「修复 hooks」会用本 bridge 覆盖它。"))
	}
	elements = append(elements, card.Actions([]card.CardElement{
		card.Button("修复 hooks", card.ActionValue{"a": CLI.RepairHooks}, card.ButtonPrimary),
	}, "cb_repair"))
	if input.CanEnable {
		elements = append(elements, card.Note("目标：机器人 owner 私聊　·　hooks 为本机全局，多个机器人共用一套（修复不会重复安装）。"))
	} else {
		elements = append(elements, card.Note("开启「☕ 咖啡一下」前请先设置机器人 owner。"))
	}
	return elements
}

func btnTypeOf(on bool) card.ButtonType {
	if on {
		return card.ButtonPrimary
	}
	return card.ButtonDefault
}

func boolToOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
