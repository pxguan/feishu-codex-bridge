package card

// dm_newproject.go —— DM 建项目 / 绑定存量群 表单卡（对齐 TS card/dm-cards）。
// BuildNewProjectFormCard: 项目名 + 可选 cwd + 后端 Agent 下拉（>1 时）+ 多话题/单会话双提交按钮。
// BuildJoinGroupFormCard: 被拉进群后弹出的绑定表单（群名预填、提交带 chatId）。

import (
	"fmt"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// NewProjectFormOpts 新建项目表单输入。
type NewProjectFormOpts struct {
	Name      string
	Cwd       string
	Error     string
	Backends  []SelectOption // 可选后端 Agent（>1 时显示下拉，=1 时静态提示）
}

// BuildNewProjectFormCard 交互式新建项目表单。
func BuildNewProjectFormCard(opts NewProjectFormOpts) CardObject {
	var elements []CardElement
	if opts.Error != "" {
		elements = append(elements, Md(fmt.Sprintf("❌ **创建失败**：%s", opts.Error)))
	}
	backends := opts.Backends
	formItems := []CardElement{
		Input(InputOpts{Name: "name", Label: "项目名", Placeholder: "my-app", Value: opts.Name, Required: true}),
		Input(InputOpts{Name: "cwd", Label: "文件夹路径（选填，留空自动新建）", Placeholder: "/Users/you/code/my-app", Value: opts.Cwd}),
	}
	if len(backends) > 1 {
		formItems = append(formItems,
			Note("🧠 后端 Agent（创建后**固定不可切换**；标注「未下载」的需先去 Web「后端 Agent」页下载，选它会提示）"),
			SelectMenu("backend", "选择后端 Agent", backends, backends[0].Value),
		)
	} else if len(backends) == 1 {
		formItems = append(formItems, Note(fmt.Sprintf("🧠 后端 Agent：**%s**（创建后固定）", backends[0].Label)))
	}
	formItems = append(formItems,
		Note("选群类型(直接点对应按钮创建)：👥 多话题群 = @我开话题、每话题独立会话；💬 单会话群 = 整群一个会话、连续上下文。"),
		Actions([]CardElement{
			SubmitButton("👥 创建·多话题群", ActionValue{"a": DMNewProjectSubmit, "kind": "multi"}, ButtonPrimary, "submit_multi"),
			SubmitButton("💬 创建·单会话群", ActionValue{"a": DMNewProjectSubmit, "kind": "single"}, ButtonPrimary, "submit_single"),
		}, ""),
		Actions([]CardElement{Button("⬅️ 菜单", ActionValue{"a": DMMenu}, ButtonDefault)}, ""),
	)
	elements = append(elements,
		Md("填项目名（必填）。**文件夹路径留空** = 自动在默认位置新建一个空白项目；**填绝对路径** = 用电脑上已有的文件夹。"),
		Form("new_project", formItems),
	)
	return Card(elements, CardOpts{Header: &CardHeader{Title: "➕ 新建项目", Template: HeaderTurquoise}})
}

// JoinGroupFormOpts 绑定存量群表单输入。
type JoinGroupFormOpts struct {
	ChatID   string
	Name     string
	Cwd      string
	Error    string
	Backends []SelectOption
}

// BuildJoinGroupFormCard 绑定已有群表单（bot 被拉进群后 DM 群主）。
func BuildJoinGroupFormCard(opts JoinGroupFormOpts) CardObject {
	var elements []CardElement
	if opts.Error != "" {
		elements = append(elements, Md(fmt.Sprintf("❌ **绑定失败**：%s", opts.Error)))
	}
	backends := opts.Backends
	formItems := []CardElement{
		Input(InputOpts{Name: "name", Label: "项目名", Placeholder: "my-app", Value: opts.Name, Required: true}),
		Input(InputOpts{Name: "cwd", Label: "文件夹路径（选填，留空自动新建）", Placeholder: "/Users/you/code/my-app", Value: opts.Cwd}),
	}
	if len(backends) > 1 {
		formItems = append(formItems,
			Note("🧠 后端 Agent（绑定后**固定不可切换**）。默认 **Codex** 以「只读」档绑定（外部群安全）。"),
			SelectMenu("backend", "选择后端 Agent", backends, backends[0].Value),
		)
	} else if len(backends) == 1 {
		formItems = append(formItems, Note(fmt.Sprintf("🧠 后端 Agent：**%s**（绑定后固定）", backends[0].Label)))
	}
	formItems = append(formItems,
		Note("选群类型(直接点对应按钮创建)：👥 多话题群 = @我开话题、每话题独立会话；💬 单会话群 = 整群一个会话、连续上下文（默认不免@）。"),
		Actions([]CardElement{
			SubmitButton("👥 绑定·多话题群", ActionValue{"a": DMJoinGroupSubmit, "kind": "multi", "chatId": opts.ChatID}, ButtonPrimary, "submit_multi"),
			SubmitButton("💬 绑定·单会话群", ActionValue{"a": DMJoinGroupSubmit, "kind": "single", "chatId": opts.ChatID}, ButtonPrimary, "submit_single"),
		}, ""),
	)
	elements = append(elements,
		Md("我已被加入这个群。填一下要绑定的项目信息即可开始用。"),
		Md("项目名默认用群名，可改。**文件夹路径留空** = 自动新建空白项目；**填绝对路径** = 用电脑上已有的文件夹。"),
		Form("join_group", formItems),
	)
	return Card(elements, CardOpts{Header: &CardHeader{Title: "🔗 绑定已有群", Template: HeaderTurquoise}})
}

// NewProjectDoneInfo 建项目/绑定完成卡数据。
type NewProjectDoneInfo struct {
	Name      string
	ChatID    string
	Cwd       string
	Kind      string
	Backend   string
	Blank     bool
	Origin    string // created(默认) | joined
}

// BuildNewProjectDoneCard 建项目/绑定完成「留痕」卡（带进群链接 + 项目设置入口）。
func BuildNewProjectDoneCard(info NewProjectDoneInfo) CardObject {
	joined := info.Origin == "joined"
	verb := "已绑定群"
	title := "🔗 绑定已有群"
	if !joined {
		verb = "已创建项目"
		title = "➕ 新建项目"
	}
	backendName := info.Backend
	if backendName == "" {
		backendName = agent.DEFAULT_BACKEND_ID
	}
	if entry, ok := agent.CatalogByID(backendName); ok {
		backendName = entry.DisplayName
	}
	elements := []CardElement{
		Md(fmt.Sprintf("✅ %s **%s**%s", verb, info.Name, boolMark(info.Blank, " _(空白项目)_"))),
		Note(fmt.Sprintf("📂 `%s`   ·   %s   ·   🧠 %s", info.Cwd, KindLabel(info.Kind), backendName)),
		Md(orElse(info.ChatID != "", "👉 去群里 **@我** 干活。", "发我任意消息可再次打开管理台。")),
	}
	if info.ChatID != "" {
		elements = append(elements, Actions([]CardElement{
			LinkButton("💬 打开群聊", OpenChatURL(info.ChatID), ButtonPrimary, ""),
			Button("⚙️ 项目设置", ActionValue{"a": DMProjectSettings, "n": info.Name}, ButtonDefault),
		}, ""))
	}
	return Card(elements, CardOpts{Header: &CardHeader{Title: title, Template: HeaderGreen}})
}

// ── 小工具 ──

func boolMark(b bool, s string) string {
	if b {
		return s
	}
	return ""
}

func orElse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
