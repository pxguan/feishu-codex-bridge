package codex

// thread.go —— codex 后端编排层（对齐 TS codex-appserver/backend）。
// 实现 agent.AgentBackend（CodexAppServerBackend）+ agent.AgentThread（CodexThread）。
// 把 AppServerClient + event-map + pool + sandboxParams + mapTurn/models + bridge-instructions 组装成完整后端。

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

const (
	readHistoryTimeout = 20 * time.Second
	compactTimeout     = 120 * time.Second
	goalIdleNotUsed    = 0 // 占位（goal idle 由 orchestrator 的 watchdog 管）
)

func nowMs() int64 { return time.Now().UnixMilli() }

// toUserInput 把 AgentInput 转 codex turn/start|steer 的 input 数组。
func toUserInput(in agent.AgentInput) []map[string]any {
	out := []map[string]any{}
	if in.Text != "" {
		out = append(out, map[string]any{"type": "text", "text": in.Text, "text_elements": []any{}})
	}
	for _, p := range in.Images {
		out = append(out, map[string]any{"type": "localImage", "path": p})
	}
	return out
}

// ── CodexThread（AgentThread 实现）─────────────────────────────────

// CodexThread 一个 codex 会话（一个 AppServerClient = 一个 thread/session）。
type CodexThread struct {
	client    *AppServerClient
	sessionID string

	mu            sync.Mutex
	model         string
	effort        agent.ReasoningEffort
	currentTurnID string

	lastActivity atomic.Int64
}

// NewCodexThread 构造（sessionID 来自 thread/start|resume 响应）。
func NewCodexThread(client *AppServerClient, sessionID, model string, effort agent.ReasoningEffort) *CodexThread {
	t := &CodexThread{client: client, sessionID: sessionID, model: model, effort: effort}
	t.lastActivity.Store(nowMs())
	return t
}

func (t *CodexThread) SessionID() string { return t.sessionID }
func (t *CodexThread) IsAlive() bool     { return !t.client.Exited() }
func (t *CodexThread) Close(_ context.Context) error {
	return t.client.Close(4 * time.Second)
}

func (t *CodexThread) ClearGoal(ctx context.Context) error {
	_, err := t.client.Request(ctx, "thread/goal/clear", map[string]any{"threadId": t.sessionID})
	return err
}

func (t *CodexThread) Steer(ctx context.Context, in agent.AgentInput, expectedTurnID string) error {
	_, err := t.client.Request(ctx, "turn/steer", map[string]any{
		"threadId":       t.sessionID,
		"expectedTurnId": expectedTurnID,
		"input":          toUserInput(in),
	})
	return err
}

func (t *CodexThread) Abort(ctx context.Context, turnID string) error {
	_, err := t.client.Request(ctx, "turn/interrupt", map[string]any{
		"threadId": t.sessionID, "turnId": turnID,
	})
	return err
}

// RunStreamed 启动一轮（turn/start），流式返回事件直到 turn/completed。
func (t *CodexThread) RunStreamed(ctx context.Context, in agent.AgentInput, turn *agent.TurnOptions) agent.AgentRun {
	events := make(chan agent.AgentEvent, 64)
	t.mu.Lock()
	t.currentTurnID = ""
	if turn != nil {
		if turn.Model != "" {
			t.model = turn.Model
		}
		if turn.Effort != "" {
			t.effort = turn.Effort
		}
	}
	params := map[string]any{"threadId": t.sessionID, "input": toUserInput(in)}
	if t.model != "" {
		params["model"] = t.model
	}
	if t.effort != "" {
		params["effort"] = t.effort
	}
	t.mu.Unlock()
	t.lastActivity.Store(nowMs())

	go func() {
		defer close(events)
		// turn/start 在整个 turn 在飞（事件走通知），不能 await；但若它 reject，
		// codex 不会发映射到 done/error 的通知——race 它的拒绝，浮出真因。
		startErrCh := make(chan error, 1)
		go func() {
			if _, err := t.client.Request(ctx, "turn/start", params); err != nil {
				startErrCh <- err
			}
		}()
		stream := t.client.Stream()
		for {
			select {
			case err := <-startErrCh:
				select {
				case events <- agent.EvErrorT(err.Error(), false):
				case <-ctx.Done():
				}
				return
			case n, ok := <-stream:
				if !ok {
					return
				}
				t.lastActivity.Store(nowMs())
				ev, has := MapNotification(n, &MapContext{})
				if !has {
					continue
				}
				if ev.Type == agent.EvTurnStarted {
					t.mu.Lock()
					t.currentTurnID = ev.TurnID
					t.mu.Unlock()
				}
				select {
				case events <- ev:
				case <-ctx.Done():
					return
				}
				if ev.Type == agent.EvDone {
					return
				}
				if ev.Type == agent.EvError && !ev.WillRetry {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return agent.AgentRun{
		Events: events,
		TurnID: func() string {
			t.mu.Lock()
			defer t.mu.Unlock()
			return t.currentTurnID
		},
		LastActivity: func() int64 { return t.lastActivity.Load() },
	}
}

// RunGoal 设置目标并流式跑（thread/goal/clear + thread/goal/set；codex 自动续轮）。
func (t *CodexThread) RunGoal(ctx context.Context, objective string) agent.AgentRun {
	events := make(chan agent.AgentEvent, 64)
	t.mu.Lock()
	t.currentTurnID = ""
	t.mu.Unlock()
	t.lastActivity.Store(nowMs())

	go func() {
		defer close(events)
		// 先清掉残留 goal（codex resume 会重播旧 goal；identical objective 的 set 是 no-op）。
		_, _ = t.client.Request(ctx, "thread/goal/clear", map[string]any{"threadId": t.sessionID})

		setErrCh := make(chan error, 1)
		go func() {
			if _, err := t.client.Request(ctx, "thread/goal/set", map[string]any{
				"threadId": t.sessionID, "objective": objective,
			}); err != nil {
				setErrCh <- err
			}
		}()

		stream := t.client.Stream()
		armed := false      // 本 goal 的真实 turn 跑过
		turnActive := false // 当前有 turn 在飞
		goalDone := false   // 已见终态 goal，drain 完当前 turn 再停
		for {
			select {
			case err := <-setErrCh:
				select {
				case events <- agent.EvErrorT(err.Error(), false):
				case <-ctx.Done():
				}
				return
			case n, ok := <-stream:
				if !ok {
					return
				}
				t.lastActivity.Store(nowMs())
				ev, has := MapNotification(n, &MapContext{})
				if !has {
					continue
				}
				switch ev.Type {
				case agent.EvTurnStarted:
					t.mu.Lock()
					t.currentTurnID = ev.TurnID
					t.mu.Unlock()
					armed = true
					turnActive = true
					select {
					case events <- ev:
					case <-ctx.Done():
						return
					}
					continue
				case agent.EvDone:
					turnActive = false
					select {
					case events <- ev:
					case <-ctx.Done():
						return
					}
					if goalDone {
						return
					}
					continue
				case agent.EvGoalUpdate:
					if ev.Objective != objective {
						continue // 旧 goal 快照，忽略
					}
					if ev.Status == "active" || ev.Status == "paused" {
						armed = true
					}
					select {
					case events <- ev:
					case <-ctx.Done():
						return
					}
					if armed && agent.IsGoalTerminal(ev.Status) {
						if turnActive {
							goalDone = true // drain 当前 turn 的最终答案
						} else {
							return
						}
					}
					continue
				}
				select {
				case events <- ev:
				case <-ctx.Done():
					return
				}
				if ev.Type == agent.EvError && !ev.WillRetry {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return agent.AgentRun{
		Events: events,
		TurnID: func() string {
			t.mu.Lock()
			defer t.mu.Unlock()
			return t.currentTurnID
		},
		LastActivity: func() int64 { return t.lastActivity.Load() },
	}
}

// Compact 手动压缩（thread/compact/start → drain 到 turn/completed）。
func (t *CodexThread) Compact(ctx context.Context) (agent.CompactResult, error) {
	cctx, cancel := context.WithTimeout(ctx, compactTimeout)
	defer cancel()
	startErrCh := make(chan error, 1)
	go func() {
		if _, err := t.client.Request(cctx, "thread/compact/start", map[string]any{"threadId": t.sessionID}); err != nil {
			startErrCh <- err
		}
	}()
	stream := t.client.Stream()
	result := agent.CompactResult{}
	for {
		select {
		case err := <-startErrCh:
			return result, err
		case n, ok := <-stream:
			if !ok {
				return result, nil
			}
			ev, has := MapNotification(n, &MapContext{})
			if !has {
				continue
			}
			switch ev.Type {
			case agent.EvContextUsage:
				w := ptrInt(ev.ContextWindow)
				result.Usage = &agent.ContextUsage{UsedTokens: ev.UsedTokens, ContextWindow: w}
			case agent.EvContextCompacted:
				result.Compacted = true
			case agent.EvError:
				if !ev.WillRetry {
					return result, errors.New(ev.Message)
				}
			case agent.EvDone:
				return result, nil
			}
		case <-cctx.Done():
			return result, cctx.Err()
		}
	}
}

func ptrInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// ── CodexAppServerBackend（AgentBackend 实现）──────────────────────

// CodexAppServerBackend codex app-server 后端。
type CodexAppServerBackend struct {
	modelMu    sync.Mutex
	modelCache []agent.ModelInfo
}

func (b *CodexAppServerBackend) ID() string          { return agent.DEFAULT_BACKEND_ID }
func (b *CodexAppServerBackend) DisplayName() string { return "Codex (app-server)" }
func (b *CodexAppServerBackend) Capabilities() agent.AgentCapabilities {
	return agent.AllCapabilities()
}
func (b *CodexAppServerBackend) SupportedModes() []agent.PermissionMode {
	return agent.AllPermissionModes
}

func (b *CodexAppServerBackend) IsAvailable(ctx context.Context) bool {
	return b.Doctor(ctx, false).Ok
}

// Doctor 探测 codex 运行时（版本/路径）；绝不抛错。
func (b *CodexAppServerBackend) Doctor(_ context.Context, force bool) agent.BackendProbe {
	bin := ResolveCodexBin(force)
	if bin == "" {
		return agent.BackendProbe{Hint: "未找到。设置 CODEX_BIN，或安装 @openai/codex，或装 Codex.app"}
	}
	version := CodexVersion(bin, force)
	if version == "" {
		return agent.BackendProbe{Location: bin, Hint: "codex --version 执行失败（" + bin + "）"}
	}
	return agent.BackendProbe{Ok: true, Version: version, Location: bin}
}

// ListModels model/list（utility 共享进程）+ 静态兜底。
func (b *CodexAppServerBackend) ListModels(ctx context.Context) ([]agent.ModelInfo, error) {
	b.modelMu.Lock()
	if b.modelCache != nil {
		cache := b.modelCache
		b.modelMu.Unlock()
		return cache, nil
	}
	b.modelMu.Unlock()

	if ResolveCodexBin(false) == "" {
		return StaticModels, nil
	}
	res, err := UtilityRequest(ctx, "model/list", map[string]any{"limit": 50}, 0)
	if err != nil {
		core.Warn(ctx, "agent", "model/list", err.Error())
		return StaticModels, nil
	}
	var r struct {
		Data []rawModel `json:"data"`
	}
	_ = json.Unmarshal(res, &r)
	models := make([]agent.ModelInfo, 0, len(r.Data))
	for _, m := range r.Data {
		models = append(models, MapModel(m))
	}
	if len(models) == 0 {
		return StaticModels, nil
	}
	b.modelMu.Lock()
	b.modelCache = models
	b.modelMu.Unlock()
	return models, nil
}

// ListThreads thread/list（utility）→ ThreadSummary（过滤 ephemeral）。
func (b *CodexAppServerBackend) ListThreads(ctx context.Context, cwd string, limit int) ([]agent.ThreadSummary, error) {
	if limit <= 0 {
		limit = 15
	}
	if ResolveCodexBin(false) == "" {
		return nil, nil
	}
	res, err := UtilityRequest(ctx, "thread/list", map[string]any{
		"cwd": cwd, "limit": limit, "sortKey": "created_at", "sortDirection": "desc",
	}, 0)
	if err != nil {
		core.Warn(ctx, "agent", "thread/list", err.Error())
		return nil, nil
	}
	var r struct {
		Data []rawThread `json:"data"`
	}
	_ = json.Unmarshal(res, &r)
	out := make([]agent.ThreadSummary, 0, len(r.Data))
	for _, t := range r.Data {
		if t.Ephemeral {
			continue
		}
		updated := t.UpdatedAt
		if updated == 0 {
			updated = t.CreatedAt
		}
		out = append(out, agent.ThreadSummary{
			SessionID: t.ID, Preview: t.Preview, CreatedAt: t.CreatedAt, UpdatedAt: updated, Name: t.Name,
		})
	}
	return out, nil
}

// ReadHistory thread/read（utility）→ ThreadHistory（mapTurn + 截断最近 maxTurns）。
func (b *CodexAppServerBackend) ReadHistory(ctx context.Context, _, sessionID string, maxTurns int) (agent.ThreadHistory, error) {
	if maxTurns <= 0 {
		maxTurns = 10
	}
	empty := agent.ThreadHistory{}
	if ResolveCodexBin(false) == "" {
		return empty, nil
	}
	res, err := UtilityRequest(ctx, "thread/read", map[string]any{
		"threadId": sessionID, "includeTurns": true,
	}, readHistoryTimeout)
	if err != nil {
		core.Warn(ctx, "agent", "thread/read", sessionID+": "+err.Error())
		return empty, nil
	}
	var r struct {
		Thread struct {
			Turns     []Turn `json:"turns"`
			Name      string `json:"name"`
			Preview   string `json:"preview"`
			CreatedAt int64  `json:"createdAt"`
			UpdatedAt int64  `json:"updatedAt"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(res, &r)
	all := make([]agent.HistoryTurn, 0, len(r.Thread.Turns))
	for _, turn := range r.Thread.Turns {
		ht := MapTurn(turn)
		if ht.UserText == "" && ht.AssistantText == "" && len(ht.Tools) == 0 {
			continue
		}
		all = append(all, ht)
	}
	total := len(all)
	turns := all
	if total > maxTurns {
		turns = all[total-maxTurns:]
	}
	return agent.ThreadHistory{
		Turns: turns, TotalTurns: total,
		Name: r.Thread.Name, Preview: r.Thread.Preview,
		CreatedAt: r.Thread.CreatedAt, UpdatedAt: r.Thread.UpdatedAt,
	}, nil
}

// StartThread 启动新会话（SandboxParams fail-closed 在 spawn 前 → 无孤儿进程）。
func (b *CodexAppServerBackend) StartThread(ctx context.Context, opts agent.StartThreadOptions) (agent.AgentThread, error) {
	sandbox, err := SandboxParams(opts.Mode, opts.Network, "")
	if err != nil {
		return nil, err
	}
	params := WithAutoCompact(sandbox, opts.AutoCompact)
	client, err := b.spawn(ctx, opts.Cwd)
	if err != nil {
		return nil, err
	}
	threadParams := map[string]any{
		"cwd": opts.Cwd, "approvalPolicy": APPROVAL_POLICY,
		"developerInstructions": BridgeDeveloperInstructions,
	}
	for k, v := range params {
		threadParams[k] = v
	}
	if opts.Model != "" {
		threadParams["model"] = opts.Model
	}
	res, err := client.Request(ctx, "thread/start", threadParams)
	if err != nil {
		_ = client.Close(2 * time.Second)
		return nil, err
	}
	var tr struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(res, &tr); err != nil {
		_ = client.Close(2 * time.Second)
		return nil, err
	}
	return NewCodexThread(client, tr.Thread.ID, opts.Model, opts.Effort), nil
}

// ResumeThread resume 会话。
func (b *CodexAppServerBackend) ResumeThread(ctx context.Context, opts agent.ResumeThreadOptions) (agent.AgentThread, error) {
	sandbox, err := SandboxParams(opts.Mode, opts.Network, "")
	if err != nil {
		return nil, err
	}
	params := WithAutoCompact(sandbox, opts.AutoCompact)
	client, err := b.spawn(ctx, opts.Cwd)
	if err != nil {
		return nil, err
	}
	threadParams := map[string]any{
		"threadId": opts.SessionID, "cwd": opts.Cwd, "approvalPolicy": APPROVAL_POLICY,
		"developerInstructions": BridgeDeveloperInstructions,
	}
	for k, v := range params {
		threadParams[k] = v
	}
	if opts.Model != "" {
		threadParams["model"] = opts.Model
	}
	res, err := client.Request(ctx, "thread/resume", threadParams)
	if err != nil {
		_ = client.Close(2 * time.Second)
		return nil, err
	}
	var tr struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(res, &tr); err != nil {
		_ = client.Close(2 * time.Second)
		return nil, err
	}
	return NewCodexThread(client, tr.Thread.ID, opts.Model, opts.Effort), nil
}

// spawn 取预热进程或冷启动；取走/扑空都异步补位。
func (b *CodexAppServerBackend) spawn(ctx context.Context, cwd string) (*AppServerClient, error) {
	bin := ResolveCodexBin(false)
	if bin == "" {
		return nil, errors.New("codex CLI not found (set CODEX_BIN or install @openai/codex)")
	}
	if warmed := TakeWarmClient(bin); warmed != nil {
		RefillWarmPool()
		return warmed, nil
	}
	RefillWarmPool()
	client := NewAppServerClient(AppServerClientOptions{Bin: bin, Cwd: cwd})
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// rawThread thread/list 原始条目。
type rawThread struct {
	ID        string `json:"id"`
	Preview   string `json:"preview"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Name      string `json:"name"`
	Ephemeral bool   `json:"ephemeral"`
}
