package codex

// usage.go —— codex 账号用量【纯数据映射层】（对齐 TS codex-appserver/usage 的纯函数部分）。
//
// 含：readCodexAuth（auth.json 半截重试）、jwtExpMs、chatgptBaseUrl（config.toml 顶层键）、
// mapUsageResponse / mapProfileResponse（snake_case → 归一化）。
//
// HTTP 拉数层（fetchUsageBundle + 401 兜底链 + refreshViaAppServer）依赖 client-pool 的
// UtilityRequest(account/read) + 真实 ChatGPT 端点，在 backend.go port 时一并实现（避免
// usage → client-pool 的依赖在数据层就绑死，便于本层纯函数独立测试）。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

const defaultChatgptBaseURL = "https://chatgpt.com/backend-api"

// CodexAuth auth.json 读出的 ChatGPT 登录态（绝不缓存——任何 codex 进程随时可能轮换）。
type CodexAuth struct {
	AccessToken string
	AccountID   string
	LastRefresh string
}

// ResolveCodexHome $CODEX_HOME 或 ~/.codex。
func ResolveCodexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".codex")
}

// ReadCodexAuth 现读 auth.json。codex persist 是 truncate 原地写、非原子，并发读可能撞上
// 半截 JSON——parse 失败短暂重试 3 次再放弃。文件缺失→no-auth；无 access_token→api-key-mode。
func ReadCodexAuth() (*CodexAuth, error) {
	file := filepath.Join(ResolveCodexHome(), "auth.json")
	var lastErr error
	for i := 0; i < 3; i++ {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, agent.NewUsageError(agent.UsageErrNoAuth, fmt.Sprintf("读不到 %s：%v", file, err))
		}
		var j struct {
			AuthMode    string `json:"auth_mode"`
			LastRefresh string `json:"last_refresh"`
			Tokens      struct {
				AccessToken string `json:"access_token"`
				AccountID   string `json:"account_id"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			lastErr = err // 半截 JSON：等 codex 写完再读
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if j.Tokens.AccessToken == "" {
			return nil, agent.NewUsageError(agent.UsageErrAPIKeyMode, "auth.json 没有 ChatGPT access_token（API-key 登录模式）")
		}
		return &CodexAuth{AccessToken: j.Tokens.AccessToken, AccountID: j.Tokens.AccountID, LastRefresh: j.LastRefresh}, nil
	}
	return nil, agent.NewUsageError(agent.UsageErrNoAuth, fmt.Sprintf("auth.json 反复解析失败：%v", lastErr))
}

// JwtExpMs 本地解 JWT 的 exp（毫秒）；解不出来返回 ok=false（按未知处理，不拦请求）。
func JwtExpMs(token string) (int64, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, false
	}
	var p struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.Exp == 0 {
		return 0, false
	}
	return p.Exp * 1000, true
}

var chatgptBaseURLRe = regexp.MustCompile(`^chatgpt_base_url\s*=\s*"([^"]+)"`)

// ChatgptBaseUrl 尊重 $CODEX_HOME/config.toml 顶层 chatgpt_base_url（首个 [section] 后忽略）。
func ChatgptBaseUrl() string {
	raw, err := os.ReadFile(filepath.Join(ResolveCodexHome(), "config.toml"))
	if err != nil {
		return defaultChatgptBaseURL
	}
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			break // 进入 section，顶层键扫描结束
		}
		if m := chatgptBaseURLRe.FindStringSubmatch(t); len(m) >= 2 {
			return strings.TrimRight(m[1], "/")
		}
	}
	return defaultChatgptBaseURL
}

// ── mapUsageResponse（wham/usage 限额）───────────────────────────

type rawWindow struct {
	UsedPercent   *int   `json:"used_percent"`
	WindowSeconds *int64 `json:"limit_window_seconds"`
	ResetAt       *int64 `json:"reset_at"`
}

type rawRateLimit struct {
	PrimaryWindow   *rawWindow `json:"primary_window"`
	SecondaryWindow *rawWindow `json:"secondary_window"`
}

type rawUsageResponse struct {
	PlanType             string        `json:"plan_type"`
	RateLimit            *rawRateLimit `json:"rate_limit"`
	AdditionalRateLimits []struct {
		LimitName string        `json:"limit_name"`
		RateLimit *rawRateLimit `json:"rate_limit"`
	} `json:"additional_rate_limits"`
}

func mapWindow(w *rawWindow) *agent.RateWindow {
	if w == nil || w.UsedPercent == nil {
		return nil
	}
	rw := &agent.RateWindow{UsedPercent: clampInt(*w.UsedPercent, 0, 100)}
	if w.WindowSeconds != nil {
		rw.WindowSeconds = *w.WindowSeconds
	}
	if w.ResetAt != nil {
		rw.ResetAt = *w.ResetAt
	}
	return rw
}

func mapBucket(rl *rawRateLimit, name string) agent.RateBucket {
	if rl == nil {
		return agent.RateBucket{Name: name}
	}
	return agent.RateBucket{
		Name:      name,
		Primary:   mapWindow(rl.PrimaryWindow),
		Secondary: mapWindow(rl.SecondaryWindow),
	}
}

// MapUsageResponse 把 wham/usage 原始响应归一为 AccountUsageSnapshot。
func MapUsageResponse(raw rawUsageResponse, fetchedAtMs int64) agent.AccountUsageSnapshot {
	snap := agent.AccountUsageSnapshot{
		PlanType:  raw.PlanType,
		Main:      mapBucket(raw.RateLimit, ""),
		FetchedAt: fetchedAtMs,
	}
	for _, x := range raw.AdditionalRateLimits {
		if x.RateLimit == nil {
			continue
		}
		snap.Extras = append(snap.Extras, mapBucket(x.RateLimit, x.LimitName))
	}
	return snap
}

// ── mapProfileResponse（wham/profiles/me 统计 + 热力图）──────────

type rawProfileStats struct {
	LifetimeTokens                    *int64 `json:"lifetime_tokens"`
	PeakDailyTokens                   *int64 `json:"peak_daily_tokens"`
	CurrentStreakDays                 *int   `json:"current_streak_days"`
	LongestStreakDays                 *int   `json:"longest_streak_days"`
	LongestRunningTurnSec             *int64 `json:"longest_running_turn_sec"`
	TotalThreads                      *int   `json:"total_threads"`
	FastModeUsagePercentage           *int   `json:"fast_mode_usage_percentage"`
	TotalSkillsUsed                   *int   `json:"total_skills_used"`
	UniqueSkillsUsed                  *int   `json:"unique_skills_used"`
	MostUsedReasoningEffort           string `json:"most_used_reasoning_effort"`
	MostUsedReasoningEffortPercentage *int   `json:"most_used_reasoning_effort_percentage"`
	TopInvocations                    []struct {
		Type       string  `json:"type"`
		PluginName *string `json:"plugin_name"`
		SkillName  *string `json:"skill_name"`
		UsageCount *int    `json:"usage_count"`
	} `json:"top_invocations"`
	DailyUsageBuckets []struct {
		StartDate string `json:"start_date"`
		Tokens    *int   `json:"tokens"`
	} `json:"daily_usage_buckets"`
}

type rawProfileResponse struct {
	Profile struct {
		DisplayName string `json:"display_name"`
		Username    string `json:"username"`
	} `json:"profile"`
	Stats    *rawProfileStats `json:"stats"`
	Metadata struct {
		StatsAsOf string `json:"stats_as_of"`
	} `json:"metadata"`
}

// MapProfileResponse 把 wham/profiles/me 归一为 AccountProfileStats。
// 只用 display_name（绝不兜底 username——后者是邮箱 local part，会随分享卡泄出）。
func MapProfileResponse(raw rawProfileResponse) agent.AccountProfileStats {
	out := agent.AccountProfileStats{
		DisplayName: raw.Profile.DisplayName,
		StatsAsOf:   raw.Metadata.StatsAsOf,
	}
	if raw.Stats != nil {
		s := raw.Stats
		out.LifetimeTokens = ptrInt64Val(s.LifetimeTokens)
		out.PeakDailyTokens = ptrInt64Val(s.PeakDailyTokens)
		out.CurrentStreakDays = ptrIntVal(s.CurrentStreakDays)
		out.LongestStreakDays = ptrIntVal(s.LongestStreakDays)
		out.LongestTurnSec = ptrInt64Val(s.LongestRunningTurnSec)
		out.TotalThreads = ptrIntVal(s.TotalThreads)
		out.FastModePct = ptrIntVal(s.FastModeUsagePercentage)
		out.TotalSkillsUsed = ptrIntVal(s.TotalSkillsUsed)
		out.UniqueSkillsUsed = ptrIntVal(s.UniqueSkillsUsed)
		out.MostUsedEffort = s.MostUsedReasoningEffort
		out.MostUsedEffortPct = ptrIntVal(s.MostUsedReasoningEffortPercentage)
		for _, t := range s.TopInvocations {
			name := ""
			kind := "skill"
			if t.PluginName != nil && *t.PluginName != "" {
				name = *t.PluginName
				kind = "plugin"
			} else if t.SkillName != nil {
				name = *t.SkillName
			}
			if name == "" {
				continue
			}
			cnt := 0
			if t.UsageCount != nil {
				cnt = *t.UsageCount
			}
			out.TopInvocations = append(out.TopInvocations, agent.InvocationCount{Name: name, Count: cnt, Kind: kind})
		}
		for _, b := range s.DailyUsageBuckets {
			if b.StartDate == "" {
				continue
			}
			tokens := 0
			if b.Tokens != nil {
				tokens = *b.Tokens
			}
			out.DailyBuckets = append(out.DailyBuckets, agent.DailyBucket{Date: b.StartDate, Tokens: tokens})
		}
	}
	return out
}

// ── 小工具 ───────────────────────────────────────────────────────

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func ptrIntVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func ptrInt64Val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
