package bot

// handle_turn.go —— Orchestrator 的核心编排方法（对齐 TS handle-message 的 handleTurn）。
// resolveThread 三级兜底 → semaphore acquire → StartThread/ResumeThread → runStreamed event loop → reduce → final card。
// 把全部已 port 模块（agent + card + sessionStore + semaphore）组装成可运行编排。

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// TurnInput handleTurn 输入。
type TurnInput struct {
	ChatID     string
	ThreadID   string // 会话键（multi=threadId，single=chatId）
	Cwd        string
	Text       string
	Images     []string
	Files      []InboundFileRef // 下载好的本地文件（weaveFileManifest 织入 prompt）
	SenderID   string
	SenderName string
	QuoteText  string // 引用消息正文（weaveQuote 织入 prompt）
	Project    *project.Project

	// MessageID 触发本条运行的消息 id（运行卡作为它的回复；topic 内则 replyInThread）。
	MessageID string
	// ReplyInThread 运行卡是否挂入该消息所在话题（multi 且已在 topic 内为 true）。
	ReplyInThread bool
}

// ModelCard 运行卡页脚要展示的模型名（无则空 → 不渲染）。
func (t TurnInput) ModelCard() string {
	if t.Project != nil {
		return t.Project.DefaultModel
	}
	return ""
}

// EffortCard 运行卡页脚要展示的推理强度（无则空）。
func (t TurnInput) EffortCard() agent.ReasoningEffort {
	if t.Project != nil {
		return t.Project.DefaultEffort
	}
	return ""
}

// InboundFileRef 下载好的入站文件引用。
type InboundFileRef struct {
	Path string
	Name string
}

// resolveThreadKey 会话键：multi 用 threadId；single 用 chatId。
func resolveThreadKey(input TurnInput) string {
	if input.ThreadID != "" {
		return input.ThreadID
	}
	return input.ChatID
}

// resolveThread 三级兜底：
// ① LIVE 快路径（sessions map 有活 thread）
// ② SessionStore resume（按记录的 backend 路由）
// ③ resume 失败 → startThread 重建 + patchSession 重指。
func (o *Orchestrator) resolveThread(ctx context.Context, be agent.AgentBackend, input TurnInput) (agent.AgentThread, string, error) {
	key := resolveThreadKey(input)

	// ① LIVE 快路径。
	if entryIface, ok := o.sessions.Load(key); ok {
		if entry, ok := entryIface.(*SessionEntry); ok && entry.Thread.IsAlive() {
			core.Info(ctx, "bot", "resolve-live", "LIVE 快路径命中: "+key)
			return entry.Thread, "live", nil
		}
		// LIVE 死了 → 从缓存删。
		o.sessions.Delete(key)
	}

	// ② SessionStore resume。
	rec, _ := o.SessionStore.Get(key)
	if rec != nil && rec.SessionID != "" {
		thread, err := be.ResumeThread(ctx, agent.ResumeThreadOptions{
			StartThreadOptions: agent.StartThreadOptions{
				Cwd:     input.Cwd,
				Mode:    effectiveMode(input.Project),
				Network: effectiveNetwork(input.Project),
			},
			SessionID: rec.SessionID,
		})
		if err == nil {
			core.Info(ctx, "bot", "resolve-resume", "resume 命中: "+rec.SessionID)
			o.sessions.Store(key, &SessionEntry{Thread: thread, Started: time.Now()})
			return thread, "resume", nil
		}
		// resume 失败 → ③ startThread 重建。
		core.Warn(ctx, "bot", "resolve-resume-fail", fmt.Sprintf("resume 失败，重建: %v", err))
	}

	// ③ startThread 重建。
	opts := agent.StartThreadOptions{
		Cwd:     input.Cwd,
		Mode:    effectiveMode(input.Project),
		Network: effectiveNetwork(input.Project),
	}
	if input.Project != nil && input.Project.Backend != "" {
		// backend 已固定在项目。
	}
	if input.Project != nil && input.Project.DefaultModel != "" {
		opts.Model = input.Project.DefaultModel
	}
	if input.Project != nil && input.Project.DefaultEffort != "" {
		opts.Effort = input.Project.DefaultEffort
	}
	thread, err := be.StartThread(ctx, opts)
	if err != nil {
		return nil, "", fmt.Errorf("startThread: %w", err)
	}
	// 记录 session。
	o.SessionStore.Upsert(SessionRecord{
		ThreadID:  key,
		ChatID:    input.ChatID,
		Cwd:       input.Cwd,
		SessionID: thread.SessionID(),
		Backend:   agent.DEFAULT_BACKEND_ID,
		Summary:   truncateStr(input.Text, 100),
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	})
	o.sessions.Store(key, &SessionEntry{Thread: thread, Started: time.Now()})
	core.Info(ctx, "bot", "resolve-start", "startThread 新建: "+thread.SessionID())
	return thread, "start", nil
}

// HandleTurn 核心：启动一轮 codex → 流式事件 → reduce → 流式运行卡 → 终态。
// 优先走 RunCardStream 实时 patch 运行卡（含 ⏹ 终止按钮，可经卡片/表情终止）；
// 没有 CardKitClient 时回退到「跑完发单张终态卡」。
func (o *Orchestrator) HandleTurn(ctx context.Context, be agent.AgentBackend, input TurnInput) error {
	key := resolveThreadKey(input)

	// 1. resolveThread。
	thread, source, err := o.resolveThread(ctx, be, input)
	if err != nil {
		return fmt.Errorf("resolveThread: %w", err)
	}
	core.Info(ctx, "bot", "turn-start", fmt.Sprintf("key=%s source=%s session=%s", key, source, thread.SessionID()))

	// 2. semaphore acquire。
	release := o.Semaphore.Acquire()
	defer release()

	// 3. 织入上下文（sender + quote + files）→ runStreamed event loop。
	prompt := input.Text
	if input.SenderID != "" {
		prompt = WeaveSender(prompt, input.SenderID, input.SenderName)
	}
	if input.QuoteText != "" {
		prompt = WeaveQuote(prompt, &ContextMessage{Text: input.QuoteText, SenderName: "用户"})
	}
	if len(input.Files) > 0 {
		var files []InboundFile
		for _, f := range input.Files {
			files = append(files, InboundFile{Path: f.Path, Name: f.Name})
		}
		prompt = WeaveFileManifest(prompt, files)
	}
	agentInput := agent.AgentInput{Text: prompt, Images: input.Images}

	// 运行 ctx 与卡片 patch ctx 分离：stop 只 cancel 运行 ctx，卡片 patch 不受影响。
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	run := thread.RunStreamed(runCtx, agentInput, nil)

	var state card.RunState
	var finalText string
	var lastUsage *agent.ContextUsage // 最近一次 token 用量（/context 用）

	// 4. 优先流式运行卡。
	stream, handle, sErr := o.streamRunCardCreate(ctx, input, false, runCancel)
	if sErr == nil && stream != nil {
		handle.thread = thread
		state = card.InitialState()
		var lastEvent string
		var eventCount int
		for ev := range run.Events {
			eventCount++
			lastEvent = ev.Type
			if ev.Type == agent.EvContextUsage {
				lastUsage = &agent.ContextUsage{UsedTokens: ev.UsedTokens, ContextWindow: ev.ContextWindow}
			}
			state = card.Reduce(state, ev)
			o.pushRunCard(ctx, stream, state, stream.MessageID(), input, false, false)
			if ev.Type == agent.EvDone || (ev.Type == agent.EvError && !ev.WillRetry) {
				break
			}
		}
		o.finalizeRunCard(ctx, stream, handle, state)
		finalText = card.FinalMessageText(state)
		// 终态后再补一条独立 @ 提醒（best-effort，按四档策略；仅 streamed 有运行卡可回复）。
		o.sendCompletionReminder(ctx, CompletionReminderReplyInput{
			CardMsgID:       stream.MessageID(),
			RequesterOpenID: input.SenderID,
			Outcome:         completionOutcomeFromTerminal(state.Terminal),
			RequestedAt:     handle.startedAt,
			Summary:         finalText,
			CardUpdated:     true,
			ReplyInThread:   input.ReplyInThread,
		})
		core.Info(ctx, "bot", "turn-done", fmt.Sprintf("key=%s events=%d last=%s terminal=%s streamed", key, eventCount, lastEvent, state.Terminal))
	} else {
		// 回退：跑完发单张终态卡。
		if sErr != nil {
			core.Warn(ctx, "bot", "stream-fallback", "流式运行卡不可用，回退单卡："+sErr.Error())
		}
		state = card.InitialState()
		var lastEvent string
		var eventCount int
		for ev := range run.Events {
			eventCount++
			lastEvent = ev.Type
			if ev.Type == agent.EvContextUsage {
				lastUsage = &agent.ContextUsage{UsedTokens: ev.UsedTokens, ContextWindow: ev.ContextWindow}
			}
			state = card.Reduce(state, ev)
			if ev.Type == agent.EvDone || (ev.Type == agent.EvError && !ev.WillRetry) {
				break
			}
		}
		finalText = card.FinalMessageText(state)
		core.Info(ctx, "bot", "turn-done", fmt.Sprintf("key=%s events=%d last=%s terminal=%s legacy", key, eventCount, lastEvent, state.Terminal))
		if o.SendCardFunc != nil {
			runCard := card.BuildRunCard(card.RunCardState{RS: state})
			jsonBytes, _ := json.Marshal(runCard)
			msgID, sendErr := o.SendCardFunc(ctx, input.ChatID, jsonBytes)
			if sendErr != nil {
				core.Fail(ctx, "bot", "send-card", sendErr)
			} else {
				core.Info(ctx, "bot", "card-sent", "终态卡片已发送: "+msgID)
			}
		} else {
			core.Warn(ctx, "bot", "no-send-card-func", "SendCardFunc 未注入")
		}
	}

	// 4b. 记录最近 token 用量（供 /context）。
	o.patchSessionUsage(key, lastUsage)

	// 5. 更新 session lastSeen。
	o.SessionStore.Patch(key, func(rec *SessionRecord) {
		rec.LastSeenAt = time.Now().UnixMilli()
		rec.UpdatedAt = time.Now().UnixMilli()
		if finalText != "" {
			rec.Summary = truncateStr(finalText, 100)
		}
	})

	return nil
}

// HandleGoal 自主多轮 goal 执行（thread.RunGoal → 事件流 → 流式运行卡（含 🎯 结束目标按钮）→ 终态）。
func (o *Orchestrator) HandleGoal(ctx context.Context, be agent.AgentBackend, input TurnInput) error {
	key := resolveThreadKey(input)
	thread, source, err := o.resolveThread(ctx, be, input)
	if err != nil {
		return fmt.Errorf("resolveThread: %w", err)
	}
	core.Info(ctx, "bot", "goal-start", fmt.Sprintf("key=%s source=%s session=%s", key, source, thread.SessionID()))

	release := o.Semaphore.Acquire()
	defer release()

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	run := thread.RunGoal(runCtx, input.Text)

	var state card.RunState
	var lastUsage *agent.ContextUsage // 最近一次 token 用量（/context 用）

	stream, handle, sErr := o.streamRunCardCreate(ctx, input, true, runCancel)
	if sErr == nil && stream != nil {
		handle.thread = thread
		state = card.InitialState()
		var goalStatus string
		var lastEvent string
		var eventCount int
		for ev := range run.Events {
			eventCount++
			lastEvent = ev.Type
			if ev.Type == agent.EvContextUsage {
				lastUsage = &agent.ContextUsage{UsedTokens: ev.UsedTokens, ContextWindow: ev.ContextWindow}
			}
			state = card.Reduce(state, ev)
			if ev.Type == agent.EvGoalUpdate {
				goalStatus = ev.Status
			}
			o.pushRunCard(ctx, stream, state, stream.MessageID(), input, true, handle.goalEnding.Load())
			if ev.Type == agent.EvDone {
				break
			}
			if ev.Type == agent.EvError && !ev.WillRetry {
				break
			}
		}
		o.finalizeRunCard(ctx, stream, handle, state)
		_ = card.FinalMessageText(state)
		core.Info(ctx, "bot", "goal-done", fmt.Sprintf("key=%s events=%d last=%s status=%s terminal=%s streamed", key, eventCount, lastEvent, goalStatus, state.Terminal))
	} else {
		if sErr != nil {
			core.Warn(ctx, "bot", "stream-fallback", "goal 流式运行卡不可用，回退单卡："+sErr.Error())
		}
		state = card.InitialState()
		var goalStatus string
		var lastEvent string
		var eventCount int
		for ev := range run.Events {
			eventCount++
			lastEvent = ev.Type
			if ev.Type == agent.EvContextUsage {
				lastUsage = &agent.ContextUsage{UsedTokens: ev.UsedTokens, ContextWindow: ev.ContextWindow}
			}
			state = card.Reduce(state, ev)
			if ev.Type == agent.EvGoalUpdate {
				goalStatus = ev.Status
			}
			if ev.Type == agent.EvDone {
				break
			}
			if ev.Type == agent.EvError && !ev.WillRetry {
				break
			}
		}
		_ = card.FinalMessageText(state)
		core.Info(ctx, "bot", "goal-done", fmt.Sprintf("key=%s events=%d last=%s status=%s terminal=%s legacy", key, eventCount, lastEvent, goalStatus, state.Terminal))
		// 回退：goal 终态额外发一张完成摘要卡（保留旧 UX）。
		if o.SendCardFunc != nil {
			goalCard := card.BuildGoalDoneCard(card.GoalDoneCardData{
				Objective: input.Text, Status: goalStatus,
			})
			jsonBytes, _ := json.Marshal(goalCard)
			if _, sendErr := o.SendCardFunc(ctx, input.ChatID, jsonBytes); sendErr != nil {
				core.Fail(ctx, "bot", "send-goal-card", sendErr)
			}
		}
	}

	// 记录最近 token 用量（供 /context）。
	o.patchSessionUsage(key, lastUsage)

	return nil
}
// patchSessionUsage 把最近一次 token 用量写进活跃会话（best-effort，/context 用）。
func (o *Orchestrator) patchSessionUsage(key string, u *agent.ContextUsage) {
	if u == nil {
		return
	}
	if e, ok := o.sessions.Load(key); ok {
		if entry, ok := e.(*SessionEntry); ok {
			entry.LastState = &SessionState{Usage: u}
		}
	}
}

func (o *Orchestrator) EvictLiveSession(chatID string) {
	o.sessions.Range(func(key, value any) bool {
		if entry, ok := value.(*SessionEntry); ok {
			if entry.Thread != nil && !entry.Thread.IsAlive() {
				o.sessions.Delete(key)
			}
		}
		return true
	})
}

func effectiveMode(p *project.Project) agent.PermissionMode {
	if p == nil {
		return agent.PermissionFull
	}
	return project.EffectiveMode(*p)
}

func effectiveNetwork(p *project.Project) bool {
	if p == nil || p.Network == nil {
		return false
	}
	return *p.Network
}

func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return s
}

// completionOutcomeFromTerminal 把运行卡终态映射为完成提醒策略理解的 Outcome。
// 中断/取消不在普通任务结束提醒范畴（用户主动操作），归为 interrupted（sendCompletionReminder 会跳过）。
func completionOutcomeFromTerminal(t card.Terminal) config.CompletionReminderOutcome {
	switch t {
	case card.TermDone:
		return config.ReminderDone
	case card.TermError:
		return config.ReminderError
	case card.TermIdleTimeout:
		return config.ReminderIdleTimeout
	case card.TermInterrupted:
		return config.ReminderInterrupted
	}
	return config.ReminderInterrupted
}

// 防止 unused import。
var _ = sync.Mutex{}
var _ = filepath.Join
