package bot

// handlers_cards.go —— DM / 群内 /settings 全部卡片回调 handler（对齐 TS bot/handle-message.ts 的 dispatcher）。
// 设计：每个 handler 形如 func(cca card.CardActionContext) error；即时操作（开关/设置）同步改盘 +
// 回发新卡；慢操作（建项目/删除/模型落盘）用 goroutine 异步执行、点击立即 ack（对齐 TS 的
// void (async()=>{}) 模式）。所有卡片统一经 o.sendCardAction 发到 cca.Evt.ChatID（DM 私聊 / 群）。

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/admin"
	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/clibridge"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
	"github.com/modelzen/feishu-codex-bridge/internal/service"
	"github.com/modelzen/feishu-codex-bridge/internal/update"
)

// timeNowMs 当前毫秒时间戳。
func timeNowMs() int64 { return time.Now().UnixMilli() }

// ── 小工具 ──

// dmAdmin 私聊管理台门禁（仅管理员可在 DM 管理项目）。
func (o *Orchestrator) dmAdmin(op string) bool {
	if op == "" {
		return false
	}
	return config.IsAdmin(o.Cfg, op)
}

// evictLiveSessionsForChat 驱逐某群的活跃会话（权限/压缩切换后让新档下一条消息重绑生效）。
// 会话以 sessionKey（single=chatId，multi 主群区=chatId，话题=threadId）为 key；这里删 key==chatId
// 的主会话，话题级会话会在下次消息经 resolveThread 自愈重绑。
func (o *Orchestrator) evictLiveSessionsForChat(chatID string) {
	if chatID == "" {
		return
	}
	o.sessions.Delete(chatID)
}

// backendDisplayName 后端 id → 展示名（未知/空 → 默认 codex）。
func (o *Orchestrator) backendDisplayName(b string) string {
	id := agent.BackendForProject(b, false)
	if entry, ok := agent.CatalogByID(id); ok {
		return entry.DisplayName
	}
	if b == "" {
		return agent.DEFAULT_BACKEND_ID
	}
	return b
}

// backendOptions 新建/绑定项目时可选的后端列表（full 档；含全部已列后端）。
func (o *Orchestrator) backendOptions() []card.SelectOption {
	entries := agent.ProjectCreatableBackends(agent.PermissionFull, func(e agent.BackendCatalogEntry) bool { return true })
	opts := make([]card.SelectOption, 0, len(entries))
	for _, e := range entries {
		opts = append(opts, card.SelectOption{Label: e.DisplayName, Value: e.ID})
	}
	if len(opts) == 0 {
		opts = append(opts, card.SelectOption{Label: "Codex (默认)", Value: agent.DEFAULT_BACKEND_ID})
	}
	return opts
}

// listProjectModels 按项目后端实时列模型，返回可选行 + 并集推理强度。
func (o *Orchestrator) listProjectModels(ctx context.Context, backend string) ([]card.ModelRow, []string, error) {
	beID := agent.BackendForProject(backend, false)
	be, err := agent.CreateBackend(beID)
	if err != nil {
		return nil, nil, err
	}
	infos, err := be.ListModels(ctx)
	if err != nil {
		return nil, nil, err
	}
	unionSet := map[string]bool{}
	rows := make([]card.ModelRow, 0, len(infos))
	for _, m := range infos {
		if m.Hidden {
			continue
		}
		efforts := make([]string, 0, len(m.SupportedEfforts))
		for _, e := range m.SupportedEfforts {
			efforts = append(efforts, string(e))
			unionSet[string(e)] = true
		}
		rows = append(rows, card.ModelRow{ID: m.ID, DisplayName: m.DisplayName, SupportedEfforts: efforts})
	}
	union := make([]string, 0, len(unionSet))
	for e := range unionSet {
		union = append(union, e)
	}
	sort.Slice(union, func(i, j int) bool {
		return effortOrder(union[i]) < effortOrder(union[j])
	})
	return rows, union, nil
}

// effortOrder 推理强度排序权重（none<minimal<low<medium<high<xhigh）。
func effortOrder(e string) int {
	order := map[string]int{"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5}
	if v, ok := order[e]; ok {
		return v
	}
	return 99
}

// asTier 把字符串档位映射为 agent.PermissionMode（非法 → 空串，交由 Perform* 用默认值处理）。
func asTier(s string) agent.PermissionMode {
	switch s {
	case "qa":
		return agent.PermissionQA
	case "write":
		return agent.PermissionWrite
	case "full":
		return agent.PermissionFull
	}
	return ""
}

// intVal 从卡片 action value（可能 float64 / int / string）安全取整数。
func intVal(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	}
	return 0
}

// boolPtr 返回 *bool。
func boolPtr(b bool) *bool { return &b }

// ── DM 控制台入口 ──

// handleProjects 项目列表（带分页 p；话题数由 session 聚合）。
func (o *Orchestrator) handleProjects(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	page := intVal(cca.Value["p"])
	projects, _ := o.ProjectStore.List()
	topicsByChat := map[string]int{}
	if o.SessionStore != nil {
		if recs, err := o.SessionStore.List(); err == nil {
			for _, r := range recs {
				topicsByChat[r.ChatID]++
			}
		}
	}
	o.sendCardAction(cca, card.BuildProjectListCard(card.ProjectListInfo{
		Projects:     projects,
		TopicsByChat: topicsByChat,
		Page:         page,
	}))
}

// handleSettings DM 全局设置卡。
func (o *Orchestrator) handleSettings(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleDoctor 体检卡：真实探测 codex 可用性、飞书长连接状态、权限/事件订阅，
// 不再硬编码（对齐 TS doctor）。所有外部调用都走可注入 seam（validateCreds /
// diagnoseEvents / detectAgents），失败不报错、降级为「无法检查」。
func (o *Orchestrator) handleDoctor(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	appID := o.Cfg.Accounts.App.ID
	tenant := o.Cfg.Accounts.App.Tenant
	secret, err := config.ResolveAppSecret(o.Cfg)
	if err != nil {
		secret = "" // 探不到也不阻断，下方诊断会降级为「无法检查」
	}
	hc := &http.Client{Timeout: 12 * time.Second}

	// 1) codex 可用性（真实探测本地二进制）。
	codexOK := false
	codexVer := ""
	if rt := agent.FamilyOf(detectAgents(), agent.FamilyCodex); rt != nil {
		codexOK = rt.Installed
		codexVer = rt.Version
	}

	// 2) 飞书长连接真实状态（来自 Channel 维护的连接态，不依赖网络）。
	conn := o.channelConnState()

	// 3) 凭据 + 权限诊断（granted scopes 比对 REQUIRED / JOIN_GROUP）。
	vr := validateCreds(ctx, appID, secret, tenant, hc)

	// 4) 事件订阅诊断。
	ed := diagnoseEvents(ctx, appID, secret, tenant, hc)

	info := card.DoctorInfo{
		CodexOK:       codexOK,
		CodexVer:      codexVer,
		Conn:          conn,
		BridgeVer:     core.Version(),
		BotOpenID:     vr.BotOpenID,
		Node:          runtime.Version(),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		LogStdout:     config.ServiceLog(),
		LogStderr:     config.ServiceErrLog(),
		ConfigFile:    config.BotsFile(),
		MissingScopes: vr.MissingScopes,
		JoinMissing:   vr.MissingJoinScopes,
	}
	if appID != "" {
		info.ScopeGrantURL = config.BuildScopeGrantUrl(appID, tenant, config.GRANT_SCOPES...)
		info.JoinGrantURL = config.BuildScopeGrantUrl(appID, tenant, config.JOIN_GROUP_SCOPES...)
		info.EventConfigURL = config.BuildEventConfigUrl(appID, tenant)
	}
	if ed.State != "" {
		info.EventDiag = &card.EventDiagInfo{
			State:           string(ed.State),
			Version:         ed.Version,
			MissingRequired: ed.MissingRequired,
			MissingOptional: ed.MissingOptional,
			Reason:          ed.Reason,
			Events:          ed.Events,
		}
	}
	o.sendCardAction(cca, card.BuildDoctorCard(info))
}

// channelConnState 取飞书长连接真实状态；Channel 未实现 ConnState() 时降级 "unknown"。
func (o *Orchestrator) channelConnState() string {
	if ch, ok := o.Channel.(interface{ ConnState() string }); ok {
		return ch.ConnState()
	}
	return "unknown"
}

// handleReconnect 长连接状态卡：展示真实连接态（不再硬编码 "connected"）。
func (o *Orchestrator) handleReconnect(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildReconnectCard(o.channelConnState()))
}

// handleRestart 重启确认卡（两步确认）。
func (o *Orchestrator) handleRestart(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildRestartConfirmCard(o.channelConnState()))
}

// handleRestartDo 确认重启：先发「正在重启」卡（本进程随后被 kick 掉，卡不再更新），
// 再触发系统服务重启（best-effort；无系统服务可重启则回前台提示卡）。
func (o *Orchestrator) handleRestartDo(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildRestartingCard("restarting"))
	go func() {
		if err := service.Restart(); err != nil {
			core.Warn(cca.Ctx, "bot", "restart", "重启失败："+err.Error())
			o.sendCardAction(cca, card.BuildRestartingCard("foreground"))
		}
	}()
}

// handleCoffeeSettings 「☕ 咖啡一下」独立子卡（CLI 桥设置区），委托 cli-bridge.Service 现算。
func (o *Orchestrator) handleCoffeeSettings(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	section := []card.CardElement{}
	if svc, ok := o.CliBridge.(*clibridge.Service); ok {
		section = svc.SettingsSection()
	} else {
		o.sendCardAction(cca, card.Card([]card.CardElement{
			card.Md("☕ **咖啡一下**"),
			card.Note("当前未启用「☕ 咖啡一下」（cli-bridge 未启动）。请在终端运行 `feishu-codex-bridge hook --help` 了解如何开启本机 agent hook 桥接。"),
			card.BackToMenu(),
		}, card.CardOpts{Header: &card.CardHeader{Title: "☕ 咖啡一下", Template: card.HeaderBlue}}))
		return
	}
	o.sendCardAction(cca, card.BuildCoffeeSettingsCard(section))
}

// handleUpdate 版本更新（检查）：先 checking 再 checked；接 GitHub Releases 真实查询。
func (o *Orchestrator) handleUpdate(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{Phase: card.UpdateChecking}))
	go func() {
		cur := core.Version()
		repo := os.Getenv("FCB_UPDATE_REPO")
		rel, err := update.Latest(cca.Ctx, repo, nil)
		if err != nil {
			o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{
				Phase: card.UpdateError, From: cur,
				Message: "查询最新版本失败：" + err.Error(),
			}))
			return
		}
		hasUpdate := update.CompareVersion(cur, rel.TagName) < 0
		o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{
			Phase: card.UpdateChecked, Current: cur, Latest: rel.TagName,
			HasUpdate: hasUpdate,
		}))
	}()
}

// handleUpdateDo 版本更新（执行）：接 GitHub Releases 下载到临时文件（不自动替换运行中二进制）。
func (o *Orchestrator) handleUpdateDo(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{Phase: card.UpdateUpdating}))
	go func() {
		from := core.Version()
		repo := os.Getenv("FCB_UPDATE_REPO")
		rel, err := update.Latest(cca.Ctx, repo, nil)
		if err != nil {
			o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{
				Phase: card.UpdateError, From: from,
				Message: "查询最新版本失败：" + err.Error(),
			}))
			return
		}
		if update.CompareVersion(from, rel.TagName) >= 0 {
			o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{
				Phase: card.UpdateChecked, Current: from, Latest: rel.TagName, HasUpdate: false,
			}))
			return
		}
		asset, ok := update.CurrentPlatformAsset(rel)
		if !ok {
			o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{
				Phase: card.UpdateError, From: from,
				Message: "未找到匹配当前平台的预编译附件，请在 GitHub 手动下载：" + rel.HTMLURL,
			}))
			return
		}
		path, derr := update.DownloadToTemp(cca.Ctx, asset, nil)
		if derr != nil {
			o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{
				Phase: card.UpdateError, From: from,
				Message: "下载失败：" + derr.Error() + "（链接：" + asset.BrowserDownloadURL + "）",
			}))
			return
		}
		o.sendCardAction(cca, card.BuildUpdateCard(card.UpdateCardState{
			Phase:   card.UpdateDone,
			From:    from,
			To:      rel.TagName,
			Message: "已下载到临时文件：" + path + "\n为安全起见不自动替换运行中的二进制，请手动替换并重启 daemon（launchctl kickstart -k gui/501/ai.feishu-codex-bridge.bot）。",
		}))
	}()
}

// handleUsage 用量卡：Go 版暂未接入 wham 用量后端，显示当前版本 + 提示。
func (o *Orchestrator) handleUsage(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildUsageCard(card.UsageCardState{Phase: card.UsagePhaseLoading}))
	go func() {
		o.sendCardAction(cca, card.Card([]card.CardElement{
			card.Md("📊 Codex 用量"),
			card.Note("Go 版暂未接入用量统计后端（wham API）。当前版本：v" + core.Version()),
			card.BackToMenu(),
		}, card.CardOpts{Header: &card.CardHeader{Title: "📊 用量", Template: card.HeaderBlue}}))
	}()
}

// handleUsageShare / handleUsageRefresh / handleUsageShareDo 暂未接入 → 提示卡。
func (o *Orchestrator) handleStub(cca card.CardActionContext, title, msg string) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.Card([]card.CardElement{
		card.Md(title),
		card.Note(msg),
		card.BackToMenu(),
	}, card.CardOpts{Header: &card.CardHeader{Title: title, Template: card.HeaderBlue}}))
}

// ── 删除项目 ──

// handleRmConfirm 删除确认卡。
func (o *Orchestrator) handleRmConfirm(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	if name == "" {
		return
	}
	origin := ""
	if p, _ := o.ProjectStore.GetByName(name); p != nil {
		origin = p.Origin
	}
	o.sendCardAction(cca, card.BuildRmConfirmCard(card.RmConfirmInfo{Name: name, Origin: origin}))
}

// handleRmCancel 取消删除 → 回项目列表。
func (o *Orchestrator) handleRmCancel(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.handleProjects(cca)
}

// handleRmDo 执行删除：解绑（移除注册）+ 退群/转让群主（best-effort，失败仅 warn，不阻断解绑）。
func (o *Orchestrator) handleRmDo(cca card.CardActionContext) {
	name := stringOf(cca.Value["n"])
	op := cca.Evt.Operator.OpenID
	if !o.dmAdmin(op) || name == "" {
		return
	}
	p, _ := o.ProjectStore.GetByName(name)
	chatID := ""
	if p != nil {
		chatID = p.ChatID
	}
	removed, err := o.ProjectStore.Remove(name)
	if err != nil {
		core.Warn(cca.Ctx, "bot", "rm", "删除项目失败: "+err.Error())
	}
	var tail string
	if removed != nil && removed.Origin == "joined" {
		tail = "已解绑该群（未删代码目录）。"
	} else {
		tail = "已解绑（未删代码目录）。"
	}
	// 退群 / 转让群主（best-effort，失败不阻断解绑）。
	if chatID != "" {
		if creator, ok := o.Channel.(ChatCreator); ok {
			// 若该项目有其它人类成员，先转让群主再退群，避免把群丢给机器人自己。
			if members, merr := creator.GetChatMembers(cca.Ctx, chatID); merr == nil {
				var successor string
				for _, m := range members {
					if strings.HasPrefix(m.MemberID, "ou_") {
						successor = m.MemberID
						break
					}
				}
				if successor != "" {
					if terr := creator.TransferOwner(cca.Ctx, chatID, successor); terr != nil {
						core.Warn(cca.Ctx, "bot", "rm-transfer", "转让群主失败（可忽略）："+terr.Error())
					}
				}
			}
			if lerr := creator.LeaveChat(cca.Ctx, chatID); lerr != nil {
				core.Warn(cca.Ctx, "bot", "rm-leave", "退出群聊失败（可忽略，可手动移出）："+lerr.Error())
				tail += "我尝试退出群聊失败（可能因仍是群主），请在飞书手动将我移除。"
			} else {
				tail += "我已退出该群。"
			}
		}
	}
	if chatID != "" {
		o.sendCard(cca.Ctx, chatID, card.Card([]card.CardElement{
			card.Md(fmt.Sprintf("✅ 已删除项目「%s」。\n%s", name, tail)),
		}, card.CardOpts{Summary: "已删除项目"}))
	}
	o.handleProjects(cca)
}

// ── DM 全局设置：即时开关 ──

// handleSetTools 工具调用显示开关。
func (o *Orchestrator) handleSetTools(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	v := stringOf(cca.Value["v"]) == "on"
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	o.Cfg.Preferences.ShowToolCalls = boolPtr(v)
	_ = o.saveConfig()
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleSetShowModel 模型显示档位（off|running|always）。
func (o *Orchestrator) handleSetShowModel(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	v := stringOf(cca.Value["v"])
	m := "off"
	if v == "running" {
		m = "running"
	} else if v == "always" {
		m = "always"
	}
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	o.Cfg.Preferences.ShowModel = m
	_ = o.saveConfig()
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleSetPending 运行中来新消息策略（steer|queue）。
func (o *Orchestrator) handleSetPending(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	v := stringOf(cca.Value["v"])
	if v == "steer" || v == "queue" {
		if o.Cfg.Preferences == nil {
			o.Cfg.Preferences = &config.AppPreferences{}
		}
		o.Cfg.Preferences.PendingPolicy = v
		_ = o.saveConfig()
	}
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleSetProjectsRootDir 项目根目录（空白项目默认父目录）即时改盘。
func (o *Orchestrator) handleSetProjectsRootDir(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	dir := strings.TrimSpace(stringOf(cca.FormValue["dir"]))
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	o.Cfg.Preferences.ProjectsRootDir = dir
	_ = o.saveConfig()
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleSetConcurrency 并发上限（即时改盘 + 应用到运行时信号量）。
func (o *Orchestrator) handleSetConcurrency(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	n := intVal(cca.Value["v"])
	if n > 0 {
		if o.Cfg.Preferences == nil {
			o.Cfg.Preferences = &config.AppPreferences{}
		}
		o.Cfg.Preferences.MaxConcurrentRuns = &n
		o.Semaphore.SetLimit(n)
		_ = o.saveConfig()
	}
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleSetCompletionReminder 普通群任务结束提醒策略（四档；长任务阈值沿用当前）。
func (o *Orchestrator) handleSetCompletionReminder(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	mode := config.CompletionReminderMode(stringOf(cca.Value["v"]))
	if err := o.SetCompletionReminder(mode, 0); err != nil {
		core.Warn(cca.Ctx, "bot", "completion-reminder", "设置失败: "+err.Error())
	}
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleCompletionReminderCustom 打开长任务阈值输入卡。
func (o *Orchestrator) handleCompletionReminderCustom(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildCompletionReminderCustomCard(o.Cfg))
}

// handleCompletionReminderCustomSubmit 保存长任务阈值（钳到合法范围，零值回退默认值）。
func (o *Orchestrator) handleCompletionReminderCustomSubmit(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	raw := strings.TrimSpace(stringOf(cca.FormValue["minutes"]))
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		o.sendCardAction(cca, card.BuildCompletionReminderCustomCard(o.Cfg))
		return
	}
	mode := config.GetCompletionReminderConfig(o.Cfg).Mode
	if err := o.SetCompletionReminder(mode, n); err != nil {
		o.sendCardAction(cca, card.BuildCompletionReminderCustomCard(o.Cfg))
		return
	}
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// ── 云文档评论设置 ──

// handleCommentSettings 云文档评论 @bot 全局设置卡。
func (o *Orchestrator) handleCommentSettings(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	cc := config.GetCommentsConfig(o.Cfg)
	backend := cc.Backend
	if backend == "" {
		backend = agent.DEFAULT_BACKEND_ID
	}
	rows, union, _ := o.listProjectModels(cca.Ctx, backend)
	o.sendCardAction(cca, card.BuildCommentSettingsCard(card.CommentSettingsInfo{
		BackendOptions: o.backendOptions(),
		Models:         rows,
		UnionEfforts:   union,
		CurBackend:     backend,
		CurModel:       cc.Model,
		CurEffort:      cc.Effort,
	}))
}

// handleCommentSetBackend 级联切后端（改评论流后端 + 回设置卡）。
func (o *Orchestrator) handleCommentSetBackend(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	v := stringOf(cca.Value["v"])
	if v == "" {
		return
	}
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	if o.Cfg.Preferences.Comments == nil {
		o.Cfg.Preferences.Comments = &config.CommentsConfig{}
	}
	o.Cfg.Preferences.Comments.Backend = v
	_ = o.saveConfig()
	o.handleCommentSettings(cca)
}

// handleCommentSubmit 保存评论流模型 / 推理强度（校验后落盘 + 回设置卡）。
func (o *Orchestrator) handleCommentSubmit(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	cc := config.GetCommentsConfig(o.Cfg)
	backend := cc.Backend
	if backend == "" {
		backend = agent.DEFAULT_BACKEND_ID
	}
	modelID := strings.TrimSpace(stringOf(cca.FormValue["model"]))
	effortRaw := agent.ReasoningEffort(strings.TrimSpace(stringOf(cca.FormValue["effort"])))
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	if o.Cfg.Preferences.Comments == nil {
		o.Cfg.Preferences.Comments = &config.CommentsConfig{}
	}
	var notice string
	if modelID == "" {
		notice = "⚠️ 未选择模型，未保存。"
	} else {
		rows, _, _ := o.listProjectModels(cca.Ctx, backend)
		var m *card.ModelRow
		for i := range rows {
			if rows[i].ID == modelID {
				m = &rows[i]
				break
			}
		}
		if m == nil {
			notice = "⚠️ 所选模型无效或已下架，未保存。"
		} else {
			eff := string(effortRaw)
			ok := false
			for _, e := range m.SupportedEfforts {
				if e == eff {
					ok = true
					break
				}
			}
			if !ok {
				if len(m.SupportedEfforts) > 0 {
					eff = m.SupportedEfforts[0]
				} else {
					eff = ""
				}
			}
			o.Cfg.Preferences.Comments.Model = m.ID
			o.Cfg.Preferences.Comments.Effort = eff
			_ = o.saveConfig()
			notice = "✅ 已保存评论流模型 / 强度，下一条评论生效。"
		}
	}
	fresh := config.GetCommentsConfig(o.Cfg)
	rows, union, _ := o.listProjectModels(cca.Ctx, backend)
	o.sendCardAction(cca, card.BuildCommentSettingsCard(card.CommentSettingsInfo{
		BackendOptions: o.backendOptions(),
		Models:         rows,
		UnionEfforts:   union,
		CurBackend:     backend,
		CurModel:       fresh.Model,
		CurEffort:      fresh.Effort,
		Notice:         notice,
	}))
}

// handleCommentEditPrompt 打开评论提示词编辑卡。
func (o *Orchestrator) handleCommentEditPrompt(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildCommentPromptCard(card.CommentPromptInfo{
		CurrentPrompt: readCommentInstructions(),
		MasterFile:   commentMasterPath(),
	}))
}

// handleCommentPromptSubmit 保存评论提示词。
func (o *Orchestrator) handleCommentPromptSubmit(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	raw := strings.TrimSpace(stringOf(cca.FormValue["prompt"]))
	if raw == "" {
		raw = DefaultCommentInstructions
	}
	if err := writeCommentInstructions(raw); err != nil {
		core.Warn(cca.Ctx, "bot", "comment-prompt", "写提示词失败: "+err.Error())
	}
	o.sendCardAction(cca, card.BuildCommentPromptCard(card.CommentPromptInfo{
		CurrentPrompt: readCommentInstructions(),
		MasterFile:   commentMasterPath(),
		Notice:       "✅ 提示词已保存，下一条评论生效。",
	}))
}

// handleCommentResetPrompt 重置评论提示词为默认。
func (o *Orchestrator) handleCommentResetPrompt(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	if err := writeCommentInstructions(DefaultCommentInstructions); err != nil {
		core.Warn(cca.Ctx, "bot", "comment-prompt", "重置提示词失败: "+err.Error())
	}
	o.sendCardAction(cca, card.BuildCommentPromptCard(card.CommentPromptInfo{
		CurrentPrompt: readCommentInstructions(),
		MasterFile:   commentMasterPath(),
		Notice:       "✅ 已重置为默认提示词。",
	}))
}

// handleSetWatchdog 假死超时（秒；0=关闭）。
func (o *Orchestrator) handleSetWatchdog(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	n := intVal(cca.Value["v"])
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	o.Cfg.Preferences.RunIdleTimeoutSeconds = &n
	_ = o.saveConfig()
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// handleWatchdogCustom 打开自定义超时输入卡。
func (o *Orchestrator) handleWatchdogCustom(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	o.sendCardAction(cca, card.BuildWatchdogCustomCard(o.Cfg, 30, 3600))
}

// handleWatchdogCustomSubmit 保存自定义秒数（钳到 [30,3600]，0=关闭）。
func (o *Orchestrator) handleWatchdogCustomSubmit(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	raw := strings.TrimSpace(stringOf(cca.FormValue["sec"]))
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		o.sendCardAction(cca, card.BuildWatchdogCustomCard(o.Cfg, 30, 3600))
		return
	}
	sec := n
	if sec != 0 {
		if sec < 30 {
			sec = 30
		}
		if sec > 3600 {
			sec = 3600
		}
	}
	if o.Cfg.Preferences == nil {
		o.Cfg.Preferences = &config.AppPreferences{}
	}
	o.Cfg.Preferences.RunIdleTimeoutSeconds = &sec
	_ = o.saveConfig()
	o.sendCardAction(cca, card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
}

// ── 管理员名单 / 白名单 ──

func (o *Orchestrator) adminIDs() []string {
	owner := config.ResolveOwner(o.Cfg)
	ids := []string{}
	if owner != "" {
		ids = append(ids, owner)
	}
	if o.Cfg.Preferences != nil && o.Cfg.Preferences.Access != nil {
		ids = append(ids, o.Cfg.Preferences.Access.Admins...)
	}
	return ids
}

// handleAdmins 全局管理员名单卡。
func (o *Orchestrator) handleAdmins(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	owner := config.ResolveOwner(o.Cfg)
	var admins []string
	if o.Cfg.Preferences != nil && o.Cfg.Preferences.Access != nil {
		admins = o.Cfg.Preferences.Access.Admins
	}
	o.sendCardAction(cca, card.BuildAdminsCard(card.AdminsInfo{
		OwnerOpenID: owner, Admins: admins, Names: nil,
	}))
}

// handleAddAdminForm 打开添加管理员表单（跨所有项目群拉成员做下拉）。
func (o *Orchestrator) handleAddAdminForm(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	members := o.memberInputsAllProjects(cca.Ctx)
	o.sendCardAction(cca, card.BuildAddAdminCard(card.AddAdminInfo{Members: members}))
}

// handleAddAdminSubmit 添加管理员（去重 + 落盘）。
func (o *Orchestrator) handleAddAdminSubmit(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	manual := strings.TrimSpace(stringOf(cca.FormValue["open_id"]))
	id := manual
	if !strings.HasPrefix(id, "ou_") {
		id = strings.TrimSpace(stringOf(cca.FormValue["member"]))
	}
	go func() {
		if id != "" && strings.HasPrefix(id, "ou_") {
			if o.Cfg.Preferences == nil {
				o.Cfg.Preferences = &config.AppPreferences{}
			}
			if o.Cfg.Preferences.Access == nil {
				o.Cfg.Preferences.Access = &config.AppAccess{}
			}
			if o.Cfg.Preferences.Access.OwnerOpenID == "" {
				o.Cfg.Preferences.Access.OwnerOpenID = config.ResolveOwner(o.Cfg)
			}
			dup := false
			for _, a := range o.Cfg.Preferences.Access.Admins {
				if a == id {
					dup = true
					break
				}
			}
			if !dup {
				o.Cfg.Preferences.Access.Admins = append(o.Cfg.Preferences.Access.Admins, id)
				_ = o.saveConfig()
			}
		}
		o.sendCardAction(cca, card.BuildAdminsCard(card.AdminsInfo{
			OwnerOpenID: config.ResolveOwner(o.Cfg),
			Admins:      o.adminIDsFiltered(),
			Names:       nil,
		}))
	}()
}

// adminIDsFiltered 返回去重后的 admin id 列表（owner 已在 adminIDs 含，这里排重）。
func (o *Orchestrator) adminIDsFiltered() []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range o.adminIDs() {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// handleRmAdmin 移除管理员（owner 不可删）。
func (o *Orchestrator) handleRmAdmin(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	id := stringOf(cca.Value["u"])
	go func() {
		if id != "" && id != config.ResolveOwner(o.Cfg) {
			if o.Cfg.Preferences != nil && o.Cfg.Preferences.Access != nil {
				filtered := o.Cfg.Preferences.Access.Admins[:0]
				for _, a := range o.Cfg.Preferences.Access.Admins {
					if a != id {
						filtered = append(filtered, a)
					}
				}
				o.Cfg.Preferences.Access.Admins = filtered
				_ = o.saveConfig()
			}
		}
		o.sendCardAction(cca, card.BuildAdminsCard(card.AdminsInfo{
			OwnerOpenID: config.ResolveOwner(o.Cfg),
			Admins:      o.adminIDsFiltered(),
			Names:       nil,
		}))
	}()
}

// handleAllowlist 项目响应白名单卡。
func (o *Orchestrator) handleAllowlist(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	p, _ := o.ProjectStore.GetByName(name)
	if p == nil {
		o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		return
	}
	o.sendCardAction(cca, card.BuildAllowlistCard(card.AllowlistInfo{
		ProjectName: p.Name, Users: p.AllowedUsers, Names: nil,
	}))
}

// handleAddAllowedForm 打开添加白名单成员表单（从项目群拉成员做下拉）。
func (o *Orchestrator) handleAddAllowedForm(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	if name == "" {
		return
	}
	members := o.memberInputsForProject(cca.Ctx, name)
	o.sendCardAction(cca, card.BuildAddAllowedCard(card.AddAllowedInfo{ProjectName: name, Members: members}))
}

// memberInputsForProject 拉取某项目群的成员做下拉候选（best-effort，失败返回 nil）。
func (o *Orchestrator) memberInputsForProject(ctx context.Context, projectName string) []card.MemberInput {
	p, _ := o.ProjectStore.GetByName(projectName)
	if p == nil || p.ChatID == "" {
		return nil
	}
	creator, ok := o.Channel.(ChatCreator)
	if !ok {
		return nil
	}
	infos, err := creator.GetChatMembers(ctx, p.ChatID)
	if err != nil {
		core.Warn(ctx, "bot", "members", "拉取群成员失败："+err.Error())
		return nil
	}
	out := make([]card.MemberInput, 0, len(infos))
	for _, m := range infos {
		if !strings.HasPrefix(m.MemberID, "ou_") {
			continue
		}
		out = append(out, card.MemberInput{OpenID: m.MemberID, Name: m.Name})
	}
	return out
}

// memberInputsAllProjects 跨所有项目群去重拉成员（全局管理员添加用，best-effort）。
func (o *Orchestrator) memberInputsAllProjects(ctx context.Context) []card.MemberInput {
	creator, ok := o.Channel.(ChatCreator)
	if !ok {
		return nil
	}
	projects, err := o.ProjectStore.List()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []card.MemberInput
	for _, p := range projects {
		if p.ChatID == "" {
			continue
		}
		infos, merr := creator.GetChatMembers(ctx, p.ChatID)
		if merr != nil {
			continue
		}
		for _, m := range infos {
			if !strings.HasPrefix(m.MemberID, "ou_") || seen[m.MemberID] {
				continue
			}
			seen[m.MemberID] = true
			out = append(out, card.MemberInput{OpenID: m.MemberID, Name: m.Name})
		}
	}
	return out
}

// handleAddAllowedSubmit 添加白名单成员（去重 + 落盘）。
func (o *Orchestrator) handleAddAllowedSubmit(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	manual := strings.TrimSpace(stringOf(cca.FormValue["open_id"]))
	id := manual
	if !strings.HasPrefix(id, "ou_") {
		id = strings.TrimSpace(stringOf(cca.FormValue["member"]))
	}
	go func() {
		if id != "" && strings.HasPrefix(id, "ou_") && name != "" {
			_ = o.ProjectStore.Update(name, func(proj *project.Project) {
				for _, a := range proj.AllowedUsers {
					if a == id {
						return
					}
				}
				proj.AllowedUsers = append(proj.AllowedUsers, id)
			})
		}
		fresh, _ := o.ProjectStore.GetByName(name)
		if fresh == nil {
			o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
			return
		}
		o.sendCardAction(cca, card.BuildAllowlistCard(card.AllowlistInfo{
			ProjectName: fresh.Name, Users: fresh.AllowedUsers, Names: nil,
		}))
	}()
}

// handleRmAllowed 移除白名单成员。
func (o *Orchestrator) handleRmAllowed(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	id := stringOf(cca.Value["u"])
	name := stringOf(cca.Value["n"])
	go func() {
		if id != "" && name != "" {
			_ = o.ProjectStore.Update(name, func(proj *project.Project) {
				filtered := proj.AllowedUsers[:0]
				for _, a := range proj.AllowedUsers {
					if a != id {
						filtered = append(filtered, a)
					}
				}
				proj.AllowedUsers = filtered
			})
		}
		fresh, _ := o.ProjectStore.GetByName(name)
		if fresh == nil {
			o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
			return
		}
		o.sendCardAction(cca, card.BuildAllowlistCard(card.AllowlistInfo{
			ProjectName: fresh.Name, Users: fresh.AllowedUsers, Names: nil,
		}))
	}()
}

// ── 项目设置容器 ──

// handleProjectSettings 项目设置卡。
func (o *Orchestrator) handleProjectSettings(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	if name == "" {
		return
	}
	p, _ := o.ProjectStore.GetByName(name)
	if p == nil {
		o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		return
	}
	o.sendCardAction(cca, card.BuildProjectSettingsCard(card.ProjectSettingsInfo{
		Project:      *p,
		BackendName:  o.backendDisplayName(p.Backend),
	}))
}

// handleProjectTopics 项目话题列表（来自 session 记录）。
func (o *Orchestrator) handleProjectTopics(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	p, _ := o.ProjectStore.GetByName(name)
	if p == nil {
		o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		return
	}
	var topics []card.ProjectTopic
	if o.SessionStore != nil {
		if recs, err := o.SessionStore.List(); err == nil {
			for _, r := range recs {
				if r.ChatID == p.ChatID && r.Summary != "" {
					topics = append(topics, card.ProjectTopic{Summary: r.Summary, UpdatedAt: r.UpdatedAt})
				}
			}
		}
	}
	o.sendCardAction(cca, card.BuildProjectTopicsCard(name, p.ChatID, topics))
}

// handleSetNoMentionDm 项目级免@开关（DM 版，携带项目名 n）。
func (o *Orchestrator) handleSetNoMentionDm(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	on := stringOf(cca.Value["v"]) == "on"
	out := admin.PerformSetNoMention(o.ProjectStore, name, on)
	if !out.Ok || out.Project == nil {
		o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		return
	}
	o.sendCardAction(cca, card.BuildProjectSettingsCard(card.ProjectSettingsInfo{
		Project:      *out.Project,
		BackendName:  o.backendDisplayName(out.Project.Backend),
	}))
}

// handleSetAutoCompactDm 项目级自动压缩开关（DM 版，携带项目名 n；落盘 + 驱逐活跃会话）。
func (o *Orchestrator) handleSetAutoCompactDm(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	on := stringOf(cca.Value["v"]) == "on"
	out := admin.PerformSetAutoCompact(o.ProjectStore, name, on, o.evictLiveSessionsForChat)
	if !out.Ok || out.Project == nil {
		o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		return
	}
	o.sendCardAction(cca, card.BuildProjectSettingsCard(card.ProjectSettingsInfo{
		Project:      *out.Project,
		BackendName:  o.backendDisplayName(out.Project.Backend),
	}))
}

// handlePermission 打开权限档表单子卡。
func (o *Orchestrator) handlePermission(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	p, _ := o.ProjectStore.GetByName(name)
	if p == nil {
		o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		return
	}
	o.sendCardAction(cca, card.BuildPermissionCard(card.PermissionCardInfo{Project: *p}))
}

// handlePermissionSubmit 提交权限档（管理员档/普通用户档/联网）+ 驱逐活跃会话让新档立即生效。
func (o *Orchestrator) handlePermissionSubmit(cca card.CardActionContext) {
	name := stringOf(cca.Value["n"])
	op := cca.Evt.Operator.OpenID
	if !o.dmAdmin(op) || name == "" {
		return
	}
	mode := asTier(stringOf(cca.FormValue["mode"]))
	guestMode := asTier(stringOf(cca.FormValue["guestMode"]))
	network := stringOf(cca.FormValue["network"]) == "on"
	netPtr := &network
	go func() {
		out := admin.PerformSetPermissionMode(o.ProjectStore, name, mode, guestMode, netPtr, o.evictLiveSessionsForChat)
		if !out.Ok || out.Project == nil {
			o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
			return
		}
		o.sendCardAction(cca, card.BuildProjectSettingsCard(card.ProjectSettingsInfo{
			Project:      *out.Project,
			BackendName:  o.backendDisplayName(out.Project.Backend),
		}))
	}()
}

// ── 默认模型 / 推理强度 ──

// handleModelDefault 打开默认模型子卡（DM 项目级）。
func (o *Orchestrator) handleModelDefault(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	name := stringOf(cca.Value["n"])
	p, _ := o.ProjectStore.GetByName(name)
	if p == nil {
		o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		return
	}
	rows, union, _ := o.listProjectModels(cca.Ctx, p.Backend)
	o.sendCardAction(cca, card.BuildModelDefaultCard(card.ModelDefaultCardInfo{
		Project: *p, Models: rows, UnionEfforts: union, Ctx: "dm",
	}))
}

// handleModelDefaultSubmit 提交默认模型（DM 项目级）：实时校验 + 落盘 + 回项目设置卡。
func (o *Orchestrator) handleModelDefaultSubmit(cca card.CardActionContext) {
	name := stringOf(cca.Value["n"])
	op := cca.Evt.Operator.OpenID
	if !o.dmAdmin(op) || name == "" {
		return
	}
	modelID := stringOf(cca.FormValue["model"])
	effortRaw := agent.ReasoningEffort(stringOf(cca.FormValue["effort"]))
	o.modelDefaultCommit(cca, name, modelID, effortRaw, false)
}

// handleGsModelDefaultSubmit 群内提交默认模型（按 evt.chatId 解析项目）。
func (o *Orchestrator) handleGsModelDefaultSubmit(cca card.CardActionContext) {
	op := cca.Evt.Operator.OpenID
	if !o.dmAdmin(op) {
		return
	}
	p, _ := o.ProjectStore.GetByChatID(cca.Evt.ChatID)
	if p == nil {
		return
	}
	modelID := stringOf(cca.FormValue["model"])
	effortRaw := agent.ReasoningEffort(stringOf(cca.FormValue["effort"]))
	o.modelDefaultCommit(cca, p.Name, modelID, effortRaw, true)
}

// modelDefaultCommit 默认模型落盘共用逻辑（isGroup=true 回群设置卡，否则回项目设置卡）。
func (o *Orchestrator) modelDefaultCommit(cca card.CardActionContext, name, modelID string, effortRaw agent.ReasoningEffort, isGroup bool) {
	go func() {
		p, err := o.ProjectStore.GetByName(name)
		if err != nil || p == nil {
			return
		}
		rows, _, _ := o.listProjectModels(cca.Ctx, p.Backend)
		var notice string
		if modelID == "" {
			notice = "⚠️ 未选择模型，未保存。"
		} else {
			var m *card.ModelRow
			for i := range rows {
				if rows[i].ID == modelID {
					m = &rows[i]
					break
				}
			}
			if m == nil {
				notice = "⚠️ 所选模型无效或已下架，未保存。"
			} else {
				supported := m.SupportedEfforts
				eff := string(effortRaw)
				ok := false
				for _, e := range supported {
					if e == eff {
						ok = true
						break
					}
				}
				if !ok {
					if len(supported) > 0 {
						eff = supported[0]
					} else {
						eff = ""
					}
				}
				out := admin.PerformSetModelDefault(o.ProjectStore, name, m.ID, agent.ReasoningEffort(eff))
				if out.Ok {
					effStr := ""
					if eff != "" {
						effStr = " · 强度 " + eff
					}
					notice = fmt.Sprintf("✅ 默认已设为「%s」%s，新话题生效。", m.DisplayName, effStr)
				} else {
					notice = "⚠️ " + out.Reason
				}
			}
		}
		fresh, _ := o.ProjectStore.GetByName(name)
		if fresh == nil {
			return
		}
		if isGroup {
			o.sendCardAction(cca, card.BuildGroupSettingsCard(card.GroupSettingsInfo{Project: *fresh}))
		} else {
			o.sendCardAction(cca, card.BuildProjectSettingsCard(card.ProjectSettingsInfo{
				Project:      *fresh,
				BackendName:  o.backendDisplayName(fresh.Backend),
				Notice:       notice,
			}))
		}
	}()
}

// ── 群内 /settings（GS）──

// handleGsSettings 群内设置卡。
func (o *Orchestrator) handleGsSettings(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	p, _ := o.ProjectStore.GetByChatID(cca.Evt.ChatID)
	if p == nil {
		o.sendCardAction(cca, card.BuildGroupSettingsCard(card.GroupSettingsInfo{
			Project: project.Project{Name: "本群", Kind: "multi"},
		}))
		return
	}
	o.sendCardAction(cca, card.BuildGroupSettingsCard(card.GroupSettingsInfo{Project: *p}))
}

// handleGsSetNoMention 群内免@开关。
func (o *Orchestrator) handleGsSetNoMention(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	on := stringOf(cca.Value["v"]) == "on"
	p, _ := o.ProjectStore.GetByChatID(cca.Evt.ChatID)
	proj := project.Project{Name: "本群", Kind: "multi"}
	if p != nil {
		proj = *p
	}
	if p != nil {
		out := admin.PerformSetNoMention(o.ProjectStore, p.Name, on)
		if out.Ok && out.Project != nil {
			proj = *out.Project
		}
	}
	o.sendCardAction(cca, card.BuildGroupSettingsCard(card.GroupSettingsInfo{Project: proj}))
}

// handleGsSetAutoCompact 群内自动压缩开关（落盘 + 驱逐活跃会话）。
func (o *Orchestrator) handleGsSetAutoCompact(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	on := stringOf(cca.Value["v"]) == "on"
	p, _ := o.ProjectStore.GetByChatID(cca.Evt.ChatID)
	proj := project.Project{Name: "本群", Kind: "multi"}
	if p != nil {
		proj = *p
	}
	if p != nil {
		out := admin.PerformSetAutoCompact(o.ProjectStore, p.Name, on, o.evictLiveSessionsForChat)
		if out.Ok && out.Project != nil {
			proj = *out.Project
		}
	}
	o.sendCardAction(cca, card.BuildGroupSettingsCard(card.GroupSettingsInfo{Project: proj}))
}

// handleGsModelDefault 群内默认模型子卡。
func (o *Orchestrator) handleGsModelDefault(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	p, _ := o.ProjectStore.GetByChatID(cca.Evt.ChatID)
	if p == nil {
		o.sendCardAction(cca, card.BuildGroupSettingsCard(card.GroupSettingsInfo{
			Project: project.Project{Name: "本群", Kind: "multi"},
		}))
		return
	}
	rows, union, _ := o.listProjectModels(cca.Ctx, p.Backend)
	o.sendCardAction(cca, card.BuildModelDefaultCard(card.ModelDefaultCardInfo{
		Project: *p, Models: rows, UnionEfforts: union, Ctx: "group",
	}))
}

// ── 绑定存量群（join）──

// handleJoinGroupSubmit 处理「绑定已有群」表单提交：校验 → 存盘（用既有 chatId，不新建群）→ 欢迎卡。
// 对齐 TS project/lifecycle.joinExistingGroup（bot 已被拉进群，仅绑定 chatId→项目）。
func (o *Orchestrator) handleJoinGroupSubmit(cca card.CardActionContext) {
	dmChat := cca.Evt.ChatID
	op := cca.Evt.Operator.OpenID
	if !o.dmAdmin(op) {
		return
	}
	name := strings.TrimSpace(stringOf(cca.FormValue["name"]))
	cwdArg := strings.TrimSpace(stringOf(cca.FormValue["cwd"]))
	chatID := stringOf(cca.Value["chatId"])
	backend := strings.TrimSpace(stringOf(cca.FormValue["backend"]))
	kind := "multi"
	if stringOf(cca.Value["kind"]) == "single" {
		kind = "single"
	}
	if backend == "" {
		backend = agent.DEFAULT_BACKEND_ID
	}
	// 外部群绑定档位：claude 系 full，其余（含 codex）qa（安全默认）。
	mode := agent.PermissionQA
	if strings.Contains(strings.ToLower(backend), "claude") {
		mode = agent.PermissionFull
	}

	go func() {
		// goroutine 内用全新 background ctx，避免 cca.Ctx 在 handler 返回后被取消。
		gctx := core.WithTrace(context.Background(), core.NewTraceID(), dmChat, "")
		fail := func(msg string) {
			core.Warn(gctx, "bot", "join-group", msg)
			o.sendCard(gctx, dmChat, card.Card([]card.CardElement{
				card.Md("⚠️ 绑定失败：" + msg),
			}, card.CardOpts{}))
		}
		if chatID == "" {
			fail("缺少群标识，请重新从进群通知里打开绑定卡")
			return
		}
		if name == "" {
			fail("项目名不能为空")
			return
		}
		if err := project.ValidateCreateProjectInput(o.ProjectStore, name); err != nil {
			fail(err.Error())
			return
		}
		cwd, blank, err := project.ResolveCwd(name, cwdArg, config.ResolveProjectsRootDir(o.Cfg))
		if err != nil {
			fail(err.Error())
			return
		}
		if err := project.AssertBackendUsable(backend, mode, func(agent.BackendCatalogEntry) bool { return true }); err != nil {
			fail(err.Error())
			return
		}
		proj := project.Project{
			Name: name, ChatID: chatID, Cwd: cwd, Blank: blank,
			Kind: kind, Backend: backend, CreatedAt: timeNowMs(),
			Origin: "joined", Mode: mode, AddedBy: op,
		}
		if err := o.ProjectStore.Add(proj); err != nil {
			core.Fail(gctx, "bot", "store-add", err)
			fail("绑定成功但写盘失败：" + err.Error())
			return
		}
		o.sendCardActionCtx(gctx, cca, card.BuildNewProjectDoneCard(card.NewProjectDoneInfo{
			Name: name, ChatID: chatID, Cwd: cwd, Origin: "joined",
		}))
		// onboarding：欢迎卡（joined 群只发欢迎卡，不 Pin/Tab/Menu）。best-effort。
		o.onboardGroup(gctx, proj, chatID)
		// 群公告（best-effort，对齐 TS setAnnouncement）
		o.setGroupAnnouncementBestEffort(gctx, proj, chatID)
	}()
}
