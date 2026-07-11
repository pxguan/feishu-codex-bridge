package agent

// types.go —— 后端中立的 agent 接口（对齐 TS agent/types）。
//
// 两种后端共享此契约：Codex app-server（Phase 1，./codex）与 Claude CLI（二期，./claude）。
// AgentThread / AgentBackend 是 interface，各后端各自实现；orchestrator 只依赖接口。

import "context"

// DEFAULT_BACKEND_ID 未显式选后端时的缺省（codex app-server，行为与历史一致）。
const DEFAULT_BACKEND_ID = "codex-appserver"

// ReasoningEffort 推理强度（对齐 TS REASONING_EFFORTS：
// none|minimal|low|medium|high|xhigh|max|ultra）。
type ReasoningEffort string

const (
	EffortNone    ReasoningEffort = "none"
	EffortMinimal ReasoningEffort = "minimal"
	EffortLow     ReasoningEffort = "low"
	EffortMedium  ReasoningEffort = "medium"
	EffortHigh    ReasoningEffort = "high"
	EffortXhigh   ReasoningEffort = "xhigh"
	EffortMax     ReasoningEffort = "max"
	EffortUltra   ReasoningEffort = "ultra"
)

// AllReasoningEfforts 全部推理强度（与 TS REASONING_EFFORTS 一致）。
var AllReasoningEfforts = []ReasoningEffort{
	EffortNone, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXhigh, EffortMax, EffortUltra,
}

// PermissionMode 项目沙箱档。
type PermissionMode string

const (
	PermissionQA    PermissionMode = "qa"    // 只读，限 cwd
	PermissionWrite PermissionMode = "write" // 读写，限 cwd
	PermissionFull  PermissionMode = "full"  // 全机 + 联网
)

// AllPermissionModes 全档。
var AllPermissionModes = []PermissionMode{PermissionQA, PermissionWrite, PermissionFull}

// AgentInput 一轮的用户输入。
type AgentInput struct {
	Text   string
	Images []string // codex 直接读本地图片绝对路径；claude 后端 base64 编码进 image block
}

// ToolKind 工具粗分类（对齐 TS agent/types.ToolKind），由每个后端 event-map 设定，
// 供卡片按类别渲染而无需重新解析 title：
//   - command：shell 命令，title 即完整命令行（渲染为 ```bash 块，长/多行命令完整可读不截断）
//   - file：文件读/写/编辑，title 是路径标签
//   - search：grep/glob/web
//   - tool：其它（mcp / 通用）
// 空字符串 ⇒ 视为 tool。
type ToolKind string

const (
	ToolKindCommand ToolKind = "command"
	ToolKindFile    ToolKind = "file"
	ToolKindSearch  ToolKind = "search"
	ToolKindTool    ToolKind = "tool"
)

// ModelInfo 后端支持的模型。
type ModelInfo struct {
	ID               string
	DisplayName      string
	Description      string
	SupportedEfforts []ReasoningEffort
	DefaultEffort    ReasoningEffort
	IsDefault        bool
	Hidden           bool
}

// ThreadSummary 历史会话条目（resume picker）。
type ThreadSummary struct {
	SessionID string
	Preview   string
	CreatedAt int64 // unix 秒
	UpdatedAt int64
	Name      string
}

// HistoryTool 历史会话里的一次工具调用。
type HistoryTool struct {
	Title    string
	Output   string
	ExitCode *int // nil=未知/不适用
	Failed   bool
}

// HistoryTurn 历史会话里的一轮（user→assistant）。
type HistoryTurn struct {
	UserText      string
	AssistantText string
	Reasoning     string
	Tools         []HistoryTool
	StartedAt     int64 // unix 秒，0=未知
}

// ThreadHistory resume 历史卡数据。
type ThreadHistory struct {
	Turns      []HistoryTurn
	TotalTurns int
	Name       string
	Preview    string
	CreatedAt  int64
	UpdatedAt  int64
}

// ── AgentEvent（归一化事件）──────────────────────────────────────

// 事件类型常量。
const (
	EvSystem           = "system"
	EvTurnStarted      = "turn_started"
	EvTextDelta        = "text_delta"
	EvText             = "text"
	EvThinkingDelta    = "thinking_delta"
	EvThinking         = "thinking"
	EvToolUse          = "tool_use"
	EvToolResult       = "tool_result"
	EvUsage            = "usage"
	EvContextUsage     = "context_usage"
	EvContextCompacted = "context_compacted"
	EvDone             = "done"
	EvGoalUpdate       = "goal_update"
	EvApprovalRequest  = "approval_request"
	EvError            = "error"
)

// AgentEvent 归一化流式事件（单 struct + Type 标签 + 分支字段）。
// 用 Ev* 构造函数族构造，保证字段一致性。
type AgentEvent struct {
	Type            string
	ThreadID        string
	TurnID          string
	ItemID          string
	Delta           string
	Text            string
	Title           string
	Detail          string
	Kind            string // ToolKind：tool_use 的粗分类渲染（command/file/search/tool）
	Output          string
	ExitCode        *int
	InputTokens     int
	OutputTokens    int
	UsedTokens      int
	ContextWindow   *int // nil=未知（codex 未上报窗口）
	Status          string
	Objective       string
	TokensUsed      int
	TimeUsedSeconds int
	TokenBudget     *int
	RequestID       string
	Message         string
	WillRetry       bool
}

// 构造函数族（按 TS 联合分支）。
func EvSys(threadID string) AgentEvent     { return AgentEvent{Type: EvSystem, ThreadID: threadID} }
func EvTurnStart(turnID string) AgentEvent { return AgentEvent{Type: EvTurnStarted, TurnID: turnID} }
func EvTextD(itemID, delta string) AgentEvent {
	return AgentEvent{Type: EvTextDelta, ItemID: itemID, Delta: delta}
}
func EvTextFull(itemID, text string) AgentEvent {
	return AgentEvent{Type: EvText, ItemID: itemID, Text: text}
}
func EvThinkingD(itemID, delta string) AgentEvent {
	return AgentEvent{Type: EvThinkingDelta, ItemID: itemID, Delta: delta}
}
func EvThinkingFull(itemID, text string) AgentEvent {
	return AgentEvent{Type: EvThinking, ItemID: itemID, Text: text}
}
func EvToolU(itemID, title, detail string) AgentEvent {
	return AgentEvent{Type: EvToolUse, ItemID: itemID, Title: title, Detail: detail}
}

// EvToolUK 带 ToolKind 的 tool_use 构造（command/file/search/tool 分类渲染）。
func EvToolUK(itemID, title, detail string, kind ToolKind) AgentEvent {
	return AgentEvent{Type: EvToolUse, ItemID: itemID, Title: title, Detail: detail, Kind: string(kind)}
}
func EvToolR(itemID, output string, exitCode *int) AgentEvent {
	return AgentEvent{Type: EvToolResult, ItemID: itemID, Output: output, ExitCode: exitCode}
}
func EvUsageT(in, out int) AgentEvent {
	return AgentEvent{Type: EvUsage, InputTokens: in, OutputTokens: out}
}
func EvContext(used int, window *int) AgentEvent {
	return AgentEvent{Type: EvContextUsage, UsedTokens: used, ContextWindow: window}
}
func EvCompacted() AgentEvent          { return AgentEvent{Type: EvContextCompacted} }
func EvDoneT(turnID string) AgentEvent { return AgentEvent{Type: EvDone, TurnID: turnID} }
func EvErrorT(msg string, willRetry bool) AgentEvent {
	return AgentEvent{Type: EvError, Message: msg, WillRetry: willRetry}
}

// EvGoalUpdate 构造 goal 状态变更事件。
func EvGoalUpdateE(status, objective string, tokensUsed, timeUsed int, tokenBudget *int) AgentEvent {
	return AgentEvent{Type: EvGoalUpdate, Status: status, Objective: objective,
		TokensUsed: tokensUsed, TimeUsedSeconds: timeUsed, TokenBudget: tokenBudget}
}

// IsGoalTerminal goal 是否进入终态（不再自动续轮）。
// codex 协议只列 active|paused|budgetLimited|complete，但 0.139 运行时还会发
// usageLimited/blocked，故按字符串判定、绝不穷举枚举。
func IsGoalTerminal(status string) bool {
	switch status {
	case "complete", "budgetLimited", "usageLimited", "blocked":
		return true
	}
	return false
}

// IsGoalSuccess goal 是否成功完成。
func IsGoalSuccess(status string) bool { return status == "complete" }

// ── 运行 / 会话 / 后端 ───────────────────────────────────────────

// AgentRun 一轮（或一个 goal）的事件流。
// Events 由后端实现 close；消费方 range chan。
type AgentRun struct {
	Events <-chan AgentEvent
	// TurnID 返回当前轮 id（turn_started 后非空）。
	TurnID func() string
	// LastActivity 返回最近一次后端原始活动的 epoch ms（含 event-map 丢弃的通知），
	// 供 idle watchdog 区分「忙碌但安静」（长 shell）与「真卡死」。
	LastActivity func() int64
}

// ContextUsage token 上下文占用。
type ContextUsage struct {
	UsedTokens    int
	ContextWindow *int // nil=未知
}

// CompactResult 手动 /compact 结果。
type CompactResult struct {
	Compacted bool
	Usage     *ContextUsage
}

// TurnOptions 单轮覆盖（持续生效）。
type TurnOptions struct {
	Model  string
	Effort ReasoningEffort
}

// StartThreadOptions 启动新会话。
type StartThreadOptions struct {
	Cwd         string
	Model       string
	Effort      ReasoningEffort
	Mode        PermissionMode // 空 → full（保留历史 danger-full-access）
	Network     bool           // qa/write 是否联网（full 恒联网）
	AutoCompact *bool          // nil=留默认(on)；false=推到 1e9 关闭
}

// ResumeThreadOptions resume 会话。
type ResumeThreadOptions struct {
	StartThreadOptions
	SessionID string
}

// AgentThread 一个 agent 会话。
type AgentThread interface {
	SessionID() string
	RunStreamed(ctx context.Context, in AgentInput, turn *TurnOptions) AgentRun
	RunGoal(ctx context.Context, objective string) AgentRun
	ClearGoal(ctx context.Context) error
	Steer(ctx context.Context, in AgentInput, expectedTurnID string) error
	Abort(ctx context.Context, turnID string) error
	Compact(ctx context.Context) (CompactResult, error)
	IsAlive() bool
	Close(ctx context.Context) error
}

// AgentCapabilities 后端能力声明（缺省全 true = codex 完整能力）。
type AgentCapabilities struct {
	Goal      bool
	Steer     bool
	Compact   bool
	Resume    bool
	Approvals bool
}

// AllCapabilities 缺省全开。
func AllCapabilities() AgentCapabilities {
	return AgentCapabilities{Goal: true, Steer: true, Compact: true, Resume: true, Approvals: true}
}

// BackendProbe 后端运行时探测结果（doctor/onboarding/DM 体检共用）。
type BackendProbe struct {
	Ok          bool
	Version     string
	Location    string
	Hint        string
	Installable bool
	DepState    string // installed|not-installed|external-missing
}

// AgentBackend 后端工厂（每个后端一个实例）。
type AgentBackend interface {
	ID() string
	DisplayName() string
	Capabilities() AgentCapabilities  // 缺省 AllCapabilities
	SupportedModes() []PermissionMode // 缺省 AllPermissionModes
	IsAvailable(ctx context.Context) bool
	Doctor(ctx context.Context, force bool) BackendProbe
	ListModels(ctx context.Context) ([]ModelInfo, error)
	ListThreads(ctx context.Context, cwd string, limit int) ([]ThreadSummary, error)
	ReadHistory(ctx context.Context, cwd, sessionID string, maxTurns int) (ThreadHistory, error)
	StartThread(ctx context.Context, opts StartThreadOptions) (AgentThread, error)
	ResumeThread(ctx context.Context, opts ResumeThreadOptions) (AgentThread, error)
}

// ── 用量归一化 ──────────────────────────────────────────────────

// UsageErrorKind 用量获取失败原因。
type UsageErrorKind string

const (
	UsageErrNoAuth      UsageErrorKind = "no-auth"
	UsageErrAPIKeyMode  UsageErrorKind = "api-key-mode"
	UsageErrNeedRelogin UsageErrorKind = "need-relogin"
	UsageErrTransient   UsageErrorKind = "transient"
)

// UsageError 用量错误。
type UsageError struct {
	Kind UsageErrorKind
	Msg  string
}

func (e *UsageError) Error() string { return e.Msg }
func NewUsageError(kind UsageErrorKind, msg string) *UsageError {
	return &UsageError{Kind: kind, Msg: msg}
}

// RateWindow 限额窗口。
type RateWindow struct {
	UsedPercent   int
	WindowSeconds int64 // 0=未知
	ResetAt       int64 // unix 秒，0=未知
}

// RateBucket 限额桶（主 + 副，或按 feature 的附加）。
type RateBucket struct {
	Name      string
	Primary   *RateWindow
	Secondary *RateWindow
}

// AccountUsageSnapshot 用量快照（主限额 + 按 feature 附加）。
type AccountUsageSnapshot struct {
	PlanType  string
	Main      RateBucket
	Extras    []RateBucket
	FetchedAt int64 // ms
}

// DailyBucket 每日 token（热力图）。
type DailyBucket struct {
	Date   string // YYYY-MM-DD
	Tokens int
}

// InvocationCount top 调用统计。
type InvocationCount struct {
	Name  string
	Count int
	Kind  string // plugin|skill
}

// AccountProfileStats 用量统计画像。
type AccountProfileStats struct {
	DisplayName       string
	LifetimeTokens    int64
	PeakDailyTokens   int64
	CurrentStreakDays int
	LongestStreakDays int
	LongestTurnSec    int64
	TotalThreads      int
	FastModePct       int
	TotalSkillsUsed   int
	UniqueSkillsUsed  int
	MostUsedEffort    string
	MostUsedEffortPct int
	TopInvocations    []InvocationCount
	DailyBuckets      []DailyBucket
	StatsAsOf         string
}

// AccountUsageBundle /usage 卡完整数据。
type AccountUsageBundle struct {
	Profile AccountProfileStats
	Usage   AccountUsageSnapshot
}
