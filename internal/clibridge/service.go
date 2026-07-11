package clibridge

// service.go —— cli-bridge 服务编排（对齐 TS cli-bridge/service.ts）。
// 把 agent hook 推来的消息（审批 / 问答 / 完成）按 presence/notifyScope/allowCache
// 路由到飞书 owner 私聊，等待人在手机上点击 / 回复，再唤醒阻塞的 hook。

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

const (
	taskDoneClicked = "task_done_clicked"
	localReturn     = "local_return"
)

// ProjectRef 完成同步里匹配到的项目（已绑定 / 自动新建）。
type ProjectRef struct {
	ChatID string
	Name   string
	Kind   string // multi|single
}

// ServiceDeps 服务依赖（由 run 命令注入）。
type ServiceDeps struct {
	Cfg               config.AppConfig
	SocketPath        string
	SendOwnerCard     func(ctx context.Context, c card.CardObject) (messageID string, err error)
	UpdateOwnerCard   func(ctx context.Context, messageID string, c card.CardObject) bool
	SendGroupTopic    func(ctx context.Context, chatID, markdown string, replyInThread bool) error
	AddTypingReaction    func(ctx context.Context, messageID string) (reactionID string, err error)
	RemoveTypingReaction func(ctx context.Context, messageID, reactionID string) error
	IsBoundProject    func(cwd string) bool
	FindProjectByCwd func(cwd string) (*ProjectRef, error)
	CreateProjectForCwd func(cwd, source string) (*ProjectRef, error)
	LocalReturnPollMs int
	KeepAwake        KeepAwakeController
	// 可选注入（测试用）；缺省走基于 prefs 的 ioreg 读取。
	Presence    func() (CliPresenceRoute, error)
	LocalActivity func() (bool, error)
}

// OnMessageInput orchestrator 转来的私聊消息（用于续聊回复匹配）。
type OnMessageInput struct {
	ParentID string
	RootID   string
	Text     string
	MessageID string
}

// CliBridgeRuntimeHooks orchestrator 消耗的服务切面。
type CliBridgeRuntimeHooks interface {
	OnMessage(input OnMessageInput) bool
	RegisterCardActions(d *card.CardDispatcher)
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// Service cli-bridge 服务实例。
type Service struct {
	deps             ServiceDeps
	allowedSessions  map[string]struct{}
	awayNoticeSent   bool
	replyReaction    *replyReactionState
	ipc              *CliBridgeIpcServer
	presence         func() (CliPresenceRoute, error)
	localActivityFn  func() (bool, error)
}

type replyReactionState struct {
	messageID string
	idPromise chan string
}

// CreateCliBridgeService 构造服务。
func CreateCliBridgeService(deps ServiceDeps) *Service {
	if deps.LocalReturnPollMs <= 0 {
		deps.LocalReturnPollMs = 5000
	}
	if deps.KeepAwake == nil {
		deps.KeepAwake = CreateKeepAwakeController(func() bool {
			return config.GetCliBridgePreferences(deps.Cfg).KeepAwake.Enabled
		}, nil)
	}
	presence := deps.Presence
	if presence == nil {
		presence = func() (CliPresenceRoute, error) {
			return ResolveCliPresenceRoute(config.GetCliBridgePreferences(deps.Cfg))
		}
	}
	localActivity := deps.LocalActivity
	if localActivity == nil {
		localActivity = func() (bool, error) {
			act, err := ResolveCliLocalActivity(config.GetCliBridgePreferences(deps.Cfg))
			if err != nil {
				return false, err
			}
			return act.LocalActive, nil
		}
	}
	return &Service{
		deps:            deps,
		allowedSessions: map[string]struct{}{},
		presence:        presence,
		localActivityFn: localActivity,
	}
}

func (s *Service) prefs() config.ResolvedCliBridgePreferences {
	return config.GetCliBridgePreferences(s.deps.Cfg)
}

// SettingsSection 现算「☕ 咖啡一下」设置区元素（供 bot 层 BuildCoffeeSettingsCard 独立成卡）。
// 直接复用 BuildCliBridgeSettingsSection，确保主卡内联区与独立子卡完全一致。
func (s *Service) SettingsSection() []card.CardElement {
	p := s.prefs()
	claude, codex := InspectCliBridgeHooks(InspectCliBridgeHooksOptions{HomeDir: ""})
	statuses := map[CliBridgeAgent]CliHookStatus{
		AgentClaude: claude,
		AgentCodex:  codex,
	}
	_, canEnable := config.ResolveCliBridgeTarget(s.deps.Cfg)
	return BuildCliBridgeSettingsSection(CliBridgeSettingsSectionInput{
		Enabled:     p.Enabled,
		Statuses:    statuses,
		CanEnable:   canEnable,
		NotifyScope: p.NotifyScope,
		Agents: struct {
			Claude bool
			Codex  bool
		}{Claude: p.Agents.Claude, Codex: p.Agents.Codex},
		KeepAwake: p.KeepAwake.Enabled,
	})
}

func agentEnabled(p config.ResolvedCliBridgePreferences, source CliBridgeAgent) bool {
	if source == AgentCodex {
		return p.Agents.Codex
	}
	return p.Agents.Claude
}

func (s *Service) sessionKey(source, sessionID string) string {
	return source + ":" + sessionID
}

func (s *Service) markLocalActive() { s.awayNoticeSent = false }

func (s *Service) notifyAllowedForCwd(cwd string) bool {
	scope := s.prefs().NotifyScope
	if scope == "none" {
		return false
	}
	if scope == "bound_projects" {
		if s.deps.IsBoundProject == nil {
			return true
		}
		return s.deps.IsBoundProject(cwd)
	}
	return true
}

func (s *Service) resolveTarget() (string, bool) {
	return config.ResolveCliBridgeTarget(s.deps.Cfg)
}

func (s *Service) sendOwnerCard(ctx context.Context, c card.CardObject) (string, error) {
	return s.deps.SendOwnerCard(ctx, c)
}

func (s *Service) updateOwnerCard(ctx context.Context, messageID string, c card.CardObject) {
	if messageID == "" {
		return
	}
	s.deps.UpdateOwnerCard(ctx, messageID, c)
}

func (s *Service) renderPendingCard(pending *PendingCliInteraction, overrides struct {
	status        interactionStatus
	allowSession  bool
	answers       map[string]string
	replyEnabled  bool
	replyExpiresAt int64
	replyDoneAt   int64
}) card.CardObject {
	switch pending.Kind {
	case PendingPermission:
		return BuildCliBridgeApprovalCard(struct {
			ID           string
			Source       CliBridgeAgent
			Cwd          string
			ToolName     string
			Command      string
			AllowSession bool
			Status       interactionStatus
			HookEventName string
			SessionID    string
			CreatedAt    int64
		}{
			ID: pending.ID, Source: pending.Source, Cwd: pending.Cwd,
			ToolName: pending.ToolName, Command: pending.Command,
			AllowSession: overrides.allowSession, Status: overrides.status,
			HookEventName: pending.HookEventName,
		})
	case PendingQuestion:
		return BuildCliBridgeQuestionCard(struct {
			ID        string
			Source    CliBridgeAgent
			Cwd       string
			Questions []CliQuestionItem
			Status    interactionStatus
			Answers   map[string]string
			HookEventName string
			CreatedAt int64
		}{
			ID: pending.ID, Source: AgentClaude, Cwd: pending.Cwd,
			Questions: pending.Questions, Status: overrides.status, Answers: overrides.answers,
		})
	default:
		return BuildCliBridgeTaskCompletionCard(struct {
			ID             string
			Source         CliBridgeAgent
			Cwd            string
			Status         string
			Summary        string
			ReplyEnabled   bool
			SessionID      string
			HookEventName  string
			CreatedAt      int64
			ReplyExpiresAt int64
			ReplyDoneAt    int64
		}{
			ID: pending.ID, Source: pending.Source, Cwd: pending.Cwd,
			Status: pending.TaskStatus, Summary: pending.Summary,
			ReplyEnabled: overrides.replyEnabled, ReplyExpiresAt: overrides.replyExpiresAt,
			ReplyDoneAt: overrides.replyDoneAt,
		})
	}
}

func (s *Service) ensureAwayNoticeSent(ctx context.Context, target string, msg CliHookMessage) {
	if s.awayNoticeSent {
		return
	}
	s.awayNoticeSent = true
	_, _ = s.sendOwnerCard(ctx, BuildCliBridgeAwayNoticeCard(struct {
		Source CliBridgeAgent
		Cwd    string
		Key    string
	}{Source: msg.Source, Cwd: msg.Cwd, Key: msg.SessionID}))
}

// waitWithLocalReturn 等飞书决策，同时轮询本机回归（人回键盘即把控制权还终端）。
func (s *Service) waitWithLocalReturn(ctx context.Context, id string, timeoutMs int, onLocalReturn CliHookResponse) CliHookResponse {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Duration(s.deps.LocalReturnPollMs) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				active, err := s.localActivityFn()
				if err == nil && active {
					s.markLocalActive()
					if GetPendingCliInteraction(id) != nil {
						ResolvePendingCliInteraction(id, onLocalReturn)
					}
					return
				}
			}
		}
	}()
	s.deps.KeepAwake.Acquire()
	resp := WaitForPendingCliInteraction(id, timeoutMs)
	s.deps.KeepAwake.Release()
	close(done)
	return resp
}

func (s *Service) localActivity() (bool, error) {
	return s.localActivityFn()
}

// HandleMessage hook 消息主处理（IPC server 调用）。
func (s *Service) HandleMessage(msg CliHookMessage) (CliHookResponse, error) {
	p := s.prefs()
	if !p.Enabled || !agentEnabled(p, msg.Source) {
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "disabled"}, nil
	}
	if msg.BridgeOwned && !p.IncludeBridgeOwnedSessionsForDebugging {
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "bridge_owned_session"}, nil
	}
	if msg.Type == MsgTypePostToolUse {
		return CliHookResponse{Decision: DecisionAllow}, nil
	}

	route, err := s.presence()
	if err != nil {
		route = CliPresenceRoute{RouteToFeishu: false, Reason: "presence_failed"}
	}
	// delivery=always：忽略 presence，始终路由到飞书（用于调试 / 用户希望随时手机审批）。
	if p.Delivery == "always" {
		route.RouteToFeishu = true
		if route.Reason == "local_active" || route.Reason == "presence_failed" {
			route.Reason = "delivery_always"
		}
	}
	core.Info(context.Background(), "cli-bridge", "hook-recv",
		"type="+msg.Type+" source="+msg.Source+" event="+msg.HookEventName+" route="+route.Reason)
	if route.Reason == "local_active" {
		s.markLocalActive()
	}

	if msg.Type == MsgTypeTaskComplete {
		s.clearReplyTypingReaction()
		if p.CompletionSync.Enabled {
			s.runCompletionSync(msg)
		}
		if !p.TaskCompletion.Enabled {
			return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "task_completion_disabled"}, nil
		}
		if !route.RouteToFeishu {
			return CliHookResponse{Decision: DecisionFallbackLocal, Reason: route.Reason}, nil
		}
		if !s.notifyAllowedForCwd(msg.Cwd) {
			return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "notify_scope"}, nil
		}
		target, ok := s.resolveTarget()
		if !ok {
			return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "missing_owner"}, nil
		}
		localActive, _ := s.localActivity()
		canReply := p.TaskCompletion.ReplyEnabled && !localActive
		var pending *PendingCliInteraction
		if canReply {
			pending = CreatePendingCliInteraction(PendingCliInteraction{
				Kind: PendingTaskCompletion, Source: msg.Source, SessionID: msg.SessionID,
				Cwd: msg.Cwd, HookEventName: msg.HookEventName,
				TaskStatus: msg.TaskStatus, Summary: msg.Summary,
				ReplyExpiresAt: time.Now().UnixMilli() + int64(p.TaskCompletion.ReplyTimeoutSeconds)*1000,
			})
		}
		if route.Reason == "away" {
			s.ensureAwayNoticeSent(context.Background(), target, msg)
		}
		cardObj := s.renderPendingCard(pending, struct {
			status         interactionStatus
			allowSession   bool
			answers        map[string]string
			replyEnabled   bool
			replyExpiresAt int64
			replyDoneAt    int64
		}{replyEnabled: canReply, replyExpiresAt: func() int64 {
			if canReply {
				return time.Now().UnixMilli() + int64(p.TaskCompletion.ReplyTimeoutSeconds)*1000
			}
			return 0
		}()})
		sent, err := s.sendOwnerCard(context.Background(), cardObj)
		if err != nil {
			return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "send_failed:" + err.Error()}, nil
		}
		if pending == nil {
			return CliHookResponse{Decision: DecisionAllow}, nil
		}
		SetPendingCliMessageId(pending.ID, sent)
		result := s.waitWithLocalReturn(context.Background(), pending.ID, p.TaskCompletion.ReplyTimeoutSeconds*1000,
			CliHookResponse{Decision: DecisionAllow})
		if result.Reason != taskDoneClicked {
			s.updateOwnerCard(context.Background(), sent, s.renderPendingCard(pending, struct {
				status         interactionStatus
				allowSession   bool
				answers        map[string]string
				replyEnabled   bool
				replyExpiresAt int64
				replyDoneAt    int64
			}{}))
		}
		return result, nil
	}

	if !route.RouteToFeishu {
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: route.Reason}, nil
	}
	target, ok := s.resolveTarget()
	if !ok {
		core.Warn(context.Background(), "cli-bridge", "resolve-target", "找不到 owner open_id（cliBridge 通知目标为空）")
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "missing_owner"}, nil
	}

	// AskUserQuestion：仅 Claude 支持结构化表单。
	if msg.Source == AgentClaude && msg.ToolName == "AskUserQuestion" {
		if !s.notifyAllowedForCwd(msg.Cwd) {
			return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "notify_scope"}, nil
		}
		ask := ExtractAskUserQuestion(msg.ToolInput)
		if ask == nil {
			return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "unsupported_ask_user_question"}, nil
		}
		pending := CreatePendingCliInteraction(PendingCliInteraction{
			Kind: PendingQuestion, Source: msg.Source, SessionID: msg.SessionID,
			Cwd: msg.Cwd, Questions: ask.Questions, Question: ask.Questions[0].Question,
			HookEventName: msg.HookEventName, ToolInput: msg.ToolInput,
		})
		if route.Reason == "away" {
			s.ensureAwayNoticeSent(context.Background(), target, msg)
		}
	sent, err := s.sendOwnerCard(context.Background(), s.renderPendingCard(pending, struct {
		status         interactionStatus
		allowSession   bool
		answers        map[string]string
		replyEnabled   bool
		replyExpiresAt int64
		replyDoneAt    int64
	}{}))
	if err != nil {
		core.Warn(context.Background(), "cli-bridge", "send-owner-card", "AskUserQuestion 卡片发送失败: "+err.Error())
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "send_failed:" + err.Error()}, nil
	}
		SetPendingCliMessageId(pending.ID, sent)
		result := s.waitWithLocalReturn(context.Background(), pending.ID, p.Approval.TimeoutSeconds*1000,
			CliHookResponse{Decision: DecisionFallbackLocal, Reason: localReturn})
		if result.Reason == localReturn {
			s.updateOwnerCard(context.Background(), sent, s.renderPendingCard(pending, struct {
				status         interactionStatus
				allowSession   bool
				answers        map[string]string
				replyEnabled   bool
				replyExpiresAt int64
				replyDoneAt    int64
			}{status: stLocal}))
		}
		return result, nil
	}

	if !p.Approval.Enabled {
		core.Warn(context.Background(), "cli-bridge", "approval", "approval 未启用（cliBridge.approval.enabled=false）")
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "approval_disabled"}, nil
	}
	if p.AllowCache.Enabled && func() bool {
		_, ok := s.allowedSessions[s.sessionKey(msg.Source, msg.SessionID)]
		return ok
	}() {
		core.Info(context.Background(), "cli-bridge", "allow-cache", "命中 allow-cache，直接放行")
		return CliHookResponse{Decision: DecisionAllow}, nil
	}
	if !s.notifyAllowedForCwd(msg.Cwd) {
		core.Warn(context.Background(), "cli-bridge", "notify-scope", "cwd 不在 notify 范围（scope="+s.prefs().NotifyScope+"）")
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "notify_scope"}, nil
	}
	command, _ := msg.ToolInput["command"].(string)
	pending := CreatePendingCliInteraction(PendingCliInteraction{
		Kind: PendingPermission, Source: msg.Source, SessionID: msg.SessionID,
		Cwd: msg.Cwd, ToolName: msg.ToolName, Command: command,
		HookEventName: msg.HookEventName, Question: "Permission request",
	})
	if route.Reason == "away" {
		s.ensureAwayNoticeSent(context.Background(), target, msg)
	}
	sent, err := s.sendOwnerCard(context.Background(), s.renderPendingCard(pending, struct {
		status         interactionStatus
		allowSession   bool
		answers        map[string]string
		replyEnabled   bool
		replyExpiresAt int64
		replyDoneAt    int64
	}{allowSession: p.AllowCache.Enabled}))
	if err != nil {
		core.Warn(context.Background(), "cli-bridge", "send-owner-card", "审批卡片发送失败: "+err.Error())
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "send_failed:" + err.Error()}, nil
	}
	core.Info(context.Background(), "cli-bridge", "card-sent", "审批卡已发往飞书 owner（msgID="+sent+"），等待手机点击")
	SetPendingCliMessageId(pending.ID, sent)
	result := s.waitWithLocalReturn(context.Background(), pending.ID, p.Approval.TimeoutSeconds*1000,
		CliHookResponse{Decision: DecisionFallbackLocal, Reason: localReturn})
	if result.Reason == localReturn {
		s.updateOwnerCard(context.Background(), sent, s.renderPendingCard(pending, struct {
			status         interactionStatus
			allowSession   bool
			answers        map[string]string
			replyEnabled   bool
			replyExpiresAt int64
			replyDoneAt    int64
		}{status: stLocal}))
	}
	return result, nil
}

func (s *Service) runCompletionSync(msg CliHookMessage) {
	clipped := Clip(strings.TrimSpace(msg.Summary), 2000)
	sendGroupTopic := func(chatID, name, kind string, auto bool) {
		body := "✅ **" + name + "** 任务完成\n\n" + clipped
		openThread := kind == "multi"
		if s.deps.SendGroupTopic == nil {
			core.Warn(context.Background(), "cli-bridge", "group-topic", "SendGroupTopic 未接入(nil)，跳过群话题")
			return
		}
		if e := s.deps.SendGroupTopic(context.Background(), chatID, body, openThread); e != nil {
			core.Warn(context.Background(), "cli-bridge", "group-topic", "群话题发送失败 chatID="+chatID+" err="+e.Error())
		} else {
			core.Info(context.Background(), "cli-bridge", "group-topic", "群话题已发送 chatID="+chatID)
		}
		_ = auto
	}
	if s.deps.FindProjectByCwd != nil {
		project, _ := s.deps.FindProjectByCwd(msg.Cwd)
		if project == nil && s.deps.CreateProjectForCwd != nil {
			created, _ := s.deps.CreateProjectForCwd(msg.Cwd, msg.Source)
			if created != nil && created.ChatID != "" {
				sendGroupTopic(created.ChatID, created.Name, "multi", true)
				return
			}
		}
		if project != nil && project.ChatID != "" {
			sendGroupTopic(project.ChatID, project.Name, project.Kind, false)
			return
		}
	}
	// 兜底：owner 私聊结果卡。
	target, ok := s.resolveTarget()
	if !ok {
		return
	}
	_, _ = s.sendOwnerCard(context.Background(), BuildCliBridgeNoticeCard(struct {
		Source CliBridgeAgent
		Cwd    string
		Title  string
		Body   string
	}{Source: msg.Source, Cwd: msg.Cwd, Title: "任务完成（未建群）", Body: clipped}))
	_ = target
}

// ── 决策 resolve ───────────────────────────────────────────────

func (s *Service) resolveAction(actionID, id string) bool {
	switch actionID {
	case CLI.ApproveOnce:
		pending := GetPendingCliInteraction(id)
		if pending == nil || pending.Kind != PendingPermission {
			return false
		}
		s.updateOwnerCard(context.Background(), pending.MessageID, s.renderPendingCard(pending, struct {
			status         interactionStatus
			allowSession   bool
			answers        map[string]string
			replyEnabled   bool
			replyExpiresAt int64
			replyDoneAt    int64
		}{status: stApproved}))
		return ResolvePendingCliInteraction(id, CliHookResponse{Decision: DecisionAllow})
	case CLI.ApproveSession:
		pending := GetPendingCliInteraction(id)
		if pending == nil || pending.Kind != PendingPermission {
			return false
		}
		s.updateOwnerCard(context.Background(), pending.MessageID, s.renderPendingCard(pending, struct {
			status         interactionStatus
			allowSession   bool
			answers        map[string]string
			replyEnabled   bool
			replyExpiresAt int64
			replyDoneAt    int64
		}{status: stApproved}))
		ok := ResolvePendingCliInteraction(id, CliHookResponse{Decision: DecisionAllow})
		if ok && s.prefs().AllowCache.Enabled {
			s.allowedSessions[s.sessionKey(pending.Source, pending.SessionID)] = struct{}{}
		}
		return ok
	case CLI.Deny:
		pending := GetPendingCliInteraction(id)
		if pending == nil || pending.Kind != PendingPermission {
			return false
		}
		s.updateOwnerCard(context.Background(), pending.MessageID, s.renderPendingCard(pending, struct {
			status         interactionStatus
			allowSession   bool
			answers        map[string]string
			replyEnabled   bool
			replyExpiresAt int64
			replyDoneAt    int64
		}{status: stDenied}))
		return ResolvePendingCliInteraction(id, CliHookResponse{Decision: DecisionDeny, Interrupt: true, Reason: "Denied from Feishu"})
	case CLI.TaskCompletionDone:
		pending := GetPendingCliInteraction(id)
		if pending == nil || pending.Kind != PendingTaskCompletion {
			return false
		}
		s.updateOwnerCard(context.Background(), pending.MessageID, s.renderPendingCard(pending, struct {
			status         interactionStatus
			allowSession   bool
			answers        map[string]string
			replyEnabled   bool
			replyExpiresAt int64
			replyDoneAt    int64
		}{replyDoneAt: time.Now().UnixMilli()}))
		return ResolvePendingCliInteraction(id, CliHookResponse{Decision: DecisionAllow, Reason: taskDoneClicked})
	}
	return false
}

func (s *Service) resolveQuestionSubmit(id string, formValue map[string]any) bool {
	pending := GetPendingCliInteraction(id)
	if pending == nil || pending.Kind != PendingQuestion {
		return false
	}
	questions := pending.Questions
	answers := map[string]string{}
	for i, q := range questions {
		custom := strings.TrimSpace(stringOrT(formValue[QuestionCustomField(i)]))
		if custom != "" {
			answers[q.Question] = custom
			continue
		}
		choice := formValue[QuestionChoiceField(i)]
		switch v := choice.(type) {
		case []any:
			picked := make([]string, 0, len(v))
			for _, c := range v {
				if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
					picked = append(picked, strings.TrimSpace(s))
				}
			}
			if len(picked) > 0 {
				answers[q.Question] = strings.Join(picked, "、")
			}
		case string:
			if strings.TrimSpace(v) != "" {
				answers[q.Question] = strings.TrimSpace(v)
			}
		}
	}
	if len(answers) == 0 {
		return false
	}
	s.updateOwnerCard(context.Background(), pending.MessageID, s.renderPendingCard(pending, struct {
		status         interactionStatus
		allowSession   bool
		answers        map[string]string
		replyEnabled   bool
		replyExpiresAt int64
		replyDoneAt    int64
	}{status: stApproved, answers: answers}))
	return ResolvePendingCliInteraction(id, CliHookResponse{
		Decision:    DecisionAllow,
		UpdatedInput: func() map[string]any {
			ti := pending.ToolInput
			if ti == nil {
				ti = map[string]any{}
			}
			out := map[string]any{}
			for k, v := range ti {
				out[k] = v
			}
			out["answers"] = answers
			return out
		}(),
	})
}

func (s *Service) resolveReply(reply OnMessageInput) bool {
	pending := FindPendingCliInteractionByMessageReply(struct {
		ParentID string
		RootID   string
	}{ParentID: reply.ParentID, RootID: reply.RootID})
	text := strings.TrimSpace(reply.Text)
	if pending == nil || pending.Kind != PendingTaskCompletion || text == "" {
		return false
	}
	ok := ResolvePendingCliInteraction(pending.ID, CliHookResponse{
		Decision: DecisionAllow,
		Stdout:   `{"decision":"block","reason":` + jsonString(text) + `}`,
	})
	if ok && reply.MessageID != "" {
		s.armReplyTypingReaction(reply.MessageID)
	}
	return ok
}

func (s *Service) armReplyTypingReaction(messageID string) {
	s.clearReplyTypingReaction()
	state := &replyReactionState{messageID: messageID, idPromise: make(chan string, 1)}
	s.replyReaction = state
	if s.deps.AddTypingReaction != nil {
		go func() {
			id, err := s.deps.AddTypingReaction(context.Background(), messageID)
			if err != nil {
				return
			}
			state.idPromise <- id
		}()
	}
}

func (s *Service) clearReplyTypingReaction() {
	state := s.replyReaction
	s.replyReaction = nil
	if state == nil || s.deps.RemoveTypingReaction == nil {
		return
	}
	go func() {
		id, ok := <-state.idPromise
		if !ok || id == "" {
			return
		}
		_ = s.deps.RemoveTypingReaction(context.Background(), state.messageID, id)
	}()
}

// ── CliBridgeRuntimeHooks 实现 ────────────────────────────────

// OnMessage 私聊消息入口：优先尝试续聊回复匹配，命中则吞掉（不弹菜单）。
func (s *Service) OnMessage(input OnMessageInput) bool {
	if s.resolveReply(input) {
		return true
	}
	return FindPendingCliInteractionByMessageReply(struct {
		ParentID string
		RootID   string
	}{ParentID: input.ParentID, RootID: input.RootID}) != nil
}

// RegisterCardActions 注册桥卡回调。
func (s *Service) RegisterCardActions(d *card.CardDispatcher) {
	d.On(CLI.ApproveOnce, func(ccx card.CardActionContext) error {
		s.resolveAction(CLI.ApproveOnce, stringOf(ccx.Value["id"]))
		return nil
	})
	d.On(CLI.ApproveSession, func(ccx card.CardActionContext) error {
		s.resolveAction(CLI.ApproveSession, stringOf(ccx.Value["id"]))
		return nil
	})
	d.On(CLI.Deny, func(ccx card.CardActionContext) error {
		s.resolveAction(CLI.Deny, stringOf(ccx.Value["id"]))
		return nil
	})
	d.On(CLI.TaskCompletionDone, func(ccx card.CardActionContext) error {
		s.resolveAction(CLI.TaskCompletionDone, stringOf(ccx.Value["id"]))
		return nil
	})
	d.On(CLI.QuestionSubmit, func(ccx card.CardActionContext) error {
		s.resolveQuestionSubmit(stringOf(ccx.Value["id"]), ccx.FormValue)
		return nil
	})
}

// Start 启动 IPC server（阻塞接受 hook 连接）。
func (s *Service) Start(ctx context.Context) error {
	srv, err := StartCliBridgeIpcServer(s.deps.SocketPath, s.HandleMessage)
	if err != nil {
		return err
	}
	s.ipc = srv
	core.Info(ctx, "cli-bridge", "started", "socketPath="+s.deps.SocketPath)
	return nil
}

// Shutdown 关闭 IPC server + 释放保活。
func (s *Service) Shutdown(ctx context.Context) error {
	if s.ipc != nil {
		_ = s.ipc.Close()
		s.ipc = nil
	}
	s.allowedSessions = map[string]struct{}{}
	if s.deps.KeepAwake != nil {
		s.deps.KeepAwake.Shutdown()
	}
	return nil
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
