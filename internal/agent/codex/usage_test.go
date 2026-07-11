package codex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func redirectCodexHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	return dir
}

// ── mapUsageResponse ────────────────────────────────────────────

func TestMapUsageResponse_WindowsAndClamp(t *testing.T) {
	pct := 150
	raw := rawUsageResponse{
		PlanType: "pro",
		RateLimit: &rawRateLimit{
			PrimaryWindow:   &rawWindow{UsedPercent: &pct, WindowSeconds: int64Ptr(18000), ResetAt: int64Ptr(1700000000)},
			SecondaryWindow: &rawWindow{UsedPercent: intPtrLocal(30), WindowSeconds: int64Ptr(604800)},
		},
		AdditionalRateLimits: []struct {
			LimitName string        `json:"limit_name"`
			RateLimit *rawRateLimit `json:"rate_limit"`
		}{
			{LimitName: "GPT-5.3-Codex-Spark", RateLimit: &rawRateLimit{PrimaryWindow: &rawWindow{UsedPercent: intPtrLocal(0)}}},
		},
	}
	snap := MapUsageResponse(raw, 1700000001000)
	if snap.PlanType != "pro" || snap.FetchedAt != 1700000001000 {
		t.Fatalf("plan/fetchedAt wrong: %+v", snap)
	}
	// used_percent=150 clamp 到 100。
	if snap.Main.Primary == nil || snap.Main.Primary.UsedPercent != 100 {
		t.Fatalf("primary should clamp to 100: %+v", snap.Main.Primary)
	}
	if snap.Main.Primary.WindowSeconds != 18000 || snap.Main.Primary.ResetAt != 1700000000 {
		t.Fatalf("primary window seconds/resetAt wrong: %+v", snap.Main.Primary)
	}
	if snap.Main.Secondary == nil || snap.Main.Secondary.UsedPercent != 30 {
		t.Fatalf("secondary wrong: %+v", snap.Main.Secondary)
	}
	if len(snap.Extras) != 1 || snap.Extras[0].Name != "GPT-5.3-Codex-Spark" {
		t.Fatalf("extras wrong: %+v", snap.Extras)
	}
}

func TestMapUsageResponse_NullWindows(t *testing.T) {
	// rate_limit=null / 窗口缺失 / used_percent 缺失 → 对应 Primary/Secondary 为 nil。
	snap := MapUsageResponse(rawUsageResponse{}, 1)
	if snap.Main.Primary != nil || snap.Main.Secondary != nil {
		t.Fatalf("null windows should map to nil: %+v", snap.Main)
	}
}

func TestMapUsageResponse_SkipNilExtraRateLimits(t *testing.T) {
	raw := rawUsageResponse{
		AdditionalRateLimits: []struct {
			LimitName string        `json:"limit_name"`
			RateLimit *rawRateLimit `json:"rate_limit"`
		}{
			{LimitName: "ok", RateLimit: &rawRateLimit{}},
			{LimitName: "skip", RateLimit: nil},
		},
	}
	snap := MapUsageResponse(raw, 1)
	if len(snap.Extras) != 1 || snap.Extras[0].Name != "ok" {
		t.Fatalf("nil rate_limit extras should be skipped: %+v", snap.Extras)
	}
}

// ── mapProfileResponse ──────────────────────────────────────────

func TestMapProfileResponse_DisplayNameNoUsernameFallback(t *testing.T) {
	u := "leaked@local"
	raw := rawProfileResponse{}
	raw.Profile.DisplayName = "My Name"
	raw.Profile.Username = u
	out := MapProfileResponse(raw)
	if out.DisplayName != "My Name" {
		t.Fatalf("display name wrong: %q", out.DisplayName)
	}
	// 绝不兜底 username（防邮箱 local part 泄露）。
}

func TestMapProfileResponse_TopInvocationsKind(t *testing.T) {
	plugin := "my-plugin"
	skill := "my-skill"
	raw := rawProfileResponse{Stats: &rawProfileStats{
		TopInvocations: []struct {
			Type       string  `json:"type"`
			PluginName *string `json:"plugin_name"`
			SkillName  *string `json:"skill_name"`
			UsageCount *int    `json:"usage_count"`
		}{
			{PluginName: &plugin, UsageCount: intPtrLocal(5)},
			{SkillName: &skill, UsageCount: intPtrLocal(3)},
			{SkillName: new(string)}, // 空 name → 过滤
		},
	}}
	out := MapProfileResponse(raw)
	if len(out.TopInvocations) != 2 {
		t.Fatalf("want 2 invocations (empty filtered), got %d: %+v", len(out.TopInvocations), out.TopInvocations)
	}
	if out.TopInvocations[0].Name != "my-plugin" || out.TopInvocations[0].Kind != "plugin" {
		t.Fatalf("plugin invocation wrong: %+v", out.TopInvocations[0])
	}
	if out.TopInvocations[1].Name != "my-skill" || out.TopInvocations[1].Kind != "skill" {
		t.Fatalf("skill invocation wrong: %+v", out.TopInvocations[1])
	}
}

func TestMapProfileResponse_DailyBucketsFilterEmptyDate(t *testing.T) {
	raw := rawProfileResponse{Stats: &rawProfileStats{
		DailyUsageBuckets: []struct {
			StartDate string `json:"start_date"`
			Tokens    *int   `json:"tokens"`
		}{
			{StartDate: "2026-07-01", Tokens: intPtrLocal(100)},
			{StartDate: "", Tokens: intPtrLocal(50)}, // 无日期 → 过滤
			{StartDate: "2026-07-02"},                // tokens 缺失 → 0
		},
	}}
	out := MapProfileResponse(raw)
	if len(out.DailyBuckets) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(out.DailyBuckets))
	}
	if out.DailyBuckets[1].Tokens != 0 {
		t.Fatalf("missing tokens should default 0: %+v", out.DailyBuckets[1])
	}
}

// ── jwtExpMs ────────────────────────────────────────────────────

func TestJwtExpMs(t *testing.T) {
	// 构造 payload {"exp":1700000000}，base64url 无 padding。
	payload, _ := json.Marshal(map[string]int64{"exp": 1700000000})
	enc := base64.RawURLEncoding.EncodeToString(payload)
	token := "header." + enc + ".sig"
	exp, ok := JwtExpMs(token)
	if !ok || exp != 1700000000*1000 {
		t.Fatalf("jwtExpMs = %d ok=%v, want %d true", exp, ok, 1700000000*1000)
	}
	// 坏 token。
	if _, ok := JwtExpMs("not.a.jwt"); ok {
		t.Fatal("bad jwt should return ok=false")
	}
	if _, ok := JwtExpMs("onlyone"); ok {
		t.Fatal("single-part token should return ok=false")
	}
}

// ── readCodexAuth ───────────────────────────────────────────────

func writeCodexAuth(t *testing.T, body string) {
	t.Helper()
	dir := redirectCodexHome(t)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadCodexAuth_OK(t *testing.T) {
	writeCodexAuth(t, `{"auth_mode":"chatgpt","last_refresh":"x","tokens":{"access_token":"tok123","account_id":"acc-1"}}`)
	a, err := ReadCodexAuth()
	if err != nil || a.AccessToken != "tok123" || a.AccountID != "acc-1" {
		t.Fatalf("readCodexAuth OK wrong: %+v %v", a, err)
	}
}

func TestReadCodexAuth_NoFile(t *testing.T) {
	redirectCodexHome(t)
	_, err := ReadCodexAuth()
	if err == nil {
		t.Fatal("missing auth.json should error")
	}
	var ue *agent.UsageError
	if !errors.As(err, &ue) || ue.Kind != agent.UsageErrNoAuth {
		t.Fatalf("want no-auth UsageError, got %v", err)
	}
}

func TestReadCodexAuth_APIKeyMode(t *testing.T) {
	writeCodexAuth(t, `{"auth_mode":"apikey","tokens":{"api_key":"sk-..."}}`)
	_, err := ReadCodexAuth()
	var ue *agent.UsageError
	if !errors.As(err, &ue) || ue.Kind != agent.UsageErrAPIKeyMode {
		t.Fatalf("want api-key-mode UsageError, got %v", err)
	}
}

// ── chatgptBaseUrl ──────────────────────────────────────────────

func TestChatgptBaseUrl_TopLevelKey(t *testing.T) {
	dir := redirectCodexHome(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`chatgpt_base_url = "https://custom.example.com/api/"
[features]
hooks = true
chatgpt_base_url = "https://ignored.in-section.com"
`), 0o600)
	if got := ChatgptBaseUrl(); got != "https://custom.example.com/api" {
		t.Fatalf("top-level chatgpt_base_url (trimmed trailing /) wrong: %q", got)
	}
}

func TestChatgptBaseUrl_DefaultWhenMissing(t *testing.T) {
	redirectCodexHome(t)
	if got := ChatgptBaseUrl(); got != defaultChatgptBaseURL {
		t.Fatalf("missing config.toml should use default, got %q", got)
	}
}

// ── 辅助 ────────────────────────────────────────────────────────

func intPtrLocal(i int) *int  { return &i }
func int64Ptr(i int64) *int64 { return &i }
