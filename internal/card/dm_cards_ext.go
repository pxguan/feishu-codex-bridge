package card

// dm_cards_ext.go —— DM 项目设置容器 + 权限/模型/管理员/白名单/删除/重连 等卡（对齐 TS dm-cards）。
// 纯渲染，数据由 handler 收集后传入。

import (
	"fmt"

	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// ── 权限档 ──

// tier 选项（升序；含中文说明，下拉里 label — desc 展示）。
var tierOpts = []SelectOption{
	{Value: "qa", Label: "🔒 项目内只读 — 只能查看项目文件夹里的内容，不会改任何文件"},
	{Value: "write", Label: "✏️ 项目内读写 — 能查看并修改项目文件夹里的文件，但碰不到文件夹外"},
	{Value: "full", Label: "⚠️ 完全访问 — 能读写整台电脑上的任何文件"},
}

func tierLabel(m string) string {
	switch m {
	case "qa":
		return "🔒 项目内只读"
	case "write":
		return "✏️ 项目内读写"
	case "full":
		return "⚠️ 完全访问"
	}
	return m
}

// PermissionSummary 项目权限档单行摘要（管理员档 / 普通用户档）。
func PermissionSummary(p project.Project) string {
	admin := project.EffectiveMode(p)
	guest := project.EffectiveGuestMode(p)
	if admin == guest {
		return "所有人：" + tierLabel(string(admin))
	}
	return "管理员：" + tierLabel(string(admin)) + "　·　其他人：" + tierLabel(string(guest))
}

// ── 模型 / 推理强度 ──

var effortLabelMap = map[string]string{
	"none":    "无",
	"minimal": "极低",
	"low":     "低",
	"medium":  "中",
	"high":    "高",
	"xhigh":   "极高",
}

func effortLabel(e string) string {
	if l, ok := effortLabelMap[e]; ok {
		return l
	}
	return e
}

// ModelDefaultSummary 项目默认模型/强度单行摘要。
func ModelDefaultSummary(p project.Project) string {
	if p.DefaultModel == "" {
		return "后端默认（未设）"
	}
	eff := ""
	if p.DefaultEffort != "" {
		eff = " · 强度 " + effortLabel(string(p.DefaultEffort))
	}
	return p.DefaultModel + eff
}

// ModelRow 一个可选模型（id + 展示名 + 支持的推理强度）。
type ModelRow struct {
	ID               string
	DisplayName      string
	SupportedEfforts []string
}

// ── 行内成员名 ──

func memberName(names map[string]string, id string) string {
	if names != nil {
		if n, ok := names[id]; ok && n != "" {
			return n
		}
	}
	if len(id) > 6 {
		return "…" + id[len(id)-6:]
	}
	return id
}

// ── 项目设置容器 ──

// ProjectSettingsInfo 项目设置卡数据。
type ProjectSettingsInfo struct {
	Project      project.Project
	BackendName  string
	Notice       string
}

// BuildProjectSettingsCard 项目设置容器（权限 / 后端 / 免@ / 自动压缩 / 默认模型 / 白名单）。
func BuildProjectSettingsCard(info ProjectSettingsInfo) CardObject {
	p := info.Project
	noMention := project.DefaultNoMention(p)
	autoCompact := true
	if p.AutoCompact != nil {
		autoCompact = *p.AutoCompact
	}
	var elements []CardElement
	if info.Notice != "" {
		elements = append(elements, Md(info.Notice))
	}
	elements = append(elements,
		Md(fmtTitle("项目设置", p.Name)),
		Note(fmt.Sprintf("%s%s", KindLabel(p.Kind), ternary(p.Cwd != "", "   ·   📂 `"+p.Cwd+"`", ""))),
		Hr(),
		Actions([]CardElement{Button("🔐 权限", ActionValue{"a": DMPermission, "n": p.Name}, ButtonPrimary)}, ""),
		Note(fmt.Sprintf("当前 %s　·　codex 沙箱可访问的范围（管理员 / 普通用户可分设）。", PermissionSummary(p))),
		Hr(),
		Md("🧠 后端"),
		Note(fmt.Sprintf("当前 %s 🔒　·　后端在**新建项目时选定**，运行时固定、不支持切换。如需更改，请删除该项目后用新后端重新创建。", orElseStr(info.BackendName != "", info.BackendName, p.Backend))),
		Hr(),
		Md("✋ 免@（不用 @ 也回复）"),
		Actions([]CardElement{
			Button("开", ActionValue{"a": DMSetNoMentionDm, "v": "on", "n": p.Name}, boolBtn(noMention)),
			Button("关", ActionValue{"a": DMSetNoMentionDm, "v": "off", "n": p.Name}, boolBtn(!noMention)),
		}, ""),
		Note(ternary(p.Kind == "single",
			"开启后：本群所有消息(不用 @)都交给我处理。",
			"开启后：话题内消息(不用 @)都处理；**开新话题仍需 @我**。")),
		Hr(),
		Md("🗜️ 自动压缩上下文"),
		Actions([]CardElement{
			Button("开", ActionValue{"a": DMSetAutoCompactDm, "v": "on", "n": p.Name}, boolBtn(autoCompact)),
			Button("关", ActionValue{"a": DMSetAutoCompactDm, "v": "off", "n": p.Name}, boolBtn(!autoCompact)),
		}, ""),
		Note("开启后：上下文接近上限时 Codex 自动总结早前对话、释放空间（默认开）。改动下一轮会话生效。"),
		Hr(),
		Md("🤖 默认模型 / 推理强度"),
		Actions([]CardElement{Button("设置默认模型", ActionValue{"a": DMModelDefault, "n": p.Name}, ButtonPrimary)}, ""),
		Note(fmt.Sprintf("当前 %s　·　新话题的起始模型 / 推理强度（话题内 `/model` 可临时改）。", ModelDefaultSummary(p))),
		Hr(),
		Actions([]CardElement{Button("🛡 响应白名单", ActionValue{"a": DMAllowlist, "n": p.Name}, ButtonPrimary)}, ""),
		Note("设置谁能让我在本群响应 / 跑 codex（空 = 所有人）。"),
		Hr(),
		Actions([]CardElement{Button("⬅️ 项目列表", ActionValue{"a": DMProjects}, ButtonDefault)}, ""),
	)
	return Card(elements, CardOpts{Header: &CardHeader{Title: "⚙️ 项目设置", Template: HeaderBlue}})
}

// ── 权限表单 ──

// PermissionCardInfo 权限表单卡数据。
type PermissionCardInfo struct {
	Project project.Project
}

// BuildPermissionCard 权限档表单（管理员档 / 普通用户档 / 联网）。
func BuildPermissionCard(info PermissionCardInfo) CardObject {
	p := info.Project
	network := false
	if p.Network != nil {
		network = *p.Network
	}
	elements := []CardElement{
		Md(fmtTitle("🔐 权限", p.Name)),
		Note("codex 沙箱的访问范围。「管理员档」给 owner / 管理员，「普通用户档」给群里其他人。两档**不同**时，两类人各用独立线程（互不串沙箱与对话历史）；**相同**则所有人一致。"),
		Form("perm", []CardElement{
			Md("👑 **管理员档**"),
		SelectMenu("mode", "选择管理员权限档", tierOpts, string(project.EffectiveMode(p))),
		Md("👥 **普通用户档**"),
		SelectMenu("guestMode", "选择普通用户权限档", tierOpts, string(project.EffectiveGuestMode(p))),
		Md("🌐 **联网**（只对只读 / 读写档有意义；完全访问恒联网）"),
		SelectMenu("network", "联网开关", []SelectOption{
			{Label: "关（默认，更安全）", Value: "off"},
			{Label: "开", Value: "on"},
		}, boolStr(network)),
			Actions([]CardElement{SubmitButton("✅ 保存权限", ActionValue{"a": DMPermissionSubmit, "n": p.Name}, ButtonPrimary, "submit_perm")}, ""),
		}),
		Note("保存会断开本项目正在进行的会话，让新档位立即生效。"),
		Actions([]CardElement{Button("⬅️ 返回设置", ActionValue{"a": DMProjectSettings, "n": p.Name}, ButtonDefault)}, ""),
	}
	return Card(elements, CardOpts{Header: &CardHeader{Title: "🔐 权限", Template: HeaderBlue}})
}

// ── 默认模型 / 强度表单 ──

// ModelDefaultCardInfo 默认模型卡数据。
type ModelDefaultCardInfo struct {
	Project        project.Project
	Models         []ModelRow
	UnionEfforts   []string
	Ctx            string // "dm" | "group"
	Notice         string
}

// BuildModelDefaultCard 默认模型 / 推理强度子表单（项目级 / 群内共用）。
func BuildModelDefaultCard(info ModelDefaultCardInfo) CardObject {
	visible := info.Models
	// 当前有效默认：显式 defaultModel（若仍可见）否则首个模型。
	var curModel *ModelRow
	for i := range visible {
		if visible[i].ID == info.Project.DefaultModel {
			curModel = &visible[i]
			break
		}
	}
	if curModel == nil && len(visible) > 0 {
		curModel = &visible[0]
	}
	curEfforts := curModel.SupportedEfforts
	curEffort := string(info.Project.DefaultEffort)
	if curEffort != "" {
		has := false
		for _, e := range curEfforts {
			if e == curEffort {
				has = true
				break
			}
		}
		if !has {
			curEffort = ""
		}
	}
	if curEffort == "" && curModel != nil {
		curEffort = curModel.SupportedEfforts0()
	}
	canPickModel := len(visible) > 1
	canPickEffort := len(info.UnionEfforts) > 0

	submit := ActionValue{"a": DMModelDefaultSubmit, "n": info.Project.Name}
	back := ActionValue{"a": DMProjectSettings, "n": info.Project.Name}
	if info.Ctx == "group" {
		submit = ActionValue{"a": GSModelDefaultSubmit}
		back = ActionValue{"a": GSSettings}
	}

	head := []CardElement{}
	if info.Notice != "" {
		head = append(head, Md(info.Notice))
	}
	head = append(head, Md(fmtTitle("🤖 默认模型 / 推理强度", info.Project.Name)),
		Note("本项目**新话题**的起始模型与推理强度。进行中 / 已恢复的会话不受影响；话题内随时可用 `/model` 临时改。未设时用后端自带默认。"))

	if !canPickModel && !canPickEffort {
		return Card(append(head,
			Hr(),
			Md("当前模型：**"+orElseStr(curModel != nil && curModel.DisplayName != "", curModel.DisplayName, info.Project.DefaultModel)+"**"),
			Note("该后端只有一个模型且不支持调节推理强度，无需设置默认。"),
			Actions([]CardElement{Button("⬅️ 返回", back, ButtonDefault)}, ""),
		), CardOpts{Header: &CardHeader{Title: "🤖 默认模型", Template: HeaderBlue}})
	}

	formEls := []CardElement{}
	if canPickModel {
		opts := make([]SelectOption, 0, len(visible))
		for _, m := range visible {
			opts = append(opts, SelectOption{Label: m.DisplayName, Value: m.ID})
		}
		init := ""
		if curModel != nil {
			init = curModel.ID
		}
		formEls = append(formEls, Md("🤖 **默认模型**"), SelectMenu("model", "选择默认模型", opts, init))
	}
	if canPickEffort {
		opts := make([]SelectOption, 0, len(info.UnionEfforts))
		for _, e := range info.UnionEfforts {
			opts = append(opts, SelectOption{Label: "强度：" + effortLabel(e), Value: e})
		}
		formEls = append(formEls, Md("🧠 **默认推理强度**"), SelectMenu("effort", "选择默认推理强度", opts, curEffort))
	}
	formEls = append(formEls, Actions([]CardElement{SubmitButton("✅ 保存默认", submit, ButtonPrimary, "submit_model_default")}, ""))

	extra := []CardElement{}
	if !canPickModel {
		extra = append(extra, Md("默认模型：**"+orElseStr(curModel != nil && curModel.DisplayName != "", curModel.DisplayName, "后端默认")+"**（该后端仅一个模型）"))
	}
	if canPickModel && !canPickEffort {
		extra = append(extra, Note("该后端不调节推理强度（思考由模型自动调度，无 effort 档）。"))
	}

	return Card(append(append(head, Hr()), append(extra, Form("model_default", formEls),
		Note("保存只影响之后新建的话题，不会打断正在进行的会话。"),
		Actions([]CardElement{Button("⬅️ 返回", back, ButtonDefault)}, ""),
	)...), CardOpts{Header: &CardHeader{Title: "🤖 默认模型", Template: HeaderBlue}})
}

// SupportedEfforts0 取模型支持强度的第一个（默认强度）。
func (m *ModelRow) SupportedEfforts0() string {
	if len(m.SupportedEfforts) > 0 {
		return m.SupportedEfforts[0]
	}
	return ""
}

// ── 管理员名单 / 添加 ──

// AdminsInfo 管理员名单卡数据。
type AdminsInfo struct {
	OwnerOpenID string
	Admins      []string
	Names       map[string]string
}

// BuildAdminsCard 全局管理员名单卡。
func BuildAdminsCard(info AdminsInfo) CardObject {
	elements := []CardElement{Md("**管理员名单** · 本 bot 全局（可私聊管理 / 建项目 / 销毁操作）"), Hr()}
	seen := map[string]bool{}
	if info.OwnerOpenID != "" {
		seen[info.OwnerOpenID] = true
		elements = append(elements, Actions([]CardElement{Md("👑 **" + memberName(info.Names, info.OwnerOpenID) + "** · Bot 拥有者（注册者）")}, ""))
	}
	extra := 0
	for _, id := range info.Admins {
		if seen[id] {
			continue
		}
		seen[id] = true
		extra++
		elements = append(elements, Actions([]CardElement{
			Md(memberName(info.Names, id)),
			Button("🗑 移除", ActionValue{"a": DMRmAdmin, "u": id}, ButtonDanger),
		}, ""))
	}
	if extra == 0 {
		elements = append(elements, Note("暂无额外管理员。"))
	}
	elements = append(elements, Hr(),
		Actions([]CardElement{
			Button("➕ 添加管理员", ActionValue{"a": DMAddAdminForm}, ButtonPrimary),
			Button("⬅️ 设置", ActionValue{"a": DMSettings}, ButtonDefault),
		}, ""),
		Note("👑 Bot 拥有者（注册此 bot 的人）恒为管理员，不可移除；名单为空时仅拥有者可管理。"),
	)
	return Card(elements, CardOpts{Header: &CardHeader{Title: "👮 管理员", Template: HeaderBlue}})
}

// MemberInput 可选项成员（添加管理员 / 白名单用）。
type MemberInput struct {
	OpenID string
	Name   string
}

// AddAdminInfo 添加管理员表单数据。
type AddAdminInfo struct {
	Members []MemberInput
}

// BuildAddAdminCard 添加管理员表单（候选成员下拉 + open_id 手填兜底）。
func BuildAddAdminCard(info AddAdminInfo) CardObject {
	const max = 50
	shown := info.Members
	if len(shown) > max {
		shown = shown[:max]
	}
	formEls := []CardElement{}
	if len(shown) > 0 {
		opts := make([]SelectOption, 0, len(shown))
		for _, m := range shown {
			opts = append(opts, SelectOption{Label: m.Name, Value: m.OpenID})
		}
		formEls = append(formEls, SelectMenu("member", "从项目群成员选择", opts, ""))
	}
	formEls = append(formEls,
		Input(InputOpts{Name: "open_id", Label: ternary(len(shown) > 0, "或直接输入 open_id", "输入 open_id（未读取到项目群成员）"), Placeholder: "ou_xxx"}),
		Actions([]CardElement{SubmitButton("✅ 确认添加", ActionValue{"a": DMAddAdminSubmit}, ButtonPrimary, "submit_admin")}, ""),
	)
	tail := []CardElement{}
	if len(info.Members) > max {
		tail = append(tail, Note(fmt.Sprintf("候选较多，仅列前 %d 个；其余请直接输入 open_id。", max)))
	}
	return Card(append([]CardElement{
		Md("**添加管理员** · 从项目群成员选，或输入 open_id"),
		Form("add_admin", formEls),
	}, append(tail, Actions([]CardElement{Button("⬅️ 取消", ActionValue{"a": DMAdmins}, ButtonDefault)}, ""))...),
		CardOpts{Header: &CardHeader{Title: "➕ 添加管理员", Template: HeaderBlue}})
}

// ── 响应白名单 ──

// AllowlistInfo 响应白名单卡数据。
type AllowlistInfo struct {
	ProjectName string
	Users       []string
	Names       map[string]string
}

// BuildAllowlistCard 响应白名单卡。
func BuildAllowlistCard(info AllowlistInfo) CardObject {
	elements := []CardElement{
		Md(fmt.Sprintf("**响应白名单** · %s", info.ProjectName)),
		Note("谁能让我在本群响应 / 跑 codex"),
		Hr(),
	}
	if len(info.Users) == 0 {
		elements = append(elements, Note("当前**所有人**可用（管理员始终可用）。"))
	} else {
		for _, id := range info.Users {
			elements = append(elements, Actions([]CardElement{
				Md(memberName(info.Names, id)),
				Button("🗑 移除", ActionValue{"a": DMRmAllowed, "u": id, "n": info.ProjectName}, ButtonDanger),
			}, ""))
		}
	}
	elements = append(elements, Hr(),
		Actions([]CardElement{
			Button("➕ 添加", ActionValue{"a": DMAddAllowedForm, "n": info.ProjectName}, ButtonPrimary),
			Button("⬅️ 设置", ActionValue{"a": DMProjectSettings, "n": info.ProjectName}, ButtonDefault),
		}, ""),
		Note("管理员始终可用，不受此名单限制；名单为空 = 所有人可用。"),
	)
	return Card(elements, CardOpts{Header: &CardHeader{Title: "🛡 响应白名单", Template: HeaderBlue}})
}

// AddAllowedInfo 添加白名单成员表单数据。
type AddAllowedInfo struct {
	ProjectName string
	Members     []MemberInput
}

// BuildAddAllowedCard 添加白名单成员表单。
func BuildAddAllowedCard(info AddAllowedInfo) CardObject {
	const max = 50
	shown := info.Members
	if len(shown) > max {
		shown = shown[:max]
	}
	formEls := []CardElement{}
	if len(shown) > 0 {
		opts := make([]SelectOption, 0, len(shown))
		for _, m := range shown {
			opts = append(opts, SelectOption{Label: m.Name, Value: m.OpenID})
		}
		formEls = append(formEls, SelectMenu("member", "从群成员选择", opts, ""))
	}
	formEls = append(formEls,
		Input(InputOpts{Name: "open_id", Label: ternary(len(shown) > 0, "或直接输入 open_id", "输入 open_id（未读取到群成员）"), Placeholder: "ou_xxx"}),
		Actions([]CardElement{SubmitButton("✅ 确认添加", ActionValue{"a": DMAddAllowedSubmit, "n": info.ProjectName}, ButtonPrimary, "submit_allowed")}, ""),
	)
	tail := []CardElement{}
	if len(info.Members) > max {
		tail = append(tail, Note(fmt.Sprintf("群成员较多，仅列前 %d 个；其余请直接输入 open_id。", max)))
	}
	return Card(append([]CardElement{
		Md(fmt.Sprintf("**添加可使用「%s」的人**", info.ProjectName)),
		Form("add_allowed", formEls),
	}, append(tail, Actions([]CardElement{Button("⬅️ 取消", ActionValue{"a": DMAllowlist, "n": info.ProjectName}, ButtonDefault)}, ""))...),
		CardOpts{Header: &CardHeader{Title: "➕ 添加白名单成员", Template: HeaderBlue}})
}

// ── 删除确认 ──

// RmConfirmInfo 删除确认卡数据。
type RmConfirmInfo struct {
	Name   string
	Origin string // created | joined
}

// BuildRmConfirmCard 删除项目确认卡。
func BuildRmConfirmCard(info RmConfirmInfo) CardObject {
	note := "仅解绑（移除注册 + 撤销置顶横幅），**不删代码目录**。群主会转给你，再由你自行在飞书解散群。"
	if info.Origin == "joined" {
		note = "仅解绑（移除注册），**不删代码目录**。确认后**我会退出该群**（群是你们的，不会解散）。"
	}
	return Card([]CardElement{
		Md(fmt.Sprintf("确定删除项目 **%s**？", info.Name)),
		Note(note),
		Actions([]CardElement{
			Button("✅ 确认删除", ActionValue{"a": DMRmDo, "n": info.Name}, ButtonDanger),
			Button("取消", ActionValue{"a": DMRmCancel}, ButtonDefault),
		}, ""),
	}, CardOpts{Header: &CardHeader{Title: "🗑 删除项目", Template: HeaderRed}})
}

// ── 重连 ──

// BuildReconnectCard 长连接状态卡（只读）。
func BuildReconnectCard(conn string) CardObject {
	tmpl := HeaderGreen
	if conn != "connected" {
		tmpl = HeaderOrange
	}
	return Card([]CardElement{
		Md(fmt.Sprintf("长连接状态：**%s**", conn)),
		Note("SDK 会自动重连；若长期断开，请在终端重跑 `feishu-codex-bridge run`（前台）或 `feishu-codex-bridge restart`（后台守护）。"),
		BackToMenu(),
	}, CardOpts{Header: &CardHeader{Title: "🔄 长连接", Template: tmpl}})
}

// ── 重启 ──

// BuildRestartConfirmCard 重启确认卡（两步确认，防误触）。
func BuildRestartConfirmCard(conn string) CardObject {
	return Card([]CardElement{
		Md(fmt.Sprintf("长连接状态：**%s**", conn)),
		Note("重启会**断开当前所有会话**并重新拉起后台服务（约数秒，其间机器人短暂离线），完成后自动恢复。仅在长期断连或异常时才需要。"),
		Actions([]CardElement{
			Button("🔁 确认重启", ActionValue{"a": DMRestartDo}, ButtonDanger),
			Button("取消", ActionValue{"a": DMMenu}, ButtonDefault),
		}, ""),
	}, CardOpts{Header: &CardHeader{Title: "🔁 重启后台服务", Template: HeaderOrange}})
}

// BuildRestartingCard 正在重启 / 前台运行提示卡。mode=foreground 表示没有可重启的后台守护服务。
func BuildRestartingCard(mode string) CardObject {
	if mode == "foreground" {
		return Card([]CardElement{
			Md("ℹ️ 当前为**前台运行**（非后台守护服务）。"),
			Note("此按钮只重启由 `feishu-codex-bridge start` 安装的后台服务。前台运行请在其终端里 Ctrl+C 后重跑 `feishu-codex-bridge run`。"),
			BackToMenu(),
		}, CardOpts{Header: &CardHeader{Title: "🔁 重启后台服务", Template: HeaderOrange}})
	}
	return Card([]CardElement{
		Md("🔁 正在重启后台服务…"),
		Note("机器人将短暂离线数秒后自动恢复；本卡不再更新。"),
	}, CardOpts{Header: &CardHeader{Title: "🔁 重启后台服务", Template: HeaderOrange}})
}

// ── 群内 /settings 卡 ──

// GroupSettingsInfo 群内设置卡数据。
type GroupSettingsInfo struct {
	Project project.Project
}

// BuildGroupSettingsCard 群内设置卡（@bot /settings）：免@ / 自动压缩 / 默认模型入口。
func BuildGroupSettingsCard(info GroupSettingsInfo) CardObject {
	p := info.Project
	kind := p.Kind
	if kind == "" {
		kind = "multi"
	}
	noMention := project.DefaultNoMention(p)
	autoCompact := true
	if p.AutoCompact != nil {
		autoCompact = *p.AutoCompact
	}
	scopeNote := "开启后：本群所有消息(不用 @)都交给我处理。"
	if kind == "multi" {
		scopeNote = "开启后：话题内的消息(不用 @)都交给我处理；**开新话题仍需 @我**。"
	}
	elements := []CardElement{
		Md(fmtTitle("群设置", p.Name)),
		Note(fmt.Sprintf("群类型(建群时定，不可改)：%s", KindLabel(kind))),
	}
	elements = append(elements, optionRow("✋ 免@（不用 @ 也回复）", GSSetNoMention, boolStr(noMention), []SelectOption{
		{Label: "开", Value: "on"},
		{Label: "关", Value: "off"},
	})...)
	elements = append(elements,
		Note(scopeNote),
		Note("⚠️ 免@ 需应用已开通「接收群内所有消息」(im:message.group_msg)权限，否则收不到非 @ 消息。"),
	)
	elements = append(elements, optionRow("🗜️ 自动压缩上下文", GSSetAutoCompact, boolStr(autoCompact), []SelectOption{
		{Label: "开", Value: "on"},
		{Label: "关", Value: "off"},
	})...)
	elements = append(elements,
		Note("开启后：上下文接近上限时 Codex 自动总结早前对话、释放空间（默认开）。改动下一轮会话生效。"),
		Hr(),
		Md("🤖 默认模型 / 推理强度"),
		Actions([]CardElement{Button("设置默认模型", ActionValue{"a": GSModelDefault}, ButtonPrimary)}, ""),
		Note(fmt.Sprintf("当前 %s　·　新话题的起始模型 / 推理强度（话题内 `/model` 可临时改）。", ModelDefaultSummary(p))),
	)
	return Card(elements, CardOpts{Header: &CardHeader{Title: "⚙️ 群设置", Template: HeaderBlue}})
}

// ── 小工具 ──

func fmtTitle(prefix, name string) string {
	return fmt.Sprintf("**%s** · %s", prefix, name)
}

func boolBtn(b bool) ButtonType {
	if b {
		return ButtonPrimary
	}
	return ButtonDefault
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func orElseStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ── 云文档评论设置 ──

// CommentSettingsInfo 云文档评论 @bot 设置卡数据。
type CommentSettingsInfo struct {
	BackendOptions []SelectOption // 可选后端（id+label）
	Models         []ModelRow     // 当前后端模型
	UnionEfforts   []string       // 并集推理强度
	CurBackend     string         // 当前后端 id（已解析默认）
	CurModel       string         // 当前模型 id
	CurEffort      string         // 当前推理强度
	Notice         string
}

// BuildCommentSettingsCard 云文档评论 @bot 全局设置卡（后端级联 + 模型/强度表单 + 提示词编辑）。
func BuildCommentSettingsCard(info CommentSettingsInfo) CardObject {
	els := []CardElement{}
	if info.Notice != "" {
		els = append(els, Md(info.Notice))
	}
	els = append(els,
		Md("**📝 云文档评论 @bot**"),
		Note("评论里 @我时用的后端 / 模型 / 推理强度。只影响之后新建的评论。"),
		Hr(),
	)
	// 后端：级联源。多后端给按钮，单后端只读。
	if len(info.BackendOptions) > 1 {
		btns := []CardElement{}
		for _, b := range info.BackendOptions {
			primary := ButtonDefault
			if b.Value == info.CurBackend {
				primary = ButtonPrimary
			}
			btns = append(btns, Button(b.Label, ActionValue{"a": DMCommentSetBackend, "v": b.Value}, primary))
		}
		els = append(els,
			Md("🧠 **后端**"),
			Actions(btns, ""),
		)
	} else {
		label := info.CurBackend
		if len(info.BackendOptions) == 1 {
			label = info.BackendOptions[0].Label
		}
		els = append(els, Md("🧠 **后端**："+label))
	}
	// 模型 + 推理强度：表单下拉（selectMenu 不锁卡），一次提交两者。
	canPickModel := len(info.Models) > 1
	canPickEffort := len(info.UnionEfforts) > 0
	if canPickModel || canPickEffort {
		formEls := []CardElement{}
		if canPickModel {
			opts := make([]SelectOption, 0, len(info.Models))
			for _, m := range info.Models {
				opts = append(opts, SelectOption{Label: m.DisplayName, Value: m.ID})
			}
			formEls = append(formEls, Md("🤖 **模型**"), SelectMenu("model", "选择模型", opts, info.CurModel))
		} else {
			label := "后端默认"
			if info.CurModel != "" {
				label = info.CurModel
			}
			formEls = append(formEls, Md("🤖 **模型**："+label+"（该后端仅一个模型）"))
		}
		if canPickEffort {
			opts := make([]SelectOption, 0, len(info.UnionEfforts))
			for _, e := range info.UnionEfforts {
				opts = append(opts, SelectOption{Label: "强度：" + effortLabel(e), Value: e})
			}
			formEls = append(formEls, Md("🎚 **推理强度**"), SelectMenu("effort", "选择推理强度", opts, info.CurEffort))
		}
		formEls = append(formEls, Actions([]CardElement{SubmitButton("✅ 保存模型 / 强度", ActionValue{"a": DMCommentSubmit}, ButtonPrimary, "submit_comment")}, ""))
		els = append(els, Form("comment_model_effort", formEls))
	} else {
		els = append(els, Note("该后端只有一个模型且不调推理强度，无需设置。"))
	}
	els = append(els,
		Hr(),
		Md("✍️ **提示词**"),
		Note("评论 @我 时我的角色与回复规则（含怎么读 / 改文档）。点开可直接在卡里编辑。"),
		Actions([]CardElement{Button("编辑提示词", ActionValue{"a": DMCommentEditPrompt}, ButtonPrimary)}, ""),
		Hr(),
		Md("📎 **配合飞书 CLI**"),
		Note("评论里要**读 / 改文档**，靠飞书 CLI（lark-cli）：装好并登录后即可（用你自己的身份读写、对自己的文档有权限）。"),
		Hr(),
		Actions([]CardElement{Button("⬅️ 返回设置", ActionValue{"a": DMSettings}, ButtonDefault)}, ""),
	)
	return Card(els, CardOpts{Header: &CardHeader{Title: "📝 文档评论设置", Template: HeaderBlue}})
}

// CommentPromptInfo 评论提示词编辑卡数据。
type CommentPromptInfo struct {
	CurrentPrompt string
	Notice       string
	MasterFile   string
}

// BuildCommentPromptCard 评论提示词编辑子卡（撑满多行输入框 + 保存 / 重置）。
func BuildCommentPromptCard(info CommentPromptInfo) CardObject {
	els := []CardElement{}
	if info.Notice != "" {
		els = append(els, Md(info.Notice))
	}
	els = append(els,
		Md("**✍️ 评论提示词**"),
		Note("评论 @我 时我的固定人设与回复规则——保存后会同步到所有文档（含历史），下一条评论生效。"),
		Md("**可用变量**（同步到每篇文档时自动替换成该文档自己的值）：\n- `{docUrl}` 文档链接\n- `{fileToken}` 文档 token（链接里类型后那段）\n- `{fileType}` 文档类型（doc/docx/sheet/bitable）"),
		Note("评论的选中原文、用户问题每轮会自动给我，无需写进提示词。"),
		Form("comment_prompt", []CardElement{
			Input(InputOpts{
				Name: "prompt", Label: "提示词内容", Value: info.CurrentPrompt, Required: true,
				InputType: "multiline_text", Rows: 12, Width: "fill", MaxLength: 1000,
			}),
			Actions([]CardElement{
				SubmitButton("✅ 保存提示词", ActionValue{"a": DMCommentPromptSubmit}, ButtonPrimary, "submit_prompt"),
				SubmitButton("↩️ 重置为默认", ActionValue{"a": DMCommentResetPrompt}, ButtonDefault, "reset_prompt"),
			}, ""),
		}),
		Hr(),
		Md("**📨 每轮评论 @我，我会收到下面消息：**"),
		Md("- 链接：{docUrl}\n- file_token：{fileToken}\n- 类型：{fileType}\n- 评论范围：行内评论（针对选中文字）/ 全文评论（针对整篇）\n\n用户选中的原文：（仅行内评论时附上）\n> …被评论的那段文字……\n\n用户的问题：……评论正文……"),
		Note(orElseStr(info.MasterFile != "", "提示词也可直接编辑 "+info.MasterFile+"（改文件后新内容在每篇文档的下一条评论时生效）。", "提示词也可直接编辑 bot 目录下的 comment-instructions.md。")),
		Actions([]CardElement{Button("⬅️ 返回", ActionValue{"a": DMCommentSettings}, ButtonDefault)}, ""),
	)
	return Card(els, CardOpts{Header: &CardHeader{Title: "✍️ 编辑提示词", Template: HeaderBlue}, WidthMode: "fill"})
}

// ── ☕ 咖啡一下 ──

// BuildCoffeeSettingsCard 把 cli-bridge 的「☕ 咖啡一下」设置区（已为内联进主卡加的开头 hr）
// 抽出来独立成卡。section 由调用方用 clibridge.BuildCliBridgeSettingsSection 现算后传入。
func BuildCoffeeSettingsCard(section []CardElement) CardObject {
	els := []CardElement{}
	if len(section) > 1 {
		// 去掉为「内联进主卡」而加的开头 hr()
		els = append(els, section[1:]...)
	} else if len(section) == 1 {
		els = append(els, section[0])
	}
	els = append(els, Hr(), Actions([]CardElement{Button("⬅️ 返回设置", ActionValue{"a": DMSettings}, ButtonDefault)}, ""))
	return Card(els, CardOpts{Header: &CardHeader{Title: "☕ 咖啡一下", Template: HeaderBlue}})
}
