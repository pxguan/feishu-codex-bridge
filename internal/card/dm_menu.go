package card

// dm_menu.go —— DM 控制台菜单卡 + 版本更新卡（对齐 TS card/dm-cards 的入口部分）。
// 纯渲染器（IO 在调用方）。依赖 element builder + DM action id，全就绪。

import "fmt"

// 菜单按钮固定宽度（两行总长相等、右边缘对齐）。
const (
	MenuBtnWTop = "152px" // 3 按钮
	MenuBtnWBot = "112px" // 4 按钮
)

// 项目主页。
const RepoURL = "https://github.com/modelzen/feishu-codex-bridge"

// OpenChatURL 飞书群 applink（按 chat_id 打开群）。
func OpenChatURL(chatID string) string {
	return fmt.Sprintf("https://applink.feishu.cn/client/chat/open?openChatId=%s", chatID)
}

// KindLabel 项目会话模式标签。
func KindLabel(kind string) string {
	if kind == "single" {
		return "💬 单会话群"
	}
	return "👥 多话题群"
}

// BuildDmMenuCard DM 控制台入口菜单卡。
// webConsoleURL 非空时显示「🌐 网页控制台」行（仅本机可开）。version 非空时头部显示版本药丸。
// forward=false（卡含 dm.* 管理回调 + 可能的本机控制台链接，转发无意义）。
func BuildDmMenuCard(webConsoleURL, version string) CardObject {
	noForward := false
	elements := []CardElement{
		Md("私聊用于**建项目和管理**；具体任务请到项目群里 @我。"),
		Hr(),
		ActionsFixed([]CardElement{
			Button("➕ 新建项目", ActionValue{"a": DMNewProject}, ButtonPrimary),
			Button("📁 项目列表", ActionValue{"a": DMProjects}, ButtonDefault),
			Button("⚙️ 设置", ActionValue{"a": DMSettings}, ButtonDefault),
		}, MenuBtnWTop, ""),
		ActionsFixed([]CardElement{
			Button("📊 用量", ActionValue{"a": DMUsage}, ButtonDefault),
			Button("🩺 诊断", ActionValue{"a": DMDoctor}, ButtonDefault),
			Button("🔁 重启", ActionValue{"a": DMRestart}, ButtonDefault),
			Button("⬆️ 更新", ActionValue{"a": DMUpdate}, ButtonDefault),
		}, MenuBtnWBot, ""),
	}
	if webConsoleURL != "" {
		elements = append(elements, Hr(), SplitRow(
			LinkButton("🌐 网页控制台", webConsoleURL, ButtonDefault, "small"),
			Note("仅在**运行 bridge 的这台电脑**上能打开（本机地址）。"),
			"",
		))
	}
	opts := CardOpts{Header: &CardHeader{Title: "🤖 Codex Bridge 管理台", Template: HeaderBlue}, Forward: &noForward}
	if version != "" {
		opts.Header.TextTags = []TextTag{{Text: "v" + version, Color: "green"}}
	}
	return Card(elements, opts)
}

// BackToMenu 「⬅️ 菜单」按钮行（返回 DM 入口）。
func BackToMenu() CardElement {
	return Actions([]CardElement{Button("⬅️ 菜单", ActionValue{"a": DMMenu}, ButtonDefault)}, "")
}

// UpdateCardPhase 版本更新卡阶段。
type UpdateCardPhase string

const (
	UpdateChecking UpdateCardPhase = "checking"
	UpdateChecked  UpdateCardPhase = "checked"
	UpdateUpdating UpdateCardPhase = "updating"
	UpdateDone     UpdateCardPhase = "done"
	UpdateError    UpdateCardPhase = "error"
)

// UpdateCardState 版本更新卡状态。
type UpdateCardState struct {
	Phase       UpdateCardPhase
	Current     string
	Latest      string // 空=查不到
	HasUpdate   bool
	Dev         bool // 源码开发模式
	From        string
	To          string
	WillRestart bool
	Message     string // error phase: npm 输出尾
}

// BuildUpdateCard 版本更新卡（单 builder 渲染全部 phase，原地更新）。
func BuildUpdateCard(s UpdateCardState) CardObject {
	switch s.Phase {
	case UpdateChecking:
		return Card([]CardElement{Md("⏳ 正在查询最新版本…"), Note("从 npm registry 拉取版本信息，请稍候。")}, CardOpts{
			Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderTurquoise},
		})
	case UpdateChecked:
		cur := s.Current
		if cur == "" {
			cur = "?"
		}
		if s.Latest == "" {
			return Card([]CardElement{
				Md(fmt.Sprintf("当前版本：**v%s**", cur)),
				Md("⚠️ 查不到最新版本（网络或 npm registry 问题）。"),
				Actions([]CardElement{
					Button("🔄 重试", ActionValue{"a": DMUpdate}, ButtonDefault),
					Button("⬅️ 菜单", ActionValue{"a": DMMenu}, ButtonDefault),
				}, ""),
			}, CardOpts{Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderRed}})
		}
		if !s.HasUpdate {
			return Card([]CardElement{
				Md(fmt.Sprintf("✅ 已是最新版本：**v%s**", cur)),
				BackToMenu(),
			}, CardOpts{Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderGreen}})
		}
		head := []CardElement{
			Md("发现新版本 🎉"),
			Note(fmt.Sprintf("当前 v%s  →  最新 v%s", cur, s.Latest)),
		}
		if s.Dev {
			return Card(append(head, Md("检测到**源码开发模式**（仓库内有 .git）。请在终端用 `git pull && npm i` 更新，而不是全局安装。"), BackToMenu()), CardOpts{Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderOrange}})
		}
		return Card(append(head, Actions([]CardElement{
			Button("⬆️ 立即更新", ActionValue{"a": DMUpdateDo}, ButtonPrimary),
			BackToMenu(),
		}, "")), CardOpts{Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderOrange}})
	case UpdateUpdating:
		return Card([]CardElement{Md("⏳ 正在更新…"), Note("下载并安装新版本，期间 bridge 暂停响应。")}, CardOpts{
			Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderTurquoise},
		})
	case UpdateDone:
		els := []CardElement{Md(fmt.Sprintf("✅ 已更新：**v%s → v%s**", s.From, s.To))}
		if s.WillRestart {
			els = append(els, Note("后台 daemon 正在自动重启…"))
		}
		els = append(els, BackToMenu())
		return Card(els, CardOpts{Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderGreen}})
	case UpdateError:
		return Card([]CardElement{
			Md("⚠️ 更新失败"),
			Note(fmt.Sprintf("npm 输出末尾：\n```\n%s\n```", s.Message)),
			BackToMenu(),
		}, CardOpts{Header: &CardHeader{Title: "⬆️ 版本更新", Template: HeaderRed}})
	}
	return Card([]CardElement{Md("未知状态")}, CardOpts{})
}
