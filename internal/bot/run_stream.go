package bot

// run_stream.go —— 流式运行卡 + 终止/续轮控制（对齐 TS M-6 零打字驱动 + RunCardStream）。
// 把 card.RunCardStream 引擎接入 HandleTurn/HandleGoal：
//   · 创建 CardKit 实体运行卡 → 随 agent 事件流实时 patch（含 ⏹ 终止 / 🎯 结束目标按钮）；
//   · 用「运行卡 message_id」作为 CardKey 注册到 activeRuns；卡片按钮 / 表情回复都能据此定位运行；
//   · run.stop / goal.end 卡片 action → cancel 对应 agent 运行的 ctx（恢复「停止」能力）；
//   · 运行中 run 卡收到 OK/DONE 表情 → 终止；终态卡收到 👍 → 续轮（steer "继续"）。
// 没有 CardKitClient（测试 / 未注入飞书）时自动回退到「跑完发单张终态卡」的旧行为。

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

// ── 反应意图（对齐 TS STOP_EMOJIS / CONTINUE_EMOJIS）─────────────────
// 运行中 run/排队卡只认 STOP 类表情（👌/✅）；终态卡只认 👍 续轮。
// 其余 emoji 一律忽略——群友日常表情不应有副作用。
var (
	STOP_EMOJIS     = map[string]bool{"OK": true, "DONE": true}
	CONTINUE_EMOJIS = map[string]bool{"THUMBSUP": true}
)

type reactionIntent int

const (
	intentNone reactionIntent = iota
	intentStop
	intentContinue
)

// classifyReaction 纯决策：运行中→STOP；终态→CONTINUE；其余→none。
func classifyReaction(emojiType string, running bool) reactionIntent {
	if running {
		if STOP_EMOJIS[emojiType] {
			return intentStop
		}
		return intentNone
	}
	if CONTINUE_EMOJIS[emojiType] {
		return intentContinue
	}
	return intentNone
}

// runHandle 一次运行中任务的句柄（以运行卡 message_id 为 key 注册）。
type runHandle struct {
	cancel      context.CancelFunc // 取消 agent 运行的 ctx（≠ 卡片 patch 用的 ctx）
	chatID      string
	threadKey   string
	thread      agent.AgentThread
	stream      *card.RunCardStream
	goal        bool
	ownerOpenID string

	stopped    atomic.Bool
	goalEnding atomic.Bool
	endedAt    int64 // 终态后写入，供 👍 续轮 TTL 判定
	startedAt   int64 // 本轮发起时间（ms），供任务结束提醒计算耗时
}

// stop 终止对应 agent 运行（幂等）。仅 cancel 运行 ctx，不影响卡片 patch ctx。
func (h *runHandle) stop() {
	if h.stopped.Swap(true) {
		return
	}
	if h.cancel != nil {
		h.cancel()
	}
}

// cardKit 取飞书 Channel 暴露的 CardKitClient（用于流式运行卡）；未注入返回 nil。
func (o *Orchestrator) cardKit() card.CardKitClient {
	if p, ok := o.Channel.(interface {
		CardKitClient() card.CardKitClient
	}); ok {
		return p.CardKitClient()
	}
	return nil
}

// pacerFor 取某 chat 的 per-chat 限流（所有 RunCardStream 共享该 chat 一个 pacer）。
func (o *Orchestrator) pacerFor(chatID string) *card.ChatPacer {
	if v, ok := o.pacers.Load(chatID); ok {
		return v.(*card.ChatPacer)
	}
	p := &card.ChatPacer{}
	o.pacers.Store(chatID, p)
	return p
}

// streamRunCardCreate 创建流式运行卡并注册句柄。replyTo=触发消息 id（topic 内则 replyInThread）。
// runCancel 一并存入句柄，使 run.stop/goal.end 能取消 agent 运行。
func (o *Orchestrator) streamRunCardCreate(ctx context.Context, input TurnInput, isGoal bool, runCancel context.CancelFunc) (*card.RunCardStream, *runHandle, error) {
	client := o.cardKit()
	if client == nil {
		return nil, nil, fmt.Errorf("cardkit client unavailable")
	}
	rs := card.InitialState()
	initial := card.BuildRunCard(card.RunCardState{
		RS:           rs,
		CardKey:      "",
		Model:        input.ModelCard(),
		Effort:       input.EffortCard(),
		GoalControls: isGoal,
	})
	stream := card.NewRunCardStream(card.RunCardStreamOptions{Client: client})
	stream.SetPacer(o.pacerFor(input.ChatID))
	msgID, err := stream.Create(ctx, input.ChatID, initial, input.MessageID, input.ReplyInThread)
	if err != nil {
		return nil, nil, err
	}
	h := &runHandle{
		cancel:      runCancel,
		chatID:      input.ChatID,
		threadKey:   resolveThreadKey(input),
		goal:        isGoal,
		ownerOpenID: input.SenderID,
		stream:      stream,
		startedAt:   time.Now().UnixMilli(),
	}
	o.activeRuns.Store(msgID, h)
	// 立即补一张带终止按钮的卡（替换初始无按钮版），让 ⏹ 尽早可点。
	o.pushRunCard(ctx, stream, card.InitialState(), msgID, input, isGoal, false)
	return stream, h, nil
}

// pushRunCard 渲染当前 state 并流式 patch 运行卡（带终止/结束按钮）。
func (o *Orchestrator) pushRunCard(ctx context.Context, stream *card.RunCardStream, state card.RunState, msgID string, input TurnInput, isGoal, goalEnding bool) {
	rc := card.RunCardState{
		RS:           state,
		CardKey:      msgID,
		Model:        input.ModelCard(),
		Effort:       input.EffortCard(),
		GoalControls: isGoal,
		GoalEnding:   goalEnding,
	}
	stream.StreamCoalesced(ctx, card.BuildRunCard(rc), card.AnswerEID)
}

// finalizeRunCard 收尾：标记中断（若被 stop）、Drain 在途 patch、发终态卡、移入 pastRuns 供 👍 续轮。
func (o *Orchestrator) finalizeRunCard(ctx context.Context, stream *card.RunCardStream, h *runHandle, state card.RunState) {
	if h.stopped.Load() && state.Terminal == card.TermRunning {
		state.Terminal = card.TermInterrupted
	}
	stream.Drain(ctx)
	stream.UpdateCard(ctx, card.BuildRunCard(card.RunCardState{RS: state, CardKey: ""}))
	msgID := stream.MessageID()
	o.activeRuns.Delete(msgID)
	if h.thread != nil {
		h.endedAt = time.Now().UnixMilli()
		o.pastRuns.Store(msgID, h)
	}
}

// handleRunControl 运行卡 ⏹/🎯 按钮（run.stop / goal.end）→ 取消对应 agent 运行。
func (o *Orchestrator) handleRunControl(cca card.CardActionContext) error {
	cardKey := stringOf(cca.Value["m"])
	if cardKey == "" {
		return nil
	}
	v, ok := o.activeRuns.Load(cardKey)
	if !ok {
		core.Info(cca.Ctx, "bot", "run-control-miss", "未找到运行句柄 cardKey="+cardKey)
		return nil
	}
	h := v.(*runHandle)
	h.stop()
	if cca.ActionID == card.RCEndGoal {
		h.goalEnding.Store(true)
	}
	core.Info(cca.Ctx, "bot", "run-control", "收到终止指令 action="+cca.ActionID+" cardKey="+cardKey)
	return nil
}

// HandleReaction 表情回复控制（im.message.reaction.created_v1）。
// 运行中 run 卡 OK/DONE → 终止；终态卡 👍 → 续轮。忽略 bot 自己的 reaction 与无关 emoji。
func (o *Orchestrator) HandleReaction(ctx context.Context, messageID, emojiType, operatorType, operatorOpenID string) error {
	if messageID == "" || emojiType == "" {
		return nil
	}
	if operatorType == "app" {
		return nil // 忽略 bot 自己的 Typing/OneSecond 表情
	}
	// 运行中？
	if v, ok := o.activeRuns.Load(messageID); ok {
		h := v.(*runHandle)
		if classifyReaction(emojiType, true) == intentStop {
			h.stop()
			core.Info(ctx, "bot", "reaction-stop", "reaction 终止运行中任务 messageID="+messageID+" emoji="+emojiType)
		}
		return nil
	}
	// 终态卡 → 👍 续轮
	if classifyReaction(emojiType, false) == intentContinue {
		o.continueFromReaction(ctx, messageID, operatorOpenID)
	}
	return nil
}

// continueFromReaction 终态卡 👍 → 在对应 thread 续一轮（steer "继续"）。best-effort。
func (o *Orchestrator) continueFromReaction(ctx context.Context, messageID, operatorOpenID string) {
	v, ok := o.pastRuns.Load(messageID)
	if !ok {
		return
	}
	h := v.(*runHandle)
	// TTL：超过 30 分钟不再续轮（thread 多半已冷/被驱逐）。
	if h.endedAt != 0 && time.Now().UnixMilli()-h.endedAt > 30*60*1000 {
		o.pastRuns.Delete(messageID)
		return
	}
	if h.thread == nil {
		return
	}
	core.Info(ctx, "bot", "reaction-continue", "👍 续轮 messageID="+messageID+" by="+operatorOpenID)
	if err := h.thread.Steer(ctx, agent.AgentInput{Text: "继续"}, ""); err != nil {
		core.Warn(ctx, "bot", "reaction-continue-fail", err.Error())
	}
}

// evictStalePastRuns 清理过期的 pastRuns 条目（防止无限增长）。可选后台调用。
func (o *Orchestrator) evictStalePastRuns() {
	now := time.Now().UnixMilli()
	o.pastRuns.Range(func(k, v any) bool {
		h := v.(*runHandle)
		if h.endedAt != 0 && now-h.endedAt > 30*60*1000 {
			o.pastRuns.Delete(k)
		}
		return true
	})
}
