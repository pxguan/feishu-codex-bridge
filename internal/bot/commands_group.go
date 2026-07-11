package bot

// commands_group.go —— 群内斜杠命令（/model /resume /compact /context）的实现。
// 对齐 TS handle-message 的对应命令；补齐此前为占位的群命令。

import (
	"context"
	"fmt"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/admin"
	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// ── /model ──

// handleModelCommand 群内 /model：拉后端模型列表，发模型/强度选择卡（选择即生效）。
func (o *Orchestrator) handleModelCommand(ctx context.Context, msg NormalizedMessage, proj *project.Project) {
	if !config.IsAdmin(o.Cfg, msg.SenderID) {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("🔒 修改默认模型需要管理员权限。"),
		}, card.CardOpts{Summary: "需要管理员"}))
		return
	}
	rows, _, err := o.listProjectModels(ctx, proj.Backend)
	if err != nil {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("⚠️ 无法获取模型列表：" + err.Error()),
		}, card.CardOpts{Summary: "模型列表失败"}))
		return
	}
	eff := agent.ReasoningEffort(proj.DefaultEffort)
	if eff == "" {
		eff = agent.EffortMedium
	}
	o.sendCard(ctx, msg.ChatID, card.BuildModelCard(card.ModelCardState{
		ChatID: msg.ChatID,
		Models: modelRowsToInfos(rows),
		Model:  proj.DefaultModel,
		Effort: eff,
	}))
}

// handleModelSelect 模型/强度选择（MCModel / MCEffort）即时生效：落盘 + 驱逐活跃会话让新模型立即生效。
func (o *Orchestrator) handleModelSelect(cca card.CardActionContext, field string) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	p, _ := o.ProjectStore.GetByChatID(cca.Evt.ChatID)
	if p == nil {
		return
	}
	rows, _, err := o.listProjectModels(cca.Ctx, p.Backend)
	if err != nil {
		return
	}
	modelID := p.DefaultModel
	effort := agent.ReasoningEffort(p.DefaultEffort)
	if field == "model" {
		modelID = stringOf(cca.Value["model"])
	} else {
		effort = agent.ReasoningEffort(stringOf(cca.Value["effort"]))
	}
	// 校验模型是否在后端列表内。
	var m *card.ModelRow
	for i := range rows {
		if rows[i].ID == modelID {
			m = &rows[i]
			break
		}
	}
	if modelID != "" && m == nil {
		modelID = p.DefaultModel // 非法模型，回退旧值
	}
	// 校验强度是否被该模型支持。
	if m != nil {
		supported := false
		for _, e := range m.SupportedEfforts {
			if e == string(effort) {
				supported = true
				break
			}
		}
		if !supported {
			if len(m.SupportedEfforts) > 0 {
				effort = agent.ReasoningEffort(m.SupportedEfforts[0])
			} else {
				effort = ""
			}
		}
	}
	out := admin.PerformSetModelDefault(o.ProjectStore, p.Name, modelID, effort)
	if !out.Ok {
		return
	}
	// 驱逐活跃会话，使下次对话用上新模型/强度。
	o.evictLiveSessionsForChat(cca.Evt.ChatID)
	// 重渲染卡片，反映新选择。
	o.sendCardAction(cca, card.BuildModelCard(card.ModelCardState{
		ChatID: cca.Evt.ChatID,
		Models: modelRowsToInfos(rows),
		Model:  modelID,
		Effort: effort,
	}))
}

// modelRowsToInfos 把 []ModelRow 转成 BuildModelCard 需要的 []agent.ModelInfo。
func modelRowsToInfos(rows []card.ModelRow) []agent.ModelInfo {
	out := make([]agent.ModelInfo, 0, len(rows))
	for _, r := range rows {
		effs := make([]agent.ReasoningEffort, 0, len(r.SupportedEfforts))
		for _, e := range r.SupportedEfforts {
			effs = append(effs, agent.ReasoningEffort(e))
		}
		out = append(out, agent.ModelInfo{ID: r.ID, DisplayName: r.DisplayName, SupportedEfforts: effs})
	}
	return out
}

// ── /resume ──

// handleResumeCommand 群内 /resume：列出该目录历史会话，发恢复选择卡。
func (o *Orchestrator) handleResumeCommand(ctx context.Context, msg NormalizedMessage, proj *project.Project) {
	if !config.IsAdmin(o.Cfg, msg.SenderID) {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("🔒 恢复历史会话需要管理员权限。"),
		}, card.CardOpts{Summary: "需要管理员"}))
		return
	}
	backendID := agent.BackendForProject(proj.Backend, false)
	be, err := agent.CreateBackend(backendID)
	if err != nil {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("⚠️ 无法创建后端：" + err.Error()),
		}, card.CardOpts{Summary: "后端失败"}))
		return
	}
	threads, err := be.ListThreads(ctx, proj.Cwd, 20)
	if err != nil {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("⚠️ 无法列出历史会话：" + err.Error()),
		}, card.CardOpts{Summary: "列出失败"}))
		return
	}
	o.sendCard(ctx, msg.ChatID, card.BuildResumeCard(card.ResumeCardState{
		ChatID:      msg.ChatID,
		Cwd:         proj.Cwd,
		ProjectName: proj.Name,
		Backend:     proj.Backend,
		Threads:     threads,
	}, time.Now()))
}

// handleResumePick RESPick：恢复选中的历史会话，接管当前群活跃会话。
func (o *Orchestrator) handleResumePick(cca card.CardActionContext) {
	if !o.dmAdmin(cca.Evt.Operator.OpenID) {
		return
	}
	sessionID := stringOf(cca.Value["t"])
	backend := stringOf(cca.Value["b"])
	if sessionID == "" {
		return
	}
	p, _ := o.ProjectStore.GetByChatID(cca.Evt.ChatID)
	if p == nil {
		return
	}
	if backend == "" {
		backend = p.Backend
	}
	// 先发"恢复中"卡（同步，确保卡片回调 3s 内 ack），后续异步接管。
	o.sendCardAction(cca, card.BuildResumeLaunchingCard(card.ResumeCardState{
		ChatID: cca.Evt.ChatID, Cwd: p.Cwd, ProjectName: p.Name, Backend: backend,
	}))
	go func() {
		backendID := agent.BackendForProject(backend, false)
		be, err := agent.CreateBackend(backendID)
		if err != nil {
			o.sendCardAction(cca, card.BuildResumeErrorCard(card.ResumeCardState{
				ChatID: cca.Evt.ChatID, Cwd: p.Cwd, ProjectName: p.Name, Backend: backend,
			}, err.Error()))
			return
		}
		thread, err := be.ResumeThread(cca.Ctx, agent.ResumeThreadOptions{
			StartThreadOptions: agent.StartThreadOptions{
				Cwd:     p.Cwd,
				Mode:    effectiveMode(p),
				Network: effectiveNetwork(p),
			},
			SessionID: sessionID,
		})
		if err != nil {
			o.sendCardAction(cca, card.BuildResumeErrorCard(card.ResumeCardState{
				ChatID: cca.Evt.ChatID, Cwd: p.Cwd, ProjectName: p.Name, Backend: backend,
			}, err.Error()))
			return
		}
		// 接管活跃会话：驱逐旧的，挂上新 thread，并写入 session 记录。
		o.evictLiveSessionsForChat(cca.Evt.ChatID)
		o.sessions.Store(cca.Evt.ChatID, &SessionEntry{Thread: thread, Started: time.Now()})
		o.SessionStore.Patch(cca.Evt.ChatID, func(rec *SessionRecord) {
			rec.SessionID = sessionID
			rec.ChatID = cca.Evt.ChatID
			rec.Cwd = p.Cwd
			rec.Backend = backend
			rec.UpdatedAt = time.Now().UnixMilli()
			rec.LastSeenAt = time.Now().UnixMilli()
		})
		o.sendCardAction(cca, card.BuildResumeDoneCard(card.ResumeCardState{
			ChatID: cca.Evt.ChatID, Cwd: p.Cwd, ProjectName: p.Name, Backend: backend,
		}))
	}()
}

// ── /compact ──

// handleCompactCommand 群内 /compact：压缩当前活跃会话的上下文。
func (o *Orchestrator) handleCompactCommand(ctx context.Context, msg NormalizedMessage, proj *project.Project) {
	entryIface, ok := o.sessions.Load(msg.ChatID)
	if !ok {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("🗜️ 当前没有活跃会话可压缩。先 @我 聊一句建立会话。"),
		}, card.CardOpts{Summary: "无活跃会话"}))
		return
	}
	entry, ok := entryIface.(*SessionEntry)
	if !ok || entry.Thread == nil {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("🗜️ 当前没有活跃会话可压缩。"),
		}, card.CardOpts{Summary: "无活跃会话"}))
		return
	}
	o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
		card.Md("🗜️ 正在压缩上下文…"),
	}, card.CardOpts{Summary: "压缩中"}))
	go func() {
		cctx := core.WithTrace(context.Background(), core.NewTraceID(), msg.ChatID, msg.MessageID)
		res, err := entry.Thread.Compact(cctx)
		if err != nil {
			o.sendCard(cctx, msg.ChatID, card.Card([]card.CardElement{
				card.Md("❌ 压缩失败：" + truncateStr(err.Error(), 200)),
			}, card.CardOpts{Summary: "压缩失败"}))
			return
		}
		note := "✅ 已压缩上下文。"
		if !res.Compacted {
			note = "✅ 上下文无需压缩（或后端未回报结果）。"
		}
		o.sendCard(cctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md(note),
		}, card.CardOpts{Summary: "已压缩"}))
	}()
}

// ── /clear ──

// handleClearCommand 群内 /clear（仅单会话群、仅管理员）：清空当前会话、重绑全新 thread
// （旧 thread 磁盘留档可 /resume）。对齐 TS runClear——重置「对话」而非用户模型/强度选择。
func (o *Orchestrator) handleClearCommand(ctx context.Context, msg NormalizedMessage, proj *project.Project) {
	if !config.IsAdmin(o.Cfg, msg.SenderID) {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("🔒 清空会话需要管理员权限。"),
		}, card.CardOpts{Summary: "需要管理员"}))
		return
	}
	if proj.Kind != "single" {
		o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("⚠️ `/clear` 仅单会话群可用。多话题群里，回主群区 @我 + 内容 即开新话题；`/resume` 可恢复历史会话。"),
		}, card.CardOpts{Summary: "不支持 /clear"}))
		return
	}
	o.sendCard(ctx, msg.ChatID, card.Card([]card.CardElement{card.Md("⏳ 正在清空会话…")}, card.CardOpts{Summary: "清空中"}))
	go func() {
		cctx := core.WithTrace(context.Background(), core.NewTraceID(), msg.ChatID, msg.MessageID)
		// 复用会话记录（key=chatId）取后端/模型/目录；缺失则回退项目。
		rec, _ := o.SessionStore.Get(msg.ChatID)
		backend := proj.Backend
		if rec != nil && rec.Backend != "" {
			backend = rec.Backend
		}
		cwd := proj.Cwd
		if rec != nil && rec.Cwd != "" {
			cwd = rec.Cwd
		}
		model := ""
		effort := agent.ReasoningEffort("")
		if rec != nil {
			model = rec.Model
			effort = rec.Effort
		}
		backendID := agent.BackendForProject(backend, false)
		be, err := agent.CreateBackend(backendID)
		if err != nil {
			o.sendCard(cctx, msg.ChatID, card.Card([]card.CardElement{card.Md("❌ 清空失败：" + err.Error())}, card.CardOpts{Summary: "清空失败"}))
			return
		}
		// 先关闭并驱逐旧 live thread（磁盘会话留档可 /resume）。
		if oldI, ok := o.sessions.Load(msg.ChatID); ok {
			if old, ok := oldI.(*SessionEntry); ok && old.Thread != nil {
				_ = old.Thread.Close(cctx)
			}
			o.sessions.Delete(msg.ChatID)
		}
		ac := true
		if proj.AutoCompact != nil {
			ac = *proj.AutoCompact
		}
		fresh, err := be.StartThread(cctx, agent.StartThreadOptions{
			Cwd:        cwd,
			Model:      model,
			Effort:     effort,
			Mode:       effectiveMode(proj),
			Network:    effectiveNetwork(proj),
			AutoCompact: &ac,
		})
		if err != nil {
			o.sendCard(cctx, msg.ChatID, card.Card([]card.CardElement{card.Md("❌ 清空失败：" + err.Error())}, card.CardOpts{Summary: "清空失败"}))
			return
		}
		// 挂上全新 thread，并写入会话记录（summary 标「新会话」，sessionId 更新）。
		o.sessions.Store(msg.ChatID, &SessionEntry{Thread: fresh, Started: time.Now()})
		o.SessionStore.Upsert(SessionRecord{
			ThreadID:   msg.ChatID,
			ChatID:     msg.ChatID,
			Cwd:        cwd,
			Backend:    backend,
			Model:      model,
			Effort:     effort,
			SessionID:  fresh.SessionID(),
			Summary:    "(新会话)",
			CreatedAt:  timeNowMs(),
			UpdatedAt:  timeNowMs(),
			LastSeenAt: timeNowMs(),
		})
		o.sendCard(cctx, msg.ChatID, card.Card([]card.CardElement{
			card.Md("✅ 已清空当前会话，开启全新对话。"),
			card.Note("旧会话已留档，可用 `/resume` 恢复。模型 / 推理强度沿用上轮选择。"),
		}, card.CardOpts{Summary: "已清空"}))
	}()
}

// ── /context ──

// handleContextCommand 群内 /context：展示当前活跃会话的上下文 token 用量。
func (o *Orchestrator) handleContextCommand(ctx context.Context, msg NormalizedMessage, proj *project.Project) {
	els := []card.CardElement{card.Md("📊 **上下文用量**")}
	entryIface, ok := o.sessions.Load(msg.ChatID)
	if ok {
		entry, _ := entryIface.(*SessionEntry)
		if entry != nil && entry.LastState != nil && entry.LastState.Usage != nil {
			u := entry.LastState.Usage
			if u.ContextWindow != nil && *u.ContextWindow > 0 {
				pct := float64(u.UsedTokens) / float64(*u.ContextWindow) * 100
				els = append(els, card.Md(fmt.Sprintf("已用 **%d / %d** tokens（约 %.0f%%）。", u.UsedTokens, *u.ContextWindow, pct)))
			} else {
				els = append(els, card.Md(fmt.Sprintf("已用 **%d** tokens（后端未上报窗口上限）。", u.UsedTokens)))
			}
			els = append(els, card.Note("接近上限时点 /compact 压缩历史。"))
		} else {
			els = append(els, card.Md("尚无用量统计（等一轮对话产生 token 后可见）。"))
		}
		if entry != nil && entry.Thread != nil {
			els = append(els, card.Note("活跃会话："+entry.Thread.SessionID()))
		}
	} else {
		els = append(els, card.Md("当前没有活跃会话。先 @我 聊一句，再来看用量。"))
	}
	if proj.DefaultModel != "" {
		effLabel := card.EffortLabel[agent.ReasoningEffort(proj.DefaultEffort)]
		suffix := ""
		if effLabel != "" {
			suffix = " · 强度 " + effLabel
		}
		els = append(els, card.Note("默认模型："+proj.DefaultModel+suffix))
	}
	o.sendCard(ctx, msg.ChatID, card.Card(els, card.CardOpts{Summary: "上下文用量"}))
}
