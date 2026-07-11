package claude

// thread.go —— claude 后端编排层（港口 TS claude-agent/thread）。
// 实现 agent.AgentBackend（ClaudeBackend）+ agent.AgentThread（ClaudeThread）。
//
// 与 codex 的关键差异：codex 一个持久 app-server 进程 = 一个 thread（turn 走 RPC）；
// claude CLI 一次性子进程 = 一个 turn，靠 --resume <sessionId> 跨进程续会话。
// 故 ClaudeThread 不持有长连接，每次 RunStreamed/RunGoal/Compact 拉起一个新 `claude` 进程，
// 从 system/init 捕获 sessionId 存回 thread，供后续轮 --resume。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

const (
	compactTimeout = 120 * time.Second
)

func nowMs() int64 { return time.Now().UnixMilli() }

// ClaudeThread 一个 claude 会话（sessionId 由首个 turn 的 system/init 捕获）。
type ClaudeThread struct {
	bin                 string
	cwd                 string
	model               string
	effort              agent.ReasoningEffort
	perms               []string // CLI 权限档参数片段
	appendSystemPrompt string
	env                 map[string]string

	mu                 sync.Mutex
	sessionID          string
	currentTurnID      string
	interruptRequested bool
	goalRunning        bool
	running            *ClaudeCli
	closed             bool

	turnSeq     int64
	lastActivity atomic.Int64
}

// NewClaudeThread 构造（sessionId 首轮后才非空）。
func NewClaudeThread(bin, cwd, model string, effort agent.ReasoningEffort, perms []string, appendSystemPrompt string) *ClaudeThread {
	t := &ClaudeThread{
		bin: bin, cwd: cwd, model: model, effort: effort,
		perms: perms, appendSystemPrompt: appendSystemPrompt,
	}
	t.lastActivity.Store(nowMs())
	return t
}

func (t *ClaudeThread) SessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

func (t *ClaudeThread) setSessionID(s string) {
	t.mu.Lock()
	t.sessionID = s
	t.mu.Unlock()
}

func (t *ClaudeThread) IsAlive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed
}

func (t *ClaudeThread) Close(_ context.Context) error {
	t.mu.Lock()
	t.closed = true
	cli := t.running
	t.mu.Unlock()
	if cli != nil {
		cli.Kill(2 * time.Second)
	}
	return nil
}

func (t *ClaudeThread) nextTurn() int64 { return atomic.AddInt64(&t.turnSeq, 1) }

// ClearGoal claude 没有 goal 引擎，故「clear」= 硬停当前 goal 轮（同 Abort）。
func (t *ClaudeThread) ClearGoal(ctx context.Context) error {
	t.mu.Lock()
	if !t.goalRunning {
		t.mu.Unlock()
		return nil
	}
	t.goalRunning = false
	t.interruptRequested = true
	cli := t.running
	t.mu.Unlock()
	if cli != nil {
		cli.Kill(4 * time.Second)
	}
	return nil
}

// Steer 能力声明为 false（capabilities.steer=false），编排层会把引导排成下一轮。
func (t *ClaudeThread) Steer(ctx context.Context, in agent.AgentInput, expectedTurnID string) error {
	return errors.New("claude-agent 后端暂不支持飞行中引导（steer），将自动改为下一轮发送")
}

// Abort ⏹：杀掉在飞的 claude 进程（整组）。interruptRequested 让 turn 收尾为干净 done。
func (t *ClaudeThread) Abort(ctx context.Context, turnID string) error {
	t.mu.Lock()
	t.interruptRequested = true
	cli := t.running
	t.mu.Unlock()
	if cli != nil {
		cli.Kill(4 * time.Second)
	}
	return nil
}

// RunStreamed 启动一轮（拉起 `claude -p`，流式返回事件直到 result/进程退出）。
func (t *ClaudeThread) RunStreamed(ctx context.Context, in agent.AgentInput, turn *agent.TurnOptions) agent.AgentRun {
	turnID := fmt.Sprintf("t%d", t.nextTurn())
	t.mu.Lock()
	t.currentTurnID = turnID
	t.interruptRequested = false
	if turn != nil {
		if turn.Model != "" {
			t.model = turn.Model
		}
		if turn.Effort != "" {
			t.effort = turn.Effort
		}
	}
	t.mu.Unlock()

	events := make(chan agent.AgentEvent, 64)
	go func() {
		defer close(events)
		t.runTurn(ctx, events, turnID, in.Text, in.Images, "", 0)
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

// RunGoal 把目标当「一个自主轮」：发带自主提示的目标 → 流式跑（claude -p 内部跑 agent loop
// 多步）→ 合成 goal_update 状态（active 起、complete/blocked/budgetLimited 收）。
// 与 codex 差异（如实）：codex 是 N 个自动续跑 turn + 原生状态机；claude 是 1 个自主 turn +
// 合成状态（仅 active→complete/blocked/budgetLimited），无 paused/usageLimited、无预算。
func (t *ClaudeThread) RunGoal(ctx context.Context, objective string) agent.AgentRun {
	turnID := fmt.Sprintf("g%d", t.nextTurn())
	startedAt := nowMs()
	t.mu.Lock()
	t.currentTurnID = turnID
	t.interruptRequested = false
	t.goalRunning = true
	t.mu.Unlock()

	events := make(chan agent.AgentEvent, 64)
	go func() {
		defer close(events)
		defer func() {
			t.mu.Lock()
			t.goalRunning = false
			t.mu.Unlock()
		}()
		t.runTurn(ctx, events, turnID, goalPrompt(objective), nil, objective, startedAt)
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

// turnOutcome 单轮尝试的结果，用于决定是否重试。
type turnOutcome int

const (
	outcomeDone      turnOutcome = iota // 见到 result（成功或已处理），终态
	outcomeAbort                        // 用户中断或 ctx 取消，终态，不再重试
	outcomeFailed                       // 进程退出但无 result（疑似网关瞬时故障），可重试
	outcomeSpawnError                   // 拉起 claude 失败，可重试
)

// runTurn 拉起一个 `claude` 进程并流式映射事件。goalObjective 非空表示这是一次 goal 轮。
// 当一轮因网关瞬时故障失败（进程退出但无 result）时，按指数退避重试最多 claudeMaxRetries() 次，
// 使 open.bigmodel.cn 这类上游网关的抖动可自愈，而不是把整轮直接判失败。
func (t *ClaudeThread) runTurn(ctx context.Context, events chan agent.AgentEvent, turnID, prompt string, images []string, goalObjective string, startedAt int64) {
	t.lastActivity.Store(nowMs())
	maxRetries := claudeMaxRetries()
	baseDelay := claudeBaseRetryDelay()

	if goalObjective != "" {
		events <- agent.EvGoalUpdateE("active", goalObjective, 0, 0, nil)
	}
	events <- agent.EvTurnStart(turnID)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 重试前先退避，并发「将重试」的非终态错误（卡片层据此显示「重试中」而非「失败」）。
		if attempt > 0 {
			delay := backoffDelay(baseDelay, attempt-1)
			events <- agent.EvErrorT(
				fmt.Sprintf("Claude 网关暂未响应，%s 后进行第 %d/%d 次重试…",
					delay.Round(time.Millisecond), attempt, maxRetries), true)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}

		outcome, err := t.runTurnOnce(ctx, events, turnID, prompt, images, goalObjective, startedAt)
		lastErr = err
		switch outcome {
		case outcomeDone, outcomeAbort:
			return // 终态，事件已在 runTurnOnce 内发出
		case outcomeFailed, outcomeSpawnError:
			// 进入重试判断
		}
		if attempt >= maxRetries {
			break
		}
	}

	// 重试耗尽：发出终态错误，并明确指向网关（而非 bridge 代码 bug）。
	msg := fmt.Sprintf("Claude 连接网关失败（已重试 %d 次）。请确认 ANTHROPIC_BASE_URL 指向的网关当前可达，或稍后重试。", maxRetries)
	if lastErr != nil {
		msg = fmt.Sprintf("Claude 连接网关失败：%v（已重试 %d 次）", lastErr, maxRetries)
	}
	events <- agent.EvErrorT(msg, false)
	if goalObjective != "" {
		events <- agent.EvGoalUpdateE("blocked", goalObjective, 0, 0, nil)
	}
}

// runTurnOnce 执行一次 claude 子进程尝试。除「成功/中断」外，不在本函数发出终态错误，
// 终态决策（重试或耗尽报错）统一由 runTurn 负责。
func (t *ClaudeThread) runTurnOnce(ctx context.Context, events chan agent.AgentEvent, turnID, prompt string, images []string, goalObjective string, startedAt int64) (turnOutcome, error) {
	sid := t.SessionID()
	hasImages := len(images) > 0
	args := buildArgs(t.perms, t.model, t.appendSystemPrompt, sid, prompt, t.effort, hasImages)
	cli := NewClaudeCli(t.bin, t.cwd, t.env)
	mapper := createTurnMapper(t.cwd)

	t.mu.Lock()
	t.running = cli
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		if t.running == cli {
			t.running = nil
		}
		t.mu.Unlock()
	}()

	if err := cli.Start(ctx, args, nil); err != nil {
		return outcomeSpawnError, err
	}

	// 含图片 → 走 stream-json 输入：把 prompt（含 image block）写进 stdin 后关管道，
	// 让 claude 从 stdin 读到 user message（对齐 TS claude-agent-sdk 的 toImageBlock）。
	if hasImages {
		if payload, err := buildStreamUserMsg(prompt, images); err != nil {
			core.Warn(ctx, "agent", "image-encode", "claude 图片序列化失败，退回纯文本: "+err.Error())
		} else {
			_, _ = cli.WriteStdin(payload)
			_ = cli.CloseStdin()
		}
	}

	interrupt := func() bool {
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.interruptRequested
	}

	gotResult := false
	for !gotResult {
		select {
		case <-ctx.Done():
			cli.Kill(2 * time.Second)
			return outcomeAbort, ctx.Err()
		case raw, ok := <-cli.Messages():
			if !ok {
				// 进程退出：用户中断 → 干净 done；否则视为网关瞬时故障，交回 runTurn 重试。
				if interrupt() {
					events <- agent.EvDoneT(turnID)
					return outcomeAbort, nil
				}
				return outcomeFailed, errors.New("claude 进程退出但未产生 result（疑似网关未响应）")
			}
			t.lastActivity.Store(nowMs())
			msg := parseClaudeMessage(raw)
			if msg.Type == "system" && msg.Subtype == "init" {
				if msg.SessionID != "" {
					t.setSessionID(msg.SessionID)
				}
			}
			for _, ev := range mapper.mapMsg(msg) {
				select {
				case events <- ev:
				case <-ctx.Done():
					cli.Kill(2 * time.Second)
					return outcomeAbort, ctx.Err()
				}
			}
			if msg.Type == "result" {
				t.emitTurnEnd(events, turnID, goalObjective, msg, startedAt, interrupt())
				gotResult = true
			}
		}
	}
	return outcomeDone, nil
}

// backoffDelay 指数退避：base * 2^step（step 从 0 起）。
func backoffDelay(base time.Duration, step int) time.Duration {
	d := base
	for i := 0; i < step; i++ {
		d *= 2
	}
	return d
}

// claudeMaxRetries 网关瞬断的重试次数（默认 3，FCB_CLAUDE_MAX_RETRIES 可覆盖，0–10）。
func claudeMaxRetries() int {
	if v := os.Getenv("FCB_CLAUDE_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 10 {
			return n
		}
	}
	return 3
}

// claudeBaseRetryDelay 重试退避基准（默认 2s，FCB_CLAUDE_RETRY_BASE_DELAY 可覆盖，1ms–1m）。
func claudeBaseRetryDelay() time.Duration {
	if v := os.Getenv("FCB_CLAUDE_RETRY_BASE_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Millisecond && d <= time.Minute {
			return d
		}
	}
	return 2 * time.Second
}

func (t *ClaudeThread) emitTurnEnd(events chan agent.AgentEvent, turnID, goalObjective string, msg claudeMessage, startedAt int64, interrupted bool) {
	if goalObjective != "" {
		status := "complete"
		if !interrupted && msg.Subtype != "success" {
			status = goalStatusFromResult(msg.Subtype)
		}
		timeUsed := 0
		if startedAt > 0 {
			timeUsed = int((nowMs() - startedAt) / 1000)
		}
		events <- agent.EvGoalUpdateE(status, goalObjective, 0, timeUsed, nil)
		events <- agent.EvDoneT(turnID)
		return
	}
	if interrupted || msg.Subtype == "success" {
		events <- agent.EvDoneT(turnID)
	} else {
		events <- agent.EvErrorT(resultErrorText(msg), false)
	}
}

// Compact 手动压缩：拉起 `claude --resume <session> -p "/compact"`，drain 到 result；
// 见到 system compact_boundary 即视为已压缩。超时才报错（不挂起「压缩中」卡）。
func (t *ClaudeThread) Compact(ctx context.Context) (agent.CompactResult, error) {
	cctx, cancel := context.WithTimeout(ctx, compactTimeout)
	defer cancel()
	sid := t.SessionID()
	if sid == "" {
		return agent.CompactResult{}, errors.New("Claude 会话尚未启动，无法压缩")
	}
	cli := NewClaudeCli(t.bin, t.cwd, t.env)
	mapper := createTurnMapper(t.cwd)
	result := agent.CompactResult{}
	done := make(chan struct{})
	args := buildArgs(t.perms, t.model, t.appendSystemPrompt, sid, "/compact", t.effort, false)
	if err := cli.Start(cctx, args, func() { close(done) }); err != nil {
		return result, err
	}
	go func() {
		for raw := range cli.Messages() {
			msg := parseClaudeMessage(raw)
			_ = mapper.mapMsg(msg) // 压缩不向调用方吐事件，仅探测边界
			if msg.Type == "system" && msg.Subtype == "compact_boundary" {
				result.Compacted = true
			}
			if msg.Type == "result" {
				return
			}
		}
	}()
	select {
	case <-done:
		return result, nil
	case <-cctx.Done():
		cli.Kill(time.Second)
		return result, errors.New("压缩超时（Claude 未在限定时间内完成）")
	}
}

// buildArgs 组装 claude CLI 参数（不含 bin）。
// hasImages=true 时改用 --input-format stream-json，并把 prompt 改由 stdin 喂入
// （argv 模式无法携带 image block）；调用方负责在 Start 后向 stdin 写 user message。
func buildArgs(perms []string, model, appendSystemPrompt, sessionID, prompt string, effort agent.ReasoningEffort, hasImages bool) []string {
	args := []string{"--print", "--output-format", "stream-json", "--verbose"}
	// 让桥启动的 claude 会话像 Claude Code 一样读项目/user 的 CLAUDE.md 与技能
	//（SDK 默认 [] 不加载任何设置，会导致项目 CLAUDE.md、用户 lark-* 技能被无视）。
	// 程序化权限档（permission.ts 的 bypassPermissions + sandbox）仍优先，加载的
	// settings.json 不能削弱沙箱（对齐 TS settingSources: ['user','project']）。
	args = append(args, "--setting-sources", "user,project")
	args = append(args, perms...)
	if model != "" {
		args = append(args, "--model", model)
	}
	// 推理强度透传：claude CLI 的 --effort 合法值为 low/medium/high/xhigh/max；
	// 上游 ultra 在 claude 上无对应，映射为 max；none/minimal 不传（保留 claude 默认）。
	if e := claudeEffort(effort); e != "" {
		args = append(args, "--effort", e)
	}
	if appendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", appendSystemPrompt)
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	if hasImages {
		// stream-json 输入：prompt + image block 经 stdin 传入（见 buildStreamUserMsg）。
		args = append(args, "--input-format", "stream-json")
	} else {
		args = append(args, "-p", prompt)
	}
	return args
}

// claudeEffort 把统一推理强度映射为 claude CLI `--effort` 接受的值；
// 返回 "" 表示不传（让 claude 用默认强度）。
func claudeEffort(e agent.ReasoningEffort) string {
	switch e {
	case agent.EffortLow, agent.EffortMedium, agent.EffortHigh, agent.EffortXhigh, agent.EffortMax:
		return string(e) // claude CLI 直接接受这些值
	case agent.EffortUltra:
		return string(agent.EffortMax) // claude 无 ultra → max
	default:
		return "" // none / minimal / 空 → 不传，保留 claude 默认
	}
}

// goalPrompt 把目标框成「自主多轮跑到完成」的提示（对齐 TS goalPrompt）。
func goalPrompt(objective string) string {
	return strings.Join([]string{
		"【自主目标】请连续、自主地完成下面的目标：按需使用工具，一步步做到完成为止，",
		"中途不要停下来等我确认；完成后用一段话总结做了什么。",
		"",
		"目标：" + objective,
	}, "\n")
}

// goalStatusFromResult 从非成功 result 合成 codex 风格终态。
func goalStatusFromResult(subtype string) string {
	if subtype == "error_max_budget_usd" {
		return "budgetLimited"
	}
	return "blocked" // error_max_turns / error_during_execution / 其它 → blocked
}

// ── 入站图片（stream-json image block）──────────────────────────────
// 镜像 TS claude-agent/thread.ts 的 toImageBlock / sniffImageType：
// base64 编码进 image block，media_type 按【魔数】嗅探（不是扩展名），
// 因为飞书下发的扩展名可能是错的（如 JPEG 落到 .png）。API 只接受这四种。
const maxImageBytes = 20 * 1024 * 1024

// sniffImageType 按文件头魔数判定图片 media_type（仅这四种 base64 image block 接受）。
// 返回 "" 表示不支持（heic/bmp/tiff/未知头），调用方应跳过而非报错。
func sniffImageType(b []byte) string {
	if len(b) >= 8 && b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4e && b[3] == 0x47 {
		return "image/png" // ‰PNG
	}
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return "image/jpeg" // JFIF/EXIF
	}
	if len(b) >= 6 && b[0] == 0x47 && b[1] == 0x49 && b[2] == 0x46 && b[3] == 0x38 {
		return "image/gif" // GIF8(7|9)a
	}
	if len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

// claudeImageSource / claudeContentBlock / claudeUserMsg 构造 stream-json 输入用的 user message。
type claudeImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}
type claudeContentBlock struct {
	Type   string             `json:"type"`
	Text   string             `json:"text,omitempty"`
	Source *claudeImageSource `json:"source,omitempty"`
}
type claudeUserMsg struct {
	Type string `json:"type"` // "user"
	Message struct {
		Role    string             `json:"role"` // "user"
		Content []claudeContentBlock `json:"content"`
	} `json:"message"`
}

// buildStreamUserMsg 把 prompt + 本地图片路径编成一行 stream-json user message（含 \n）。
// 对齐 TS toUserMessage：文本块 + 每个可读图片一个 base64 image block；
// 图片读取/嗅探失败则跳过（best-effort，绝不因一张坏图毁掉整轮）。
// 若图片全失败但 prompt 非空，退回纯文本（content 非空），claude 仍能看到文字。
func buildStreamUserMsg(prompt string, images []string) ([]byte, error) {
	msg := claudeUserMsg{Type: "user"}
	msg.Message.Role = "user"
	if prompt != "" {
		msg.Message.Content = append(msg.Message.Content, claudeContentBlock{Type: "text", Text: prompt})
	}
	for _, p := range images {
		b, err := os.ReadFile(p)
		if err != nil {
			core.Warn(context.Background(), "agent", "image-read-failed", p+": "+err.Error())
			continue
		}
		if len(b) == 0 || len(b) > maxImageBytes {
			core.Warn(context.Background(), "agent", "image-skip-size", p)
			continue
		}
		mt := sniffImageType(b)
		if mt == "" {
			core.Warn(context.Background(), "agent", "image-skip-unsupported", p)
			continue
		}
		msg.Message.Content = append(msg.Message.Content, claudeContentBlock{
			Type:   "image",
			Source: &claudeImageSource{Type: "base64", MediaType: mt, Data: base64.StdEncoding.EncodeToString(b)},
		})
	}
	if len(msg.Message.Content) == 0 {
		// 极端情况：prompt 为空且图片全失败 → 退回纯文本（可能为空，claude 自行处理）。
		msg.Message.Content = append(msg.Message.Content, claudeContentBlock{Type: "text", Text: prompt})
	}
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	return out, nil
}
