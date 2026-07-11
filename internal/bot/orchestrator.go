package bot

// orchestrator.go —— 消息编排核心骨架（对齐 TS bot/handle-message.ts 的顶层框架）。
// Orchestrator 持有全部状态 + OnMessage 消息入口（去重→P2P→群门禁→命令→分支→runStreamed）。
// 完整 handleTurn/goal/多模态/cardAction/reaper 后续逐步填充。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/admin"
	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/clibridge"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/feishu"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
	"github.com/modelzen/feishu-codex-bridge/internal/utils"
)

// ReactionAPI 给消息加/删表情回复（飞书 message reaction）。feishu.Channel 实现该接口，
// Orchestrator 通过类型断言调用（best-effort：未实现或失败即静默降级）。
type ReactionAPI interface {
	AddMessageReaction(ctx context.Context, messageID, emojiType string) (reactionID string, err error)
	RemoveMessageReaction(ctx context.Context, messageID, reactionID string) error
}

// ChatCreator 建群 + 群成员/管理员/群主管理（feishu.Channel 实现；避免循环 import，运行时类型断言）。
// 对齐 TS project/lifecycle.createProject 的飞书 SDK 建群步骤 + 群成员管理。
type ChatCreator interface {
	CreateChat(ctx context.Context, name, ownerOpenID string) (chatID string, err error)
	AddManagers(ctx context.Context, chatID string, managerIDs []string) error
	GetChatMembers(ctx context.Context, chatID string) ([]feishu.ChatMemberInfo, error)
	TransferOwner(ctx context.Context, chatID, openID string) error
	LeaveChat(ctx context.Context, chatID string) error
}

// CommentAPI 云文档评论读取 + 回帖（feishu.Channel 实现；运行时类型断言）。
// 对齐 TS bot/comments 的 resolveComment/fetchCommentContext/postCommentReply。
type CommentAPI interface {
	GetFileComment(ctx context.Context, fileToken, fileType, commentID, targetReplyID string) (*feishu.FileCommentData, error)
	CreateFileCommentReply(ctx context.Context, fileToken, fileType, commentID, text string) error
}

// Orchestrator 消息编排核心（持有全部 bot 进程内状态）。
type Orchestrator struct {
	Channel      interface{} // feishu.Channel（避免循环 import，运行时类型断言）
	Cfg          config.AppConfig
	ProjectStore *project.Store
	SessionStore *SessionStore
	Semaphore    *Semaphore
	Dispatcher   *card.CardDispatcher

	// SendCardFunc 卡片发送回调（注入；bot 层用 feishu.Channel.SendCardJSON）。
	SendCardFunc func(ctx context.Context, chatID string, cardJSON []byte) (string, error)

	// 私聊（open_id）发消息（bot 菜单 application.bot.menu_v6 用）。
	SendDMCardFunc func(ctx context.Context, openID string, cardJSON []byte) (string, error)
	SendDMTextFunc  func(ctx context.Context, openID, text string) (string, error)

	// 进程内会话缓存（key = sessionKey）。
	sessions sync.Map // map[string]*SessionEntry

	// admin 写操作。
	AdminDeps admin.Deps

	// CliBridge 「☕ 咖啡一下」反向桥运行时切面（nil=未启用）。
	CliBridge clibridge.CliBridgeRuntimeHooks

	// ConfigPath 当前 bot 的 config.json 路径（用于运行时设置落盘）。
	ConfigPath string

	// 去重缓存（msg ID → 最近时间）。
	recentIDs sync.Map // map[string]int64
	recentTTL time.Duration

	// bot 菜单事件去重（event_id → bool）。
	menuSeen sync.Map // map[string]bool

	// 普通群任务结束提醒去重（cardMsgID → bool），防止终态卡片重复触发 @ 回复。
	reminderSeen sync.Map

	// 运行卡注册表（运行卡 message_id → 句柄）。activeRuns=运行中；pastRuns=已终态（供 👍 续轮）。
	activeRuns sync.Map // map[string]*runHandle
	pastRuns   sync.Map // map[string]*runHandle
	pacers     sync.Map // map[string]*card.ChatPacer（per-chat 限流）
}

// saveConfig 把内存中的 o.Cfg 原子落盘（设置变更即时持久化）。
func (o *Orchestrator) saveConfig() error {
	if o.ConfigPath == "" {
		return fmt.Errorf("ConfigPath 未设置，无法保存配置")
	}
	return config.SaveConfig(o.ConfigPath, o.Cfg)
}

// SessionsByChat 返回某群的全部会话记录（话题钻取 / 列表计数用）。
// SessionStore 无原生分组方法，这里统一过滤 List() 结果。
func (o *Orchestrator) SessionsByChat(chatID string) []SessionRecord {
	if o.SessionStore == nil {
		return nil
	}
	all, err := o.SessionStore.List()
	if err != nil {
		return nil
	}
	var out []SessionRecord
	for _, s := range all {
		if s.ChatID == chatID {
			out = append(out, s)
		}
	}
	return out
}

// SessionEntry 一个活跃会话（thread → backend thread + active state）。
type SessionEntry struct {
	Thread    agent.AgentThread
	TurnID    string
	Started   time.Time
	LastState *SessionState // 最近一轮产出的状态（/context 展示 token 用量）
}

// SessionState 最近一次对话产出的状态（/context 用）。仅保留用量，后续可扩。
type SessionState struct {
	Usage *agent.ContextUsage
}

// NewOrchestrator 构造 + 注册 cardAction handler。
func NewOrchestrator(cfg config.AppConfig, projectStore *project.Store, sessionStore *SessionStore, configPath string) *Orchestrator {
	maxConcurrent := config.GetMaxConcurrentRuns(cfg)
	o := &Orchestrator{
		Cfg:          cfg,
		ProjectStore: projectStore,
		SessionStore: sessionStore,
		Semaphore:    NewSemaphore(maxConcurrent),
		Dispatcher:   card.NewCardDispatcher(),
		ConfigPath:   configPath,
		recentTTL:    10 * time.Minute,
	}
	o.registerCardHandlers()
	return o
}

// registerCardHandlers 注册全部卡片回调 handler（对齐 TS dispatcher）。
func (o *Orchestrator) registerCardHandlers() {
	d := o.Dispatcher

	// ── DM 控制台入口 ──
	d.On(card.DMMenu, func(cca card.CardActionContext) error {
		if o.dmAdmin(cca.Evt.Operator.OpenID) {
			o.sendCardAction(cca, card.BuildDmMenuCard("", core.Version()))
		}
		return nil
	})
	d.On(card.DMNewProject, func(cca card.CardActionContext) error {
		if o.dmAdmin(cca.Evt.Operator.OpenID) {
			o.sendCardAction(cca, card.BuildNewProjectFormCard(card.NewProjectFormOpts{Backends: o.backendOptions()}))
		}
		return nil
	})
	d.On(card.DMNewProjectSubmit, func(cca card.CardActionContext) error {
		return o.handleNewProjectSubmit(cca)
	})
	d.On(card.DMJoinGroupSubmit, func(cca card.CardActionContext) error {
		o.handleJoinGroupSubmit(cca)
		return nil
	})
	d.On(card.DMProjects, func(cca card.CardActionContext) error {
		o.handleProjects(cca)
		return nil
	})
	d.On(card.DMSettings, func(cca card.CardActionContext) error {
		o.handleSettings(cca)
		return nil
	})
	d.On(card.DMDoctor, func(cca card.CardActionContext) error {
		o.handleDoctor(cca)
		return nil
	})
	d.On(card.DMReconnect, func(cca card.CardActionContext) error {
		o.handleReconnect(cca)
		return nil
	})
	d.On(card.DMRestart, func(cca card.CardActionContext) error {
		o.handleRestart(cca)
		return nil
	})
	d.On(card.DMRestartDo, func(cca card.CardActionContext) error {
		o.handleRestartDo(cca)
		return nil
	})
	d.On(card.DMUpdate, func(cca card.CardActionContext) error {
		o.handleUpdate(cca)
		return nil
	})
	d.On(card.DMUpdateDo, func(cca card.CardActionContext) error {
		o.handleUpdateDo(cca)
		return nil
	})
	d.On(card.DMUsage, func(cca card.CardActionContext) error {
		o.handleUsage(cca)
		return nil
	})
	d.On(card.DMUsageRefresh, func(cca card.CardActionContext) error {
		o.handleUsage(cca)
		return nil
	})
	d.On(card.DMUsageShare, func(cca card.CardActionContext) error {
		o.handleStub(cca, "📤 分享用量", "Go 版暂未接入用量分享（wham API）。")
		return nil
	})
	d.On(card.DMUsageShareDo, func(cca card.CardActionContext) error {
		o.handleStub(cca, "📤 分享用量", "Go 版暂未接入用量分享（wham API）。")
		return nil
	})

	// ── 删除项目 ──
	d.On(card.DMRmConfirm, func(cca card.CardActionContext) error {
		o.handleRmConfirm(cca)
		return nil
	})
	d.On(card.DMRmCancel, func(cca card.CardActionContext) error {
		o.handleRmCancel(cca)
		return nil
	})
	d.On(card.DMRmDo, func(cca card.CardActionContext) error {
		o.handleRmDo(cca)
		return nil
	})

	// ── DM 全局设置：即时开关 ──
	d.On(card.DMSetTools, func(cca card.CardActionContext) error {
		o.handleSetTools(cca)
		return nil
	})
	d.On(card.DMSetShowModel, func(cca card.CardActionContext) error {
		o.handleSetShowModel(cca)
		return nil
	})
	d.On(card.DMSetWatchdog, func(cca card.CardActionContext) error {
		o.handleSetWatchdog(cca)
		return nil
	})
	d.On(card.DMWatchdogCustom, func(cca card.CardActionContext) error {
		o.handleWatchdogCustom(cca)
		return nil
	})
	d.On(card.DMWatchdogCustomSubmit, func(cca card.CardActionContext) error {
		o.handleWatchdogCustomSubmit(cca)
		return nil
	})
	d.On(card.DMSetPending, func(cca card.CardActionContext) error {
		o.handleSetPending(cca)
		return nil
	})
	d.On(card.DMSetConcurrency, func(cca card.CardActionContext) error {
		o.handleSetConcurrency(cca)
		return nil
	})
	d.On(card.DMSetCompletionReminder, func(cca card.CardActionContext) error {
		o.handleSetCompletionReminder(cca)
		return nil
	})
	d.On(card.DMCompletionReminderCustom, func(cca card.CardActionContext) error {
		o.handleCompletionReminderCustom(cca)
		return nil
	})
	d.On(card.DMCompletionReminderCustomSubmit, func(cca card.CardActionContext) error {
		o.handleCompletionReminderCustomSubmit(cca)
		return nil
	})

	// ── 管理员名单 / 响应白名单 ──
	d.On(card.DMAdmins, func(cca card.CardActionContext) error {
		o.handleAdmins(cca)
		return nil
	})
	d.On(card.DMAddAdminForm, func(cca card.CardActionContext) error {
		o.handleAddAdminForm(cca)
		return nil
	})
	d.On(card.DMAddAdminSubmit, func(cca card.CardActionContext) error {
		o.handleAddAdminSubmit(cca)
		return nil
	})
	d.On(card.DMRmAdmin, func(cca card.CardActionContext) error {
		o.handleRmAdmin(cca)
		return nil
	})
	d.On(card.DMAllowlist, func(cca card.CardActionContext) error {
		o.handleAllowlist(cca)
		return nil
	})
	d.On(card.DMAddAllowedForm, func(cca card.CardActionContext) error {
		o.handleAddAllowedForm(cca)
		return nil
	})
	d.On(card.DMAddAllowedSubmit, func(cca card.CardActionContext) error {
		o.handleAddAllowedSubmit(cca)
		return nil
	})
	d.On(card.DMRmAllowed, func(cca card.CardActionContext) error {
		o.handleRmAllowed(cca)
		return nil
	})

	// ── 项目设置容器 ──
	d.On(card.DMProjectSettings, func(cca card.CardActionContext) error {
		o.handleProjectSettings(cca)
		return nil
	})
	d.On(card.DMProjectTopics, func(cca card.CardActionContext) error {
		o.handleProjectTopics(cca)
		return nil
	})
	d.On(card.DMSetNoMentionDm, func(cca card.CardActionContext) error {
		o.handleSetNoMentionDm(cca)
		return nil
	})
	d.On(card.DMSetAutoCompactDm, func(cca card.CardActionContext) error {
		o.handleSetAutoCompactDm(cca)
		return nil
	})
	d.On(card.DMPermission, func(cca card.CardActionContext) error {
		o.handlePermission(cca)
		return nil
	})
	d.On(card.DMPermissionSubmit, func(cca card.CardActionContext) error {
		o.handlePermissionSubmit(cca)
		return nil
	})
	d.On(card.DMModelDefault, func(cca card.CardActionContext) error {
		o.handleModelDefault(cca)
		return nil
	})
	d.On(card.DMModelDefaultSubmit, func(cca card.CardActionContext) error {
		o.handleModelDefaultSubmit(cca)
		return nil
	})
	d.On(card.DMSetProjectsRootDir, func(cca card.CardActionContext) error {
		o.handleSetProjectsRootDir(cca)
		return nil
	})

	// ── 云文档评论 @bot 设置 ──
	d.On(card.DMCommentSettings, func(cca card.CardActionContext) error {
		o.handleCommentSettings(cca)
		return nil
	})
	d.On(card.DMCommentSetBackend, func(cca card.CardActionContext) error {
		o.handleCommentSetBackend(cca)
		return nil
	})
	d.On(card.DMCommentSubmit, func(cca card.CardActionContext) error {
		o.handleCommentSubmit(cca)
		return nil
	})
	d.On(card.DMCommentEditPrompt, func(cca card.CardActionContext) error {
		o.handleCommentEditPrompt(cca)
		return nil
	})
	d.On(card.DMCommentPromptSubmit, func(cca card.CardActionContext) error {
		o.handleCommentPromptSubmit(cca)
		return nil
	})
	d.On(card.DMCommentResetPrompt, func(cca card.CardActionContext) error {
		o.handleCommentResetPrompt(cca)
		return nil
	})
	d.On(card.DMCoffeeSettings, func(cca card.CardActionContext) error {
		o.handleCoffeeSettings(cca)
		return nil
	})

	// ── 群内 /settings（GS）──
	d.On(card.GSSettings, func(cca card.CardActionContext) error {
		o.handleGsSettings(cca)
		return nil
	})
	d.On(card.GSSetNoMention, func(cca card.CardActionContext) error {
		o.handleGsSetNoMention(cca)
		return nil
	})
	d.On(card.GSSetAutoCompact, func(cca card.CardActionContext) error {
		o.handleGsSetAutoCompact(cca)
		return nil
	})
	d.On(card.GSModelDefault, func(cca card.CardActionContext) error {
		o.handleGsModelDefault(cca)
		return nil
	})
	d.On(card.GSModelDefaultSubmit, func(cca card.CardActionContext) error {
		o.handleGsModelDefaultSubmit(cca)
		return nil
	})

	// ── 群命令卡片回调：/model 选择、/resume 恢复 ──
	d.On(card.MCModel, func(cca card.CardActionContext) error {
		o.handleModelSelect(cca, "model")
		return nil
	})
	d.On(card.MCEffort, func(cca card.CardActionContext) error {
		o.handleModelSelect(cca, "effort")
		return nil
	})
	d.On(card.RESPick, func(cca card.CardActionContext) error {
		o.handleResumePick(cca)
		return nil
	})

	// ── 运行卡控制（⏹ 终止 / 🎯 结束目标）──
	d.On(card.RCStop, func(cca card.CardActionContext) error {
		o.handleRunControl(cca)
		return nil
	})
	d.On(card.RCEndGoal, func(cca card.CardActionContext) error {
		o.handleRunControl(cca)
		return nil
	})
}

// handleNewProjectSubmit 处理 DM「新建项目」表单提交：校验 → 建群 → 存盘 → 欢迎卡。
// 对齐 TS project/lifecycle.createProject（建群/加管理员/公告/onboarding）。
func (o *Orchestrator) handleNewProjectSubmit(cca card.CardActionContext) error {
	ctx := cca.Ctx
	dmChat := cca.Evt.ChatID // 表单所在的私聊（成功卡回这里）
	owner := cca.Evt.Operator.OpenID

	fail := func(msg string) {
		core.Warn(ctx, "bot", "newproject", msg)
		o.sendCard(ctx, dmChat, card.Card([]card.CardElement{card.Md("⚠️ 建项目失败：" + msg)}, card.CardOpts{}))
	}

	name := strings.TrimSpace(stringOf(cca.FormValue["name"]))
	cwdArg := strings.TrimSpace(stringOf(cca.FormValue["cwd"]))
	kind := "multi"
	if stringOf(cca.Value["kind"]) == "single" {
		kind = "single"
	}
	backend := strings.TrimSpace(stringOf(cca.FormValue["backend"]))
	if backend == "" {
		backend = agent.DEFAULT_BACKEND_ID
	}
	// 权限档：表单未暴露 mode 选择（默认 full）；保留从 form 读取以便显式覆盖。
	modeArg := strings.TrimSpace(stringOf(cca.FormValue["mode"]))
	mode := agent.PermissionFull
	switch modeArg {
	case "write":
		mode = agent.PermissionWrite
	case "qa":
		mode = agent.PermissionQA
	}

	// 同步校验（快）：名查重 + 目录解析 + 后端门禁。失败立即回卡并快速返回，
	// 避免阻塞卡片 action 回调导致「目标回调超时」。
	if err := project.ValidateCreateProjectInput(o.ProjectStore, name); err != nil {
		fail(err.Error())
		return nil
	}
	cwd, blank, err := project.ResolveCwd(name, cwdArg, config.ResolveProjectsRootDir(o.Cfg))
	if err != nil {
		fail(err.Error())
		return nil
	}
	if err := project.AssertBackendUsable(backend, mode, func(agent.BackendCatalogEntry) bool { return true }); err != nil {
		fail(err.Error())
		return nil
	}

	// 慢操作（建群 + 发成功卡 + onboarding + 公告）整体丢进 goroutine，handler 立即返回，
	// 让框架尽快回 card.action.response（飞书要求 ~3s 内），避免「目标回调超时」。
	// 注意：goroutine 内必须用全新 background ctx，不能用 cca.Ctx（handler 返回后可能被取消）。
	go func() {
		gctx := core.WithTrace(context.Background(), core.NewTraceID(), dmChat, "")
		failBg := func(msg string) {
			core.Warn(gctx, "bot", "newproject", msg)
			o.sendCard(gctx, dmChat, card.Card([]card.CardElement{card.Md("⚠️ 建项目失败：" + msg)}, card.CardOpts{}))
		}
		// 1. 建群。
		creator, ok := o.Channel.(ChatCreator)
		if !ok {
			failBg("运行环境不支持建群（Channel 未实现 ChatCreator）")
			return
		}
		newChatID, err := creator.CreateChat(gctx, name, owner)
		if err != nil {
			core.Fail(gctx, "bot", "create-chat", err)
			failBg("建群失败：" + err.Error())
			return
		}
		// 提管理员（真实生效：把创建者提升为群管理员）。
		if err := creator.AddManagers(gctx, newChatID, []string{owner}); err != nil {
			core.Warn(gctx, "bot", "add-managers", "提管理员失败（可忽略）："+err.Error())
		}
		// 2. 存盘。
		proj := project.Project{
			Name: name, ChatID: newChatID, Cwd: cwd, Blank: blank,
			Kind: kind, Backend: backend, CreatedAt: time.Now().UnixMilli(),
			Origin: "created", Mode: mode,
		}
		if err := o.ProjectStore.Add(proj); err != nil {
			core.Fail(gctx, "bot", "store-add", err)
			failBg("建群成功但写盘失败：" + err.Error())
			return
		}
		// 3. 成功卡回 DM（用全新 gctx 发送，避免 cca.Ctx 已取消）。
		doneJSON, e := json.Marshal(card.BuildNewProjectDoneCard(card.NewProjectDoneInfo{
			Name: name, ChatID: newChatID, Cwd: cwd,
		}))
		if e != nil {
			core.Fail(gctx, "bot", "done-card-marshal", e)
		} else if o.SendCardFunc == nil {
			core.Warn(gctx, "bot", "no-send-card-func", "SendCardFunc 未注入，成功卡无法发出")
		} else if _, e2 := o.SendCardFunc(gctx, dmChat, doneJSON); e2 != nil {
			core.Warn(gctx, "bot", "card-send-fail", "回发成功卡失败 chatID="+dmChat+" err="+e2.Error())
		} else {
			core.Info(gctx, "bot", "card-sent", "回发成功卡 chatID="+dmChat+" action=dm.newProject.submit")
		}
		// 4. onboarding：欢迎卡（→ Pin/Tab/Menu，created 群）。best-effort。
		o.onboardGroup(gctx, proj, newChatID)
		// 5. 群公告（best-effort，对齐 TS setAnnouncement）
		o.setGroupAnnouncementBestEffort(gctx, proj, newChatID)
		// 测试钩子：异步建群完成后通知（生产默认 nil = 无操作）。
		if testHookAfterNewProject != nil {
			testHookAfterNewProject()
		}
	}()
	return nil
}

// testHookAfterNewProject 仅供单元测试在异步建群完成后同步等待；生产代码保持 nil。
var testHookAfterNewProject func()

// setGroupAnnouncementBestEffort 建群/绑群后写群公告（对齐 TS setAnnouncement）。
// 失败仅告警，不影响建群；并持久化检测到的真实分支。
func (o *Orchestrator) setGroupAnnouncementBestEffort(ctx context.Context, p project.Project, chatID string) {
	type announcer interface {
		SetGroupAnnouncement(context.Context, string, string) error
	}
	a, ok := o.Channel.(announcer)
	if !ok {
		return
	}
	branch := project.CurrentBranch(p.Cwd)
	text := project.AnnouncementText(p, branch)
	if err := a.SetGroupAnnouncement(ctx, chatID, text); err != nil {
		core.Warn(ctx, "bot", "announcement", "群公告写入失败（可忽略）："+err.Error())
		return
	}
	core.Info(ctx, "bot", "announcement", "群公告已写入 chatID="+chatID)
	if branch != "" && branch != p.Branch {
		_ = o.ProjectStore.Update(p.Name, func(pr *project.Project) { pr.Branch = branch })
	}
}

// stringOf 将卡片表单值（any）转字符串。
func stringOf(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// sendCardAction 从 CardActionContext 发卡片（沿用 cca.Ctx）。
func (o *Orchestrator) sendCardAction(cca card.CardActionContext, c card.CardObject) {
	o.sendCardActionCtx(cca.Ctx, cca, c)
}

// sendCardActionCtx 显式传 ctx 版本，供 goroutine 使用（避免 cca.Ctx 在 handler 返回后被取消）。
func (o *Orchestrator) sendCardActionCtx(ctx context.Context, cca card.CardActionContext, c card.CardObject) {
	if o.SendCardFunc == nil {
		core.Warn(ctx, "bot", "no-send-card-func", "SendCardFunc 未注入，卡片无法发出")
		return
	}
	jsonBytes, _ := json.Marshal(c)
	chatID := cca.Evt.ChatID
	if chatID == "" {
		core.Warn(ctx, "bot", "card-no-chat", "卡片回调缺少 chat_id，无法回发卡片（action="+cca.ActionID+"）")
		return
	}
	if _, err := o.SendCardFunc(ctx, chatID, jsonBytes); err != nil {
		core.Warn(ctx, "bot", "card-send-fail", "回发卡片失败 chatID="+chatID+" err="+err.Error())
		return
	}
	core.Info(ctx, "bot", "card-sent", "回发卡片成功 chatID="+chatID+" action="+cca.ActionID)
}

// NormalizedMessage 飞书消息（通用结构，运行时从 SDK 事件适配）。
type NormalizedMessage struct {
	MessageID    string
	ChatID       string
	ThreadID     string
	SenderID     string
	SenderName   string
	SenderType   string // user | app | system
	Content      string // 消息文本
	RawType      string // text/post/image/file/...
	ChatType     string // p2p | group
	Mentions     []Mention
	ReplyToMsgID string // 引用消息 id（parent_id）
}

// OnMessage 消息入口（去重→P2P→群门禁→命令→分支）。
func (o *Orchestrator) OnMessage(ctx context.Context, msg NormalizedMessage) {
	// 1. 去重（WS 重连重推防护）。
	if o.isRecent(msg.MessageID) {
		return
	}
	o.markRecent(msg.MessageID)

	ctx = core.WithTrace(ctx, core.NewTraceID(), msg.ChatID, msg.MessageID)

	// 2. P2P 私聊。
	isP2P := msg.ChatType == "p2p"
	// 2a. 先让 cli-bridge 尝试续聊回复匹配（任务完成卡的回复）；命中即吞掉。
	if o.CliBridge != nil && isP2P {
		if o.CliBridge.OnMessage(clibridge.OnMessageInput{
			ParentID:  msg.ReplyToMsgID,
			RootID:    msg.ThreadID,
			Text:      msg.Content,
			MessageID: msg.MessageID,
		}) {
			return
		}
	}
	// 2b. 否则路由到 DM 控制台（发菜单卡）。
	if isP2P {
		core.Info(ctx, "bot", "dm", "私聊消息路由到 DM 控制台")
		if o.SendCardFunc != nil {
			menuCard := card.BuildDmMenuCard("", core.Version())
			jsonBytes, _ := json.Marshal(menuCard)
			o.SendCardFunc(ctx, msg.ChatID, jsonBytes)
		}
		return
	}

	// 3. 群门禁：查项目 + @门 + 白名单。
	proj, err := o.ProjectStore.GetByChatID(msg.ChatID)
	if err != nil || proj == nil {
		// 未绑定群 → 引导绑定。
		core.Info(ctx, "bot", "unbound-chat", "未绑定群（后续回引导卡）")
		return
	}

	// @门 + 白名单检查。
	if !o.shouldRespond(proj, msg) {
		core.Info(ctx, "bot", "skip", "免@门或白名单未通过")
		return
	}

	// 4. 命令解析（前导斜杠 / goal trigger）。
	// 先从飞书原始 content 抽出纯文本（text/post→文字、@mention 替换），
	// 再剥掉开头 @bot 提及，否则 @bot /settings 会变 @_user_1 /settings 而漏判命令。
	// content 为飞书 JSON（以 { 开头）才走 ExtractMessageText；纯文本（含单测）原样用。
	rawText := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(rawText, "{") {
		rawText = ExtractMessageText(msg.RawType, msg.Content, msg.Mentions)
	}
	text := stripLeadingMention(rawText)
	if cmd, rest := parseCommand(text); cmd != "" {
		o.handleCommand(ctx, msg, proj, cmd, rest)
		return
	}

	// 5. 消息分支（single/multi）。
	kind := "multi"
	if proj.Kind != "" {
		kind = proj.Kind
	}

	go func() {
		turnCtx := core.WithTrace(context.Background(), core.NewTraceID(), msg.ChatID, msg.MessageID)
		// 先展示"处理中"表情（对齐 TS runReaction：排队用 OneSecond，否则 Typing）。
		reactionID := addProcessingReaction(turnCtx, o, msg.MessageID)
		backendID := agent.BackendForProject(proj.Backend, false)
		be, beErr := agent.CreateBackend(backendID)
		if beErr != nil {
			core.Fail(turnCtx, "bot", "create-backend", beErr)
			clearProcessingReaction(turnCtx, o, msg.MessageID, reactionID)
			return
		}
		// single: 整群一会话（key=chatId）；multi 话题内: key=threadId；multi 主群区: 也用 chatId（后续接 startTopicDirectly）
		threadKey := msg.ChatID
		if kind == "multi" && msg.ThreadID != "" {
			threadKey = msg.ThreadID
		}
		if herr := o.HandleTurn(turnCtx, be, TurnInput{
			ChatID: msg.ChatID, ThreadID: threadKey, Cwd: proj.Cwd,
			Text: text, Project: proj,
			MessageID: msg.MessageID,
			ReplyInThread: kind == "multi" && msg.ThreadID != "",
		}); herr != nil {
			core.Fail(turnCtx, "bot", "handle-turn", herr)
		}
		clearProcessingReaction(turnCtx, o, msg.MessageID, reactionID)
	}()
}

// isRecent 检查消息 ID 是否在最近去重窗口内。
func (o *Orchestrator) isRecent(msgID string) bool {
	if msgID == "" {
		return false
	}
	if v, ok := o.recentIDs.Load(msgID); ok {
		return time.Since(time.UnixMilli(v.(int64))) < o.recentTTL
	}
	return false
}

// markRecent 记录消息 ID。
func (o *Orchestrator) markRecent(msgID string) {
	if msgID == "" {
		return
	}
	o.recentIDs.Store(msgID, time.Now().UnixMilli())
}

// isP2P 判断是否私聊（chat_type == "p2p"）。
func (o *Orchestrator) isP2P(chatType string) bool {
	return chatType == "p2p"
}

// shouldRespond 群门禁 + @门 + 白名单检查。
func (o *Orchestrator) shouldRespond(proj *project.Project, msg NormalizedMessage) bool {
	// 管理员恒豁免。
	if config.IsAdmin(o.Cfg, msg.SenderID) {
		return true
	}
	// 项目白名单。
	if !config.IsUserAllowedInProject(o.Cfg, proj.AllowedUsers, msg.SenderID) {
		return false
	}
	// @门 + 免@决策。
	requireMention := config.GetRequireMentionInGroup(o.Cfg)
	noMention := proj.NoMention
	if noMention == nil {
		noMention = new(bool)
		*noMention = project.DefaultNoMention(*proj)
	}
	if requireMention && !*noMention {
		// 需 @（检查 mentions 是否含 bot）。
		if !hasBotMention(msg.Mentions) {
			return false
		}
	}
	return true
}

// parseCommand 解析前导斜杠命令（/help /model /settings /goal 等）。
func parseCommand(text string) (cmd, rest string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	// /goal <objective> 是 trigger。
	if strings.HasPrefix(text, "/goal ") || text == "/goal" {
		return "/goal", strings.TrimSpace(strings.TrimPrefix(text, "/goal"))
	}
	// 其它 /command。
	parts := strings.SplitN(text[1:], " ", 2)
	cmd = "/" + parts[0]
	if len(parts) > 1 {
		rest = strings.TrimSpace(parts[1])
	}
	return cmd, rest
}

// stripLeadingMention 去掉文本开头的 @bot 提及（飞书里形如 @_user_1 或 @显示名），
// 使后续 /command 可被 parseCommand 识别。仅当确实存在前置 @ 时才裁剪；
// 裁剪后在开头仍是 @（如 @某人 帮我看看）则原样保留，避免误删正常 @。
func stripLeadingMention(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "@") {
		return s
	}
	// 去掉第一个 @token（到首个空白为止）。
	if idx := strings.IndexAny(s, " \t\n\r"); idx > 0 {
		return strings.TrimSpace(s[idx:])
	}
	// 整句只有一个 @token（无后续内容）。
	return ""
}

// handleCommand 命令处理（/help /model /settings /compact /context /resume /goal）。
func (o *Orchestrator) handleCommand(ctx context.Context, msg NormalizedMessage, proj *project.Project, cmd, rest string) {
	core.Info(ctx, "bot", "command", fmt.Sprintf("cmd=%s rest=%q proj=%s", cmd, rest, proj.Name))

	switch cmd {
	case "/help":
		o.sendCard(ctx, msg.ChatID, card.BuildHelpCard(card.HelpTopic, true, config.IsAdmin(o.Cfg, msg.SenderID), card.HelpCaps{}))
	case "/model":
		o.handleModelCommand(ctx, msg, proj)
	case "/context":
		o.handleContextCommand(ctx, msg, proj)
	case "/compact":
		o.handleCompactCommand(ctx, msg, proj)
	case "/settings":
		// @bot /settings：群内设置卡（免@ / 自动压缩 / 默认模型）。
		proj := project.Project{Name: "本群", Kind: "multi"}
		if p, _ := o.ProjectStore.GetByChatID(msg.ChatID); p != nil {
			proj = *p
		}
		o.sendCard(ctx, msg.ChatID, card.BuildGroupSettingsCard(card.GroupSettingsInfo{Project: proj}))
	case "/resume":
		o.handleResumeCommand(ctx, msg, proj)
	case "/clear":
		o.handleClearCommand(ctx, msg, proj)
	case "/goal":
		if rest == "" {
			o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{card.Md("用法：`/goal <目标>` —— 让我自主多轮跑到完成。")}, card.CardOpts{}))
			return
		}
		// /goal → HandleGoal（RunGoal 自主多轮）。
		go func() {
			goalCtx := core.WithTrace(context.Background(), core.NewTraceID(), msg.ChatID, msg.MessageID)
			reactionID := addProcessingReaction(goalCtx, o, msg.MessageID)
			backendID := agent.BackendForProject(proj.Backend, false)
			be, beErr := agent.CreateBackend(backendID)
			if beErr != nil {
				core.Fail(goalCtx, "bot", "create-backend", beErr)
				clearProcessingReaction(goalCtx, o, msg.MessageID, reactionID)
				return
			}
			threadKey := msg.ChatID
			if msg.ThreadID != "" {
				threadKey = msg.ThreadID
			}
			if herr := o.HandleGoal(goalCtx, be, TurnInput{
				ChatID: msg.ChatID, ThreadID: threadKey, Cwd: proj.Cwd,
				Text: rest, SenderID: msg.SenderID, SenderName: msg.SenderName, Project: proj,
				MessageID: msg.MessageID,
				ReplyInThread: msg.ThreadID != "",
			}); herr != nil {
				core.Fail(goalCtx, "bot", "handle-goal", herr)
			}
			clearProcessingReaction(goalCtx, o, msg.MessageID, reactionID)
		}()
	default:
		core.Info(ctx, "bot", "command-unknown", "未知命令: "+cmd)
	}
}

// sendCard 构造 CardObject → JSON → SendCardFunc。
func (o *Orchestrator) sendCard(ctx context.Context, chatID string, c card.CardObject) {
	if o.SendCardFunc == nil {
		core.Warn(ctx, "bot", "no-send-card-func", "SendCardFunc 未注入，卡片无法发送")
		return
	}
	jsonBytes, _ := json.Marshal(c)
	msgID, err := o.SendCardFunc(ctx, chatID, jsonBytes)
	if err != nil {
		core.Fail(ctx, "bot", "send-card", err)
		return
	}
	core.Info(ctx, "bot", "card-sent", "卡片已发送: "+msgID)
}

// HandleBotMenu 处理飞书 bot 菜单点击（application.bot.menu_v6）：按 event_key 路由到对应 DM 卡。
// 对齐 TS onBotMenu：非管理员拒绝；命中 DM.* 键直达对应卡，其余回退首页菜单卡。
// 事件不带 chat_id，统一以 open_id 私聊投递。
func (o *Orchestrator) HandleBotMenu(ctx context.Context, openID, eventKey, eventID string) error {
	if openID == "" {
		return nil
	}
	// raw-tap 绕过 SDK 去重，有 event_id 则按它去重（防 at-least-once 重推双开卡）。
	if eventID != "" {
		if _, loaded := o.menuSeen.LoadOrStore("menu:"+eventID, true); loaded {
			return nil
		}
	}
	if !config.IsAdmin(o.Cfg, openID) {
		if o.SendDMTextFunc != nil {
			if _, err := o.SendDMTextFunc(ctx, openID, "⛔ 仅管理员可在私聊里管理项目。"); err != nil {
				core.Fail(ctx, "bot", "bot-menu-reject", err)
			}
		}
		return nil
	}
	send := func(c card.CardObject) {
		if o.SendDMCardFunc == nil {
			return
		}
		b, _ := json.Marshal(c)
		if _, err := o.SendDMCardFunc(ctx, openID, b); err != nil {
			core.Fail(ctx, "bot", "bot-menu-send", err)
		}
	}
	switch eventKey {
	case card.DMNewProject:
		send(card.BuildNewProjectFormCard(card.NewProjectFormOpts{Backends: o.backendOptions()}))
	case card.DMProjects:
		projects, _ := o.ProjectStore.List()
		topicsByChat := map[string]int{}
		if o.SessionStore != nil {
			if recs, err := o.SessionStore.List(); err == nil {
				for _, r := range recs {
					topicsByChat[r.ChatID]++
				}
			}
		}
		send(card.BuildProjectListCard(card.ProjectListInfo{Projects: projects, TopicsByChat: topicsByChat, Page: 0}))
	case card.DMSettings:
		send(card.BuildDmSettingsCard(card.DmSettingsInfo{Cfg: o.Cfg}))
	case card.DMDoctor:
		send(card.BuildDoctorCard(card.DoctorInfo{CodexOK: true, CodexVer: "codex", Conn: "connected", BridgeVer: core.Version(), BotOpenID: config.ResolveOwner(o.Cfg)}))
	case card.DMReconnect:
		send(card.BuildReconnectCard("connected"))
	case card.DMRestart:
		send(card.BuildRestartConfirmCard("connected"))
	case card.DMUpdate:
		send(card.BuildUpdateCard(card.UpdateCardState{Phase: card.UpdateChecking}))
		go func() {
			cur := core.Version()
			send(card.BuildUpdateCard(card.UpdateCardState{Phase: card.UpdateChecked, Current: cur, Latest: "", HasUpdate: false}))
		}()
	case card.DMUsage:
		send(card.BuildUsageCard(card.UsageCardState{Phase: card.UsagePhaseLoading}))
		go func() {
			send(card.Card([]card.CardElement{
				card.Md("📊 Codex 用量"),
				card.Note("Go 版暂未接入用量统计后端（wham API）。当前版本：v" + core.Version()),
				card.BackToMenu(),
			}, card.CardOpts{Header: &card.CardHeader{Title: "📊 用量", Template: card.HeaderBlue}}))
		}()
	default:
		send(card.BuildDmMenuCard("", core.Version()))
	}
	return nil
}

// HandleBotAdded 机器人被加入群（im.chat.member.bot.added_v1）：已绑定则提示，
// 未绑定则 DM 操作者一张「绑定此群」表单（复用 dm.joinGroup.submit 路径）。
func (o *Orchestrator) HandleBotAdded(ctx context.Context, chatID, operatorOpenID, chatName string) error {
	if chatID == "" {
		return nil
	}
	if existing, _ := o.ProjectStore.GetByChatID(chatID); existing != nil {
		o.sendCard(ctx, chatID, card.Card([]card.CardElement{
			card.Md(fmt.Sprintf("ℹ️ 本群已绑定为项目「%s」，无需重复绑定。", existing.Name)),
		}, card.CardOpts{Summary: "已绑定"}))
		return nil
	}
	if operatorOpenID == "" || o.SendDMCardFunc == nil {
		// 没有操作者 open_id 时退而在群里发提示。
		if operatorOpenID == "" {
			o.sendCard(ctx, chatID, card.Card([]card.CardElement{
				card.Md("我已被加入本群。管理员可私聊我，或在群里 @我 后发 `/settings` 管理项目。"),
			}, card.CardOpts{Summary: "已加入群"}))
		}
		return nil
	}
	b, _ := json.Marshal(card.BuildJoinGroupFormCard(card.JoinGroupFormOpts{
		ChatID: chatID, Name: chatName, Backends: o.backendOptions(),
	}))
	if _, err := o.SendDMCardFunc(ctx, operatorOpenID, b); err != nil {
		core.Fail(ctx, "bot", "bot-added-dm", err)
	}
	return nil
}

// HandleBotDeleted 机器人被移出群（im.chat.member.bot.deleted_v1）：解绑对应项目，
// 并 DM 群主提示。best-effort：找不到绑定 / DM 失败都不阻断。
func (o *Orchestrator) HandleBotDeleted(ctx context.Context, chatID, operatorOpenID string) error {
	if chatID == "" {
		return nil
	}
	proj, _ := o.ProjectStore.GetByChatID(chatID)
	if proj == nil {
		return nil
	}
	name := proj.Name
	if _, err := o.ProjectStore.Remove(name); err != nil {
		core.Warn(ctx, "bot", "bot-deleted-unbind", "解绑失败: "+err.Error())
	} else {
		core.Info(ctx, "bot", "bot-deleted-unbind", "已解绑项目「"+name+"」（群 "+chatID+"）")
	}
	owner := config.ResolveOwner(o.Cfg)
	if owner != "" && o.SendDMTextFunc != nil {
		if _, err := o.SendDMTextFunc(ctx, owner, fmt.Sprintf("ℹ️ 我已被移出群。已自动解绑项目「%s」。", name)); err != nil {
			core.Fail(ctx, "bot", "bot-deleted-dm", err)
		}
	}
	return nil
}

// AnnounceWhenLive 长连接连上后的启动收尾（对齐 TS onboarding.announceEventsWhenLive）：
// 诊断事件订阅状态，DM 通知 owner/admin；missing/unpublished 时后台轮询，配好并发布
// 版本后自动再播报「事件已生效」。unchecked（缺 scope/网络）仅记日志不播报。绝不 panic。
func (o *Orchestrator) AnnounceWhenLive(ctx context.Context) {
	appID := o.Cfg.Accounts.App.ID
	tenant := o.Cfg.Accounts.App.Tenant
	secret, err := config.ResolveAppSecret(o.Cfg)
	if err != nil {
		core.Warn(ctx, "bot", "announce-no-secret", "无法解析 app secret，跳过事件生效播报: "+err.Error())
		return
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	d := diagnoseEvents(ctx, appID, secret, tenant, hc)
	core.Info(ctx, "bot", "events-live", utils.SummarizeEventDiagnosis(d))
	if d.State == utils.EventDiagnosisOK {
		o.announceEventLiveCard(ctx, d, false)
		return
	}
	if d.State == utils.EventDiagnosisUnchecked {
		return // 已记日志，无法自动检测
	}
	// missing / unpublished：后台轮询，用户配完 + 发布版本后播报。
	go func() {
		poll := pollEvents(ctx, appID, secret, tenant, hc, 20*time.Second, 6*time.Minute)
		if poll == nil {
			core.Info(ctx, "bot", "events-live-timeout", "轮询事件订阅超时（仍非 ok），不再自动播报")
			return
		}
		core.Info(ctx, "bot", "events-live", "轮询确认事件已生效："+utils.SummarizeEventDiagnosis(*poll))
		o.announceEventLiveCard(ctx, *poll, true)
	}()
}

// announceEventLiveCard 把事件状态卡 DM 给 owner + 全部 admin（去重）。
func (o *Orchestrator) announceEventLiveCard(ctx context.Context, d utils.EventDiagnosis, polled bool) {
	if o.SendDMCardFunc == nil {
		return
	}
	guidance := ""
	if d.State != utils.EventDiagnosisOK {
		guidance = config.BuildEventConfigUrl(o.Cfg.Accounts.App.ID, o.Cfg.Accounts.App.Tenant)
	}
	c := card.BuildEventLiveCard(card.EventLiveCardOpts{
		State:           string(d.State),
		Version:         d.Version,
		Events:          d.Events,
		MissingRequired: d.MissingRequired,
		MissingOptional: d.MissingOptional,
		Polled:          polled,
		GuidanceURL:     guidance,
	})
	for _, openID := range o.adminOpenIDs() {
		b, _ := json.Marshal(c)
		if _, err := o.SendDMCardFunc(ctx, openID, b); err != nil {
			core.Warn(ctx, "bot", "announce-dm-fail", "事件生效播报 DM 失败 openID="+openID+" err="+err.Error())
		}
	}
}

// adminOpenIDs 返回 owner + 全部 admin 的 open_id（去重；空则空切片）。
func (o *Orchestrator) adminOpenIDs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(config.ResolveOwner(o.Cfg))
	if o.Cfg.Preferences != nil && o.Cfg.Preferences.Access != nil {
		for _, a := range o.Cfg.Preferences.Access.Admins {
			add(a)
		}
	}
	return out
}

// diagnoseEvents / pollEvents / validateCreds / detectAgents 是可替换的测试桩
// （默认指向 utils / agent 真实实现），便于 doctor / 事件播报单测时替换为假数据。
var (
	diagnoseEvents = utils.DiagnoseEventSubscription
	pollEvents     = utils.PollEventSubscription
	validateCreds  = utils.ValidateAppCredentials
	detectAgents   = agent.DetectAgents
)
// HandleComment 云文档评论 @bot → 跑 agent → 回帖（对齐 TS bot/comments）。
func (o *Orchestrator) HandleComment(ctx context.Context, fileToken, fileType, commentID, replyID string, isMentioned bool, noticeType string) error {
	if fileToken == "" || commentID == "" {
		return nil
	}
	if !isMentioned {
		// 仅处理 @我 的评论/回复，避免对所有评论都触发。
		core.Info(ctx, "bot", "comment-skip", "评论未 @bot，跳过")
		return nil
	}
	api, ok := o.Channel.(CommentAPI)
	if !ok {
		core.Warn(ctx, "bot", "comment-no-api", "Channel 未实现 CommentAPI，无法回帖")
		return nil
	}
	proj, perr := o.resolveCommentProject(ctx, fileToken)
	if perr != nil {
		core.Warn(ctx, "bot", "comment-no-project", perr.Error())
		return nil
	}
	cdata, gerr := api.GetFileComment(ctx, fileToken, fileType, commentID, replyID)
	if gerr != nil {
		core.Fail(ctx, "bot", "comment-fetch", gerr)
		return gerr
	}
	if cdata == nil || cdata.Question == "" {
		core.Warn(ctx, "bot", "comment-empty", "评论问题文本为空，跳过")
		return nil
	}
	backendID := agent.BackendForProject(proj.Backend, false)
	be, berr := agent.CreateBackend(backendID)
	if berr != nil {
		core.Fail(ctx, "bot", "comment-backend", berr)
		return berr
	}
	prompt := BuildCommentPrompt(ResolvedTarget{FileToken: fileToken, FileType: fileType},
		CommentContext{Question: cdata.Question, Quote: cdata.Quote, IsWhole: cdata.IsWhole, TargetReplyID: cdata.TargetReplyID},
		o.Cfg.Accounts.App.Tenant, readCommentInstructions())
	answer, rerr := o.runAgentSync(ctx, be, proj.Cwd, prompt)
	if rerr != nil {
		core.Fail(ctx, "bot", "comment-run", rerr)
		return rerr
	}
	answer = StripMarkdown(answer)
	if len([]rune(answer)) > ReplyMaxChars {
		runes := []rune(answer)
		answer = string(runes[:ReplyMaxChars])
	}
	if err := api.CreateFileCommentReply(ctx, fileToken, fileType, commentID, answer); err != nil {
		core.Fail(ctx, "bot", "comment-reply", err)
		return err
	}
	core.Info(ctx, "bot", "comment-replied", fmt.Sprintf("file=%s comment=%s project=%s len=%d", fileToken, commentID, proj.Name, len(answer)))
	return nil
}

// resolveCommentProject 由文档 file_token 定位运行项目（cwd）。
// 优先按项目 SourceURL 包含 file_token 匹配；否则若只有一个项目则直接用；
// 多项目且未配置 SourceURL 时无法唯一确定，返回错误（避免跑错目录）。
func (o *Orchestrator) resolveCommentProject(ctx context.Context, fileToken string) (*project.Project, error) {
	if o.ProjectStore == nil {
		return nil, fmt.Errorf("ProjectStore 未注入")
	}
	list, err := o.ProjectStore.List()
	if err != nil {
		return nil, err
	}
	if len(list) == 1 {
		return &list[0], nil
	}
	for i := range list {
		if list[i].SourceURL != "" && strings.Contains(list[i].SourceURL, fileToken) {
			return &list[i], nil
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("无项目可绑定云文档评论")
	}
	return nil, fmt.Errorf("存在 %d 个项目且未配置 SourceURL，无法确定运行目录；请在项目设置里填写云文档 URL", len(list))
}

// runAgentSync 一次性跑完 agent 并取最终文本（评论回信用，不流式、不建运行卡）。
func (o *Orchestrator) runAgentSync(ctx context.Context, be agent.AgentBackend, cwd, prompt string) (string, error) {
	thread, err := be.StartThread(ctx, agent.StartThreadOptions{Cwd: cwd})
	if err != nil {
		return "", fmt.Errorf("startThread: %w", err)
	}
	defer thread.Close(ctx)
	run := thread.RunStreamed(ctx, agent.AgentInput{Text: prompt}, nil)
	state := card.InitialState()
	for ev := range run.Events {
		state = card.Reduce(state, ev)
		if ev.Type == agent.EvDone || (ev.Type == agent.EvError && !ev.WillRetry) {
			break
		}
	}
	return card.FinalMessageText(state), nil
}

func hasBotMention(mentions []Mention) bool {
	// 简化：有任意 mention 即假设含 bot（精确判断需 bot open_id）。
	return len(mentions) > 0
}

// addProcessingReaction 给触发消息加"处理中"表情（对齐 TS runReaction：排队用 OneSecond，否则 Typing）。
// best-effort：Channel 未实现 ReactionAPI / 无 messageID / 调用失败都静默降级返回空串。
func addProcessingReaction(ctx context.Context, o *Orchestrator, messageID string) string {
	ra, ok := o.Channel.(ReactionAPI)
	if !ok || messageID == "" {
		return ""
	}
	emoji := "Typing"
	if !o.Semaphore.HasFree() {
		emoji = "OneSecond"
	}
	rid, err := ra.AddMessageReaction(ctx, messageID, emoji)
	if err != nil {
		core.Warn(ctx, "bot", "reaction-add", "add processing reaction failed: "+err.Error())
		return ""
	}
	return rid
}

// clearProcessingReaction 移除"处理中"表情（best-effort）。
func clearProcessingReaction(ctx context.Context, o *Orchestrator, messageID, reactionID string) {
	if reactionID == "" {
		return
	}
	if ra, ok := o.Channel.(ReactionAPI); ok {
		if err := ra.RemoveMessageReaction(ctx, messageID, reactionID); err != nil {
			core.Warn(ctx, "bot", "reaction-remove", "remove processing reaction failed: "+err.Error())
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
