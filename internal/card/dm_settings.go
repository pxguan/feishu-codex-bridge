package card

// dm_settings.go —— 全局设置卡（对齐 TS card/dm-cards 的 buildSettingsCard / buildWatchdogCustomCard）。
// 设置项按主题分区（输出展示 / 运行控制），每项自解释（名称 + 灰字说明 + 选项按钮，
// 当前值高亮 primary）。按钮而非 select：select 一旦交互会锁 card_id，之后所有按钮失效。

import (
	"fmt"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// settingSection 分区小标题（灰、加粗）。
func settingSection(title string) CardElement {
	return Md("**" + title + "**")
}

// optionRow 标签行 + 选项按钮行（当前值高亮 primary）。
func optionRow(label string, actionID string, current string, opts []SelectOption) []CardElement {
	btns := make([]CardElement, 0, len(opts))
	for _, o := range opts {
		bt := ButtonDefault
		if o.Value == current {
			bt = ButtonPrimary
		}
		btns = append(btns, Button(o.Label, ActionValue{"a": actionID, "v": o.Value}, bt))
	}
	return []CardElement{Md(label), Actions(btns, "")}
}

// settingItem 带说明的设置项（名称 + 灰字说明 + 选项按钮行）。
func settingItem(name, desc, actionID, current string, opts []SelectOption) []CardElement {
	btns := make([]CardElement, 0, len(opts))
	for _, o := range opts {
		bt := ButtonDefault
		if o.Value == current {
			bt = ButtonPrimary
		}
		btns = append(btns, Button(o.Label, ActionValue{"a": actionID, "v": o.Value}, bt))
	}
	return []CardElement{Md("**" + name + "**"), Note(desc), Actions(btns, "")}
}

// DmSettingsInfo 全局设置卡数据。
type DmSettingsInfo struct {
	Cfg         config.AppConfig
	LocalAgents []CardElement // 预留（后端切换已移除，通常为空）
}

// BuildDmSettingsCard 全局设置卡。
func BuildDmSettingsCard(info DmSettingsInfo) CardObject {
	cfg := info.Cfg
	wd := 120
	if cfg.Preferences != nil && cfg.Preferences.RunIdleTimeoutSeconds != nil {
		wd = *cfg.Preferences.RunIdleTimeoutSeconds
	}
	var elements []CardElement
	elements = append(elements, settingSection("📤 输出展示"))
	elements = append(elements, settingItem(
		"🔧 工具调用",
		"输出时显示执行的命令 / 工具调用；关掉只看最终回答。",
		DMSetTools, boolOn(config.GetShowToolCalls(cfg)),
		[]SelectOption{{Label: "显示", Value: "on"}, {Label: "隐藏", Value: "off"}},
	)...)
	elements = append(elements, settingItem(
		"🧠 模型显示",
		"每条回复右下角显示「模型 · 推理强度」。仅输出时＝只在生成中显示；始终＝生成完后卡片也保留。",
		DMSetShowModel, config.GetModelDisplay(cfg),
		[]SelectOption{{Label: "关闭", Value: "off"}, {Label: "仅输出时", Value: "running"}, {Label: "始终", Value: "always"}},
	)...)
	elements = append(elements, Hr(), settingSection("⏱ 运行控制"))
	wdLabel := "关闭"
	if wd != 0 {
		wdLabel = fmt.Sprintf("%d秒", wd)
	}
	elements = append(elements, Md(fmt.Sprintf("**⏱ 假死超时** · 当前 **%s**", wdLabel)))
	elements = append(elements, Note("多久没有任何输出就自动终止本轮（防卡死）。"))
	wdBtns := make([]CardElement, 0, 4)
	for _, v := range []int{0, 120, 300} {
		label := "关闭"
		if v != 0 {
			label = fmt.Sprintf("%d秒", v)
		}
		bt := ButtonDefault
		if v == wd {
			bt = ButtonPrimary
		}
		wdBtns = append(wdBtns, Button(label, ActionValue{"a": DMSetWatchdog, "v": fmt.Sprintf("%d", v)}, bt))
	}
	wdBtns = append(wdBtns, Button("自定义…", ActionValue{"a": DMWatchdogCustom}, ButtonDefault))
	elements = append(elements, Actions(wdBtns, ""))
	// 🔔 任务结束提醒：四档策略 + 长任务阈值子卡。
	cr := config.GetCompletionReminderConfig(cfg)
	elements = append(elements, settingItem(
		"🔔 任务结束提醒",
		"结束后额外回复一条消息并 @ 发起人。「仅手动」会在运行卡显示提醒按钮；其他档位自动判断，不显示按钮。",
		DMSetCompletionReminder, string(cr.Mode),
		[]SelectOption{
			{Label: "仅手动", Value: "manual"},
			{Label: "长任务", Value: "long"},
			{Label: "失败或超时", Value: "failures"},
			{Label: "每次结束", Value: "always"},
		})...)
	if cr.Mode == config.ReminderLong {
		elements = append(elements,
			Md(fmt.Sprintf("**⏳ 长任务阈值** · 当前 **%d 分钟**", cr.LongTaskMinutes)),
			Note("任务耗时达到这个阈值后，结束时才会提醒。"),
			Actions([]CardElement{Button("修改阈值…", ActionValue{"a": DMCompletionReminderCustom}, ButtonDefault)}, ""),
		)
	}
	elements = append(elements, settingItem(
		"📥 运行中来新消息",
		"正在跑时你又发消息：引导＝插进当前轮纠偏；排队＝等这轮跑完再处理。",
		DMSetPending, config.GetPendingPolicy(cfg),
		[]SelectOption{{Label: "引导", Value: "steer"}, {Label: "排队", Value: "queue"}},
	)...)
	elements = append(elements, settingItem(
		"⚡ 并发上限",
		"所有群 / 话题全局同时最多跑几个，满了排队（排队卡可 ⏹ 取消）。改后需重启生效。",
		DMSetConcurrency, fmt.Sprintf("%d", config.GetMaxConcurrentRuns(cfg)),
		[]SelectOption{{Label: "1", Value: "1"}, {Label: "5", Value: "5"}, {Label: "10", Value: "10"}, {Label: "20", Value: "20"}},
	)...)
	if len(info.LocalAgents) > 0 {
		elements = append(elements, info.LocalAgents...)
	}
	// 📂 项目根目录（空白项目默认父目录）。
	rootCur := config.ProjectsRootDir()
	if cfg.Preferences != nil && cfg.Preferences.ProjectsRootDir != "" {
		rootCur = cfg.Preferences.ProjectsRootDir
	}
	elements = append(elements, Hr(),
		Md("📂 **项目根目录**"),
		Note("新建空白项目时，代码目录默认放在此父目录下（子目录名=项目名）。留空=默认 "+config.ProjectsRootDir()+"；支持 ~ 或绝对路径。"),
		Form("projects_root_dir", []CardElement{
			Input(InputOpts{Name: "dir", Label: "项目根目录", Value: rootCur, Placeholder: config.ProjectsRootDir()}),
			Actions([]CardElement{SubmitButton("✅ 保存", ActionValue{"a": DMSetProjectsRootDir}, ButtonPrimary, "submit_rootdir")}, ""),
		}),
	)
	// 子卡入口：云文档评论 @bot + ☕ 咖啡一下（cli-bridge），各自独立子卡。
	elements = append(elements, Hr(), Actions([]CardElement{
		Button("📝 云文档评论", ActionValue{"a": DMCommentSettings}, ButtonDefault),
		Button("☕ 咖啡一下", ActionValue{"a": DMCoffeeSettings}, ButtonDefault),
	}, ""))
	elements = append(elements, Hr(), Actions([]CardElement{
		Button("👮 管理员", ActionValue{"a": DMAdmins}, ButtonDefault),
		BackToMenu(),
	}, ""))
	return Card(elements, CardOpts{Header: &CardHeader{Title: "⚙️ 全局设置", Template: HeaderBlue}})
}

// BuildWatchdogCustomCard 自定义假死超时输入卡。
func BuildWatchdogCustomCard(cfg config.AppConfig, min, max int) CardObject {
	cur := 120
	if cfg.Preferences != nil && cfg.Preferences.RunIdleTimeoutSeconds != nil {
		cur = *cfg.Preferences.RunIdleTimeoutSeconds
	}
	if min <= 0 {
		min = 30
	}
	if max <= 0 {
		max = 3600
	}
	elements := []CardElement{
		Md("**自定义假死超时**"),
		Note(fmt.Sprintf("多少秒没有任何输出就自动终止本轮。范围 %d–%d 秒；填 0 关闭。", min, max)),
		Form("watchdog_custom", []CardElement{
			Input(InputOpts{Name: "sec", Label: "超时秒数", Placeholder: fmt.Sprintf("%d", cur), Value: fmt.Sprintf("%d", cur), Required: true}),
			Actions([]CardElement{SubmitButton("✅ 保存", ActionValue{"a": DMWatchdogCustomSubmit}, ButtonPrimary, "submit_watchdog")}, ""),
		}),
		Actions([]CardElement{Button("⬅️ 返回设置", ActionValue{"a": DMSettings}, ButtonDefault)}, ""),
	}
	return Card(elements, CardOpts{Header: &CardHeader{Title: "⏱ 自定义超时", Template: HeaderBlue}})
}

// boolOn 把 bool 转 settingItem 的当前值（on/off）。
func boolOn(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// BuildCompletionReminderCustomCard 长任务阈值自定义输入卡。
func BuildCompletionReminderCustomCard(cfg config.AppConfig) CardObject {
	cr := config.GetCompletionReminderConfig(cfg)
	cur := cr.LongTaskMinutes
	return Card([]CardElement{
		Md("**自定义长任务阈值**"),
		Note(fmt.Sprintf("任务耗时达到这个时长，结束时额外回复消息并 @ 发起人。范围 %d–%d 分钟。",
			config.CompletionReminderLongTaskMinMinutes, config.CompletionReminderLongTaskMaxMinutes)),
		Form("completion_reminder_custom", []CardElement{
			Input(InputOpts{Name: "minutes", Label: "时长（分钟）", Placeholder: fmt.Sprintf("%d", cur), Value: fmt.Sprintf("%d", cur), Required: true}),
			Actions([]CardElement{SubmitButton("✅ 保存", ActionValue{"a": DMCompletionReminderCustomSubmit}, ButtonPrimary, "submit_completion_reminder")}, ""),
		}),
		Actions([]CardElement{Button("⬅️ 返回设置", ActionValue{"a": DMSettings}, ButtonDefault)}, ""),
	}, CardOpts{Header: &CardHeader{Title: "🔔 长任务阈值", Template: HeaderBlue}})
}
