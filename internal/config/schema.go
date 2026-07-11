package config

import "encoding/json"

// schema.go —— AppConfig 配置 schema + 全部归一 getter（字节级对齐 TS config/schema）。
//
// 关键映射点（见方案 §4）：
//   - SecretInput = 明文 string | SecretRef（联合类型，自定义 JSON 编解码）。
//   - 可选 bool 字段用 *bool：nil=未设置→默认，非 nil=用户显式设定（TS 用 undefined 区分）。
//   - ShowModel 兼容历史 bool（true→always、false→off）与 off/running/always 三档。
//   - 数值字段 clamp 边界严格复刻 TS（maxConcurrentRuns[1,50]、agentStopGrace[100,30000]ms、
//     runIdle[10,3600]s 且 0=关闭、cliBridge 各 timeout[1,86400]、presence idle[10,3600]）。

// ── 基础类型 ─────────────────────────────────────────────────────

type TenantBrand string

const (
	TenantFeishu TenantBrand = "feishu"
	TenantLark   TenantBrand = "lark"
)

// SecretRef 指向 config.json 之外的密钥（keystore / env / file / exec provider）。
type SecretRef struct {
	Source   string `json:"source"`             // env|file|exec
	Provider string `json:"provider,omitempty"` // provider 名（对应 secrets.providers[key]）
	ID       string `json:"id"`
}

// SecretInput 是 string（明文 / "${VAR}" 模板）或 SecretRef 的联合。
type SecretInput struct {
	Plain string
	Ref   *SecretRef
}

// PlainSecret 构造明文输入。
func PlainSecret(s string) SecretInput { return SecretInput{Plain: s} }

// RefSecret 构造引用输入。
func RefSecret(r SecretRef) SecretInput { return SecretInput{Ref: &r} }

func (s SecretInput) IsRef() bool          { return s.Ref != nil }
func (s SecretInput) IsZero() bool         { return s.Plain == "" && s.Ref == nil }
func (s SecretInput) PlainOrEmpty() string { return s.Plain }

func (s SecretInput) MarshalJSON() ([]byte, error) {
	if s.Ref != nil {
		return json.Marshal(s.Ref)
	}
	return json.Marshal(s.Plain)
}

func (s *SecretInput) UnmarshalJSON(b []byte) error {
	// 优先按明文字符串解析；失败再按 SecretRef 对象解析。
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = SecretInput{Plain: str}
		return nil
	}
	var ref SecretRef
	if err := json.Unmarshal(b, &ref); err != nil {
		return err
	}
	*s = SecretInput{Ref: &ref}
	return nil
}

type AppCredentials struct {
	ID     string      `json:"id"`
	Secret SecretInput `json:"secret"`
	Tenant TenantBrand `json:"tenant"`
}

type ProviderConfig struct {
	Source            string            `json:"source"`
	Allowlist         []string          `json:"allowlist,omitempty"`
	Path              string            `json:"path,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	PassEnv           []string          `json:"passEnv,omitempty"`
	NoOutputTimeoutMs int               `json:"noOutputTimeoutMs,omitempty"`
	MaxOutputBytes    int               `json:"maxOutputBytes,omitempty"`
}

type SecretsConfig struct {
	Providers map[string]ProviderConfig `json:"providers,omitempty"`
	Defaults  struct {
		Env  string `json:"env,omitempty"`
		File string `json:"file,omitempty"`
		Exec string `json:"exec,omitempty"`
	} `json:"defaults,omitempty"`
}

type AppAccess struct {
	OwnerOpenID  string   `json:"ownerOpenId,omitempty"`
	Admins       []string `json:"admins,omitempty"`
	AllowedUsers []string `json:"allowedUsers,omitempty"` // @deprecated
	AllowedChats []string `json:"allowedChats,omitempty"`
}

type AppPreferences struct {
	ProjectsRootDir       string                `json:"projectsRootDir,omitempty"` // 空白项目的默认父目录（仅 config.json，支持绝对路径或 ~ 开头）
	MessageReply          string                `json:"messageReply,omitempty"`   // card|markdown|text
	ShowToolCalls         *bool                 `json:"showToolCalls,omitempty"`
	ShowModel             interface{}           `json:"showModel,omitempty"` // off|running|always | bool
	MaxConcurrentRuns     *int                  `json:"maxConcurrentRuns,omitempty"`
	RunIdleTimeoutSeconds *int                  `json:"runIdleTimeoutSeconds,omitempty"`
	PendingPolicy         string                `json:"pendingPolicy,omitempty"` // steer|queue
	RequireMentionInGroup *bool                 `json:"requireMentionInGroup,omitempty"`
	Access                *AppAccess            `json:"access,omitempty"`
	AgentStopGraceMs      *int                  `json:"agentStopGraceMs,omitempty"`
	CliBridge             *CliBridgePreferences `json:"cliBridge,omitempty"`
	// CompletionReminder 普通群任务的结束提醒策略（与 CliBridge taskCompletion 是两条独立链路）。
	CompletionReminder *CompletionReminderConfig `json:"completionReminder,omitempty"`
	// Comments 云文档评论 @bot 流的全局配置（仅后端/模型/推理强度三个短标量，都可空）。
	Comments *CommentsConfig `json:"comments,omitempty"`
}

// AppConfig —— 单个 bot 的 config.json 顶层结构。
type AppConfig struct {
	Accounts struct {
		App AppCredentials `json:"app"`
	} `json:"accounts"`
	Secrets     *SecretsConfig  `json:"secrets,omitempty"`
	Preferences *AppPreferences `json:"preferences,omitempty"`
}

// ── 完整性 / 密钥工具 ───────────────────────────────────────────

// IsComplete 判断 config 是否具备可用的 bot 凭据（id+secret+tenant 齐全）。
func IsComplete(cfg AppConfig) bool {
	app := cfg.Accounts.App
	return app.ID != "" && HasSecret(app.Secret) && app.Tenant != ""
}

// HasSecret 判断 SecretInput 是否携带有效密钥。
func HasSecret(s SecretInput) bool {
	if s.IsRef() {
		return s.Ref.Source != "" && s.Ref.ID != ""
	}
	return s.Plain != ""
}

// IsSecretRef 类型谓词。
func IsSecretRef(s SecretInput) bool { return s.IsRef() }

// SecretKeyForApp 返回该 bot 在 keystore 里的 key。
func SecretKeyForApp(appID string) string { return "app-" + appID }

// ── 偏好 getter（带默认值 / clamp）────────────────────────────────

func GetMessageReplyMode(cfg AppConfig) string {
	r := ""
	if cfg.Preferences != nil {
		r = cfg.Preferences.MessageReply
	}
	switch r {
	case "card", "markdown", "text":
		return r
	}
	return "card"
}

func GetShowToolCalls(cfg AppConfig) bool {
	if cfg.Preferences == nil || cfg.Preferences.ShowToolCalls == nil {
		return true
	}
	return *cfg.Preferences.ShowToolCalls
}

// GetModelDisplay 兼容历史布尔（true→always、false→off）与三档字符串；默认 running。
func GetModelDisplay(cfg AppConfig) string {
	v := interface{}(nil)
	if cfg.Preferences != nil {
		v = cfg.Preferences.ShowModel
	}
	switch t := v.(type) {
	case string:
		if t == "running" || t == "always" || t == "off" {
			return t
		}
	case bool:
		if t {
			return "always"
		}
		return "off"
	}
	return "running"
}

func GetMaxConcurrentRuns(cfg AppConfig) int {
	if cfg.Preferences == nil || cfg.Preferences.MaxConcurrentRuns == nil {
		return 10
	}
	raw := *cfg.Preferences.MaxConcurrentRuns
	if raw < 1 {
		return 10
	}
	if raw > 50 {
		return 50
	}
	return raw
}

func GetRequireMentionInGroup(cfg AppConfig) bool {
	if cfg.Preferences == nil || cfg.Preferences.RequireMentionInGroup == nil {
		return true
	}
	return *cfg.Preferences.RequireMentionInGroup
}

func GetPendingPolicy(cfg AppConfig) string {
	if cfg.Preferences != nil && cfg.Preferences.PendingPolicy == "queue" {
		return "queue"
	}
	return "steer"
}

// ── 普通群任务结束提醒 ──────────────────────────────────────────
//
// 与 CliBridgePreferences.taskCompletion（本地 CLI agent 的 Stop 转发）是两条完全独立的通知链路。

// CompletionReminderMode 普通群任务结束提醒策略。
type CompletionReminderMode string

const (
	// ReminderManual 仅用户本轮手动开启。
	ReminderManual CompletionReminderMode = "manual"
	// ReminderLong 超过耗时阈值（longTaskMinutes 分钟）。
	ReminderLong CompletionReminderMode = "long"
	// ReminderFailures 仅失败 / 假死超时（默认）。
	ReminderFailures CompletionReminderMode = "failures"
	// ReminderAlways 每次结束都提醒。
	ReminderAlways CompletionReminderMode = "always"
)

// CompletionReminderConfig raw（config.json 中的 completionReminder）。
type CompletionReminderConfig struct {
	Mode            CompletionReminderMode `json:"mode,omitempty"` // manual|long|failures|always
	LongTaskMinutes *int                   `json:"longTaskMinutes,omitempty"`
}

// ResolvedCompletionReminderConfig fully-resolved。
type ResolvedCompletionReminderConfig struct {
	Mode            CompletionReminderMode
	LongTaskMinutes int
}

const (
	CompletionReminderLongTaskMinMinutes = 1
	CompletionReminderLongTaskMaxMinutes = 1440
	CompletionReminderLongTaskDefMinutes = 3
)

// GetCompletionReminderConfig 安全归一普通群任务结束提醒设置。
// 未知 mode 回落 failures；非法/非正阈值用 3 分钟默认，越界正数 clamp 到 [1,1440]。
func GetCompletionReminderConfig(cfg AppConfig) ResolvedCompletionReminderConfig {
	var mode CompletionReminderMode = ReminderFailures
	minutes := 0
	if cfg.Preferences != nil && cfg.Preferences.CompletionReminder != nil {
		raw := cfg.Preferences.CompletionReminder.Mode
		if raw == ReminderManual || raw == ReminderLong || raw == ReminderAlways || raw == ReminderFailures {
			mode = raw
		}
		if m := cfg.Preferences.CompletionReminder.LongTaskMinutes; m != nil {
			minutes = *m
		}
	}
	longTaskMinutes := CompletionReminderLongTaskDefMinutes
	if minutes > 0 {
		if minutes < CompletionReminderLongTaskMinMinutes {
			longTaskMinutes = CompletionReminderLongTaskMinMinutes
		} else if minutes > CompletionReminderLongTaskMaxMinutes {
			longTaskMinutes = CompletionReminderLongTaskMaxMinutes
		} else {
			longTaskMinutes = minutes
		}
	}
	return ResolvedCompletionReminderConfig{Mode: mode, LongTaskMinutes: longTaskMinutes}
}

// ShouldShowCompletionReminderButton 每轮「完成后提醒我」按钮仅在 manual 模式出现。
func ShouldShowCompletionReminderButton(cfg AppConfig) bool {
	return GetCompletionReminderConfig(cfg).Mode == ReminderManual
}

// CompletionReminderOutcome 普通群任务提醒策略理解的终态。
type CompletionReminderOutcome string

const (
	ReminderDone        CompletionReminderOutcome = "done"
	ReminderError       CompletionReminderOutcome = "error"
	ReminderIdleTimeout CompletionReminderOutcome = "idle_timeout"
	ReminderInterrupted CompletionReminderOutcome = "interrupted"
	ReminderCancelled   CompletionReminderOutcome = "cancelled"
)

// CompletionReminderDecision 一次普通任务终态的判定输入。
type CompletionReminderDecision struct {
	Outcome           CompletionReminderOutcome
	ElapsedMs         int64 // 该轮（排队/运行）墙钟耗时（毫秒）
	ManuallyRequested bool  // 发起者是否本轮手动开启一次性提醒
}

// ShouldSendCompletionReminder 判定普通群任务终态是否要发独立提醒。
// 用户中断 / 排队取消永不通知；long 仅看墙钟耗时；failures 覆盖 agent 错误与 idle watchdog。
func ShouldSendCompletionReminder(cfg AppConfig, decision CompletionReminderDecision) bool {
	if decision.Outcome == ReminderInterrupted || decision.Outcome == ReminderCancelled {
		return false
	}
	reminder := GetCompletionReminderConfig(cfg)
	switch reminder.Mode {
	case ReminderManual:
		return decision.ManuallyRequested
	case ReminderLong:
		elapsed := decision.ElapsedMs
		if elapsed < 0 {
			elapsed = 0
		}
		return elapsed >= int64(reminder.LongTaskMinutes)*60000
	case ReminderAlways:
		return true
	case ReminderFailures:
		return decision.Outcome == ReminderError || decision.Outcome == ReminderIdleTimeout
	}
	return false
}

// CommentsConfig 云文档评论流的全局可配项（仅后端/模型/推理强度三个短标量，都可空）。
// 缺字段不崩——loadConfig 返回 Partial 时这里返回空结构。Effort 用 string（消费处转 agent.ReasoningEffort，
// 避免 config↔agent 循环依赖）。
type CommentsConfig struct {
	Backend string `json:"backend,omitempty"` // 评论流新会话用的后端 id（缺省 → 默认后端）
	Model   string `json:"model,omitempty"`   // 缺省 → 后端默认模型
	Effort  string `json:"effort,omitempty"`   // 缺省 → 模型默认
}

// GetCommentsConfig 云文档评论流的全局配置（每字段都可空，消费侧自带回落）。
func GetCommentsConfig(cfg AppConfig) CommentsConfig {
	if cfg.Preferences == nil || cfg.Preferences.Comments == nil {
		return CommentsConfig{}
	}
	return *cfg.Preferences.Comments
}

func GetAgentStopGraceMs(cfg AppConfig) int {
	if cfg.Preferences == nil || cfg.Preferences.AgentStopGraceMs == nil {
		return 5000
	}
	raw := *cfg.Preferences.AgentStopGraceMs
	if raw < 100 {
		raw = 100
	}
	if raw > 30000 {
		raw = 30000
	}
	return raw
}

const (
	RunIdleTimeoutMinSec = 10
	RunIdleTimeoutMaxSec = 3600
)

// GetRunIdleTimeoutMs 返回单轮 idle watchdog 毫秒值；ok=false 表示关闭（0 或缺省回退前的 0）。
// 缺省/非法 → 120000（开）；0 → 关闭；正数 clamp 到 [10,3600] 秒。
func GetRunIdleTimeoutMs(cfg AppConfig) (ms int, on bool) {
	if cfg.Preferences == nil || cfg.Preferences.RunIdleTimeoutSeconds == nil {
		return 120000, true
	}
	raw := *cfg.Preferences.RunIdleTimeoutSeconds
	if raw == 0 {
		return 0, false
	}
	if raw < 0 {
		return 120000, true
	}
	if raw < RunIdleTimeoutMinSec {
		raw = RunIdleTimeoutMinSec
	}
	if raw > RunIdleTimeoutMaxSec {
		raw = RunIdleTimeoutMaxSec
	}
	return raw * 1000, true
}

// ── 访问控制 ────────────────────────────────────────────────────

// ResolveOwner 返回 owner open_id（显式 ownerOpenId，否则首个 admin）；从未注册则空。
func ResolveOwner(cfg AppConfig) string {
	if cfg.Preferences == nil || cfg.Preferences.Access == nil {
		return ""
	}
	acc := cfg.Preferences.Access
	if acc.OwnerOpenID != "" {
		return acc.OwnerOpenID
	}
	if len(acc.Admins) > 0 {
		return acc.Admins[0]
	}
	return ""
}

// IsAdmin owner 恒为 admin；其余看 admins 列表。
func IsAdmin(cfg AppConfig, senderID string) bool {
	if senderID == "" {
		return false
	}
	if senderID == ResolveOwner(cfg) {
		return true
	}
	if cfg.Preferences != nil && cfg.Preferences.Access != nil {
		for _, a := range cfg.Preferences.Access.Admins {
			if a == senderID {
				return true
			}
		}
	}
	return false
}

// IsChatAllowed 空白名单 = 全部；非空则必须在列表。
func IsChatAllowed(cfg AppConfig, chatID string) bool {
	if cfg.Preferences == nil || cfg.Preferences.Access == nil {
		return true
	}
	list := cfg.Preferences.Access.AllowedChats
	if len(list) == 0 {
		return true
	}
	for _, c := range list {
		if c == chatID {
			return true
		}
	}
	return false
}

// IsUserAllowedInProject admin 恒豁免；项目白名单空 = 所有人。
func IsUserAllowedInProject(cfg AppConfig, allowedUsers []string, senderID string) bool {
	if IsAdmin(cfg, senderID) {
		return true
	}
	if len(allowedUsers) == 0 {
		return true
	}
	for _, u := range allowedUsers {
		if u == senderID {
			return true
		}
	}
	return false
}

// ── CliBridge 偏好（raw + 归一）──────────────────────────────────

type CliBridgeAgentToggles struct {
	Claude *bool `json:"claude,omitempty"`
	Codex  *bool `json:"codex,omitempty"`
}

type CliBridgeKeepAwake struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type CliBridgeApproval struct {
	Enabled        *bool `json:"enabled,omitempty"`
	TimeoutSeconds *int  `json:"timeoutSeconds,omitempty"`
}

type CliBridgeTaskCompletion struct {
	Enabled             *bool `json:"enabled,omitempty"`
	ReplyEnabled        *bool `json:"replyEnabled,omitempty"`
	ReplyTimeoutSeconds *int  `json:"replyTimeoutSeconds,omitempty"`
}

type CliBridgeAllowCache struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

type CliBridgeCompletionSync struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type CliBridgePresence struct {
	Enabled              *bool  `json:"enabled,omitempty"`
	Platform             string `json:"platform,omitempty"` // auto|macos
	IdleThresholdSeconds *int   `json:"idleThresholdSeconds,omitempty"`
}

// CliBridgePreferences raw（config.json 里 cliBridge 字段，全可选）。
type CliBridgePreferences struct {
	Enabled                                *bool                    `json:"enabled,omitempty"`
	Delivery                               string                   `json:"delivery,omitempty"`
	IncludeBridgeOwnedSessionsForDebugging *bool                    `json:"includeBridgeOwnedSessionsForDebugging,omitempty"`
	Agents                                 *CliBridgeAgentToggles   `json:"agents,omitempty"`
	NotifyScope                            string                   `json:"notifyScope,omitempty"`
	KeepAwake                              *CliBridgeKeepAwake      `json:"keepAwake,omitempty"`
	Approval                               *CliBridgeApproval       `json:"approval,omitempty"`
	TaskCompletion                         *CliBridgeTaskCompletion `json:"taskCompletion,omitempty"`
	AllowCache                             *CliBridgeAllowCache     `json:"allowCache,omitempty"`
	CompletionSync                         *CliBridgeCompletionSync `json:"completionSync,omitempty"`
	Presence                               *CliBridgePresence       `json:"presence,omitempty"`
}

// ResolvedCliBridgePreferences fully-resolved（每字段非空，已 clamp）。
type ResolvedCliBridgePreferences struct {
	Enabled                                bool
	Delivery                               string
	IncludeBridgeOwnedSessionsForDebugging bool
	Agents                                 struct {
		Claude bool
		Codex  bool
	}
	NotifyScope string
	KeepAwake   struct{ Enabled bool }
	Approval    struct {
		Enabled        bool
		TimeoutSeconds int
	}
	TaskCompletion struct {
		Enabled             bool
		ReplyEnabled        bool
		ReplyTimeoutSeconds int
	}
	AllowCache struct {
		Enabled bool
		Scope   string
	}
	CompletionSync struct{ Enabled bool }
	Presence       struct {
		Enabled              bool
		Platform             string
		IdleThresholdSeconds int
	}
}

// GetCliBridgePreferences 把 raw 偏好归一为 fully-resolved 安全默认形态。
func GetCliBridgePreferences(cfg AppConfig) ResolvedCliBridgePreferences {
	var raw *CliBridgePreferences
	if cfg.Preferences != nil {
		raw = cfg.Preferences.CliBridge
	}
	r := ResolvedCliBridgePreferences{
		Enabled:                                false,
		Delivery:                               "away_only",
		IncludeBridgeOwnedSessionsForDebugging: false,
		NotifyScope:                            "all",
	}
	r.Agents.Claude = true
	r.Agents.Codex = true
	r.KeepAwake.Enabled = true
	r.Approval.Enabled = true
	r.Approval.TimeoutSeconds = 86400
	r.TaskCompletion.Enabled = true
	r.TaskCompletion.ReplyEnabled = true
	r.TaskCompletion.ReplyTimeoutSeconds = 1800
	r.AllowCache.Enabled = true
	r.AllowCache.Scope = "session"
	r.CompletionSync.Enabled = true
	r.Presence.Enabled = true
	r.Presence.Platform = "auto"
	r.Presence.IdleThresholdSeconds = 120
	if raw == nil {
		return r
	}
	if raw.Delivery == "always" || raw.Delivery == "away_only" {
		r.Delivery = raw.Delivery
	}
	if raw.Enabled != nil {
		r.Enabled = *raw.Enabled
	}
	if raw.IncludeBridgeOwnedSessionsForDebugging != nil {
		r.IncludeBridgeOwnedSessionsForDebugging = *raw.IncludeBridgeOwnedSessionsForDebugging
	}
	if raw.Agents != nil {
		if raw.Agents.Claude != nil {
			r.Agents.Claude = *raw.Agents.Claude
		}
		if raw.Agents.Codex != nil {
			r.Agents.Codex = *raw.Agents.Codex
		}
	}
	if raw.NotifyScope == "bound_projects" || raw.NotifyScope == "none" {
		r.NotifyScope = raw.NotifyScope
	}
	if raw.KeepAwake != nil && raw.KeepAwake.Enabled != nil {
		r.KeepAwake.Enabled = *raw.KeepAwake.Enabled
	}
	if raw.Approval != nil {
		if raw.Approval.Enabled != nil {
			r.Approval.Enabled = *raw.Approval.Enabled
		}
		if raw.Approval.TimeoutSeconds != nil {
			r.Approval.TimeoutSeconds = secondsOr(*raw.Approval.TimeoutSeconds, 86400, 1, 86400)
		}
	}
	if raw.TaskCompletion != nil {
		if raw.TaskCompletion.Enabled != nil {
			r.TaskCompletion.Enabled = *raw.TaskCompletion.Enabled
		}
		if raw.TaskCompletion.ReplyEnabled != nil {
			r.TaskCompletion.ReplyEnabled = *raw.TaskCompletion.ReplyEnabled
		}
		if raw.TaskCompletion.ReplyTimeoutSeconds != nil {
			r.TaskCompletion.ReplyTimeoutSeconds = secondsOr(*raw.TaskCompletion.ReplyTimeoutSeconds, 1800, 1, 86400)
		}
	}
	if raw.AllowCache != nil && raw.AllowCache.Enabled != nil {
		r.AllowCache.Enabled = *raw.AllowCache.Enabled
	}
	if raw.CompletionSync != nil && raw.CompletionSync.Enabled != nil {
		r.CompletionSync.Enabled = *raw.CompletionSync.Enabled
	}
	if raw.Presence != nil {
		if raw.Presence.Enabled != nil {
			r.Presence.Enabled = *raw.Presence.Enabled
		}
		if raw.Presence.Platform == "macos" {
			r.Presence.Platform = "macos"
		}
		if raw.Presence.IdleThresholdSeconds != nil {
			r.Presence.IdleThresholdSeconds = secondsOr(*raw.Presence.IdleThresholdSeconds, 120, 10, 3600)
		}
	}
	return r
}

// secondsOr：v<=0（含 0/负数）→ fallback；否则 clamp 到 [min,max]。对齐 TS。
func secondsOr(v, fallback, min, max int) int {
	if v <= 0 {
		return fallback
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// ResolveCliBridgeTarget 返回 owner open_id（cli-bridge 通知目标）；无 owner 则 ok=false。
func ResolveCliBridgeTarget(cfg AppConfig) (receiveID string, ok bool) {
	owner := ResolveOwner(cfg)
	if owner == "" {
		return "", false
	}
	return owner, true
}

// CanEnableCliBridge 能否开启 cli-bridge（需有 owner）。
func CanEnableCliBridge(cfg AppConfig) bool {
	_, ok := ResolveCliBridgeTarget(cfg)
	return ok
}
