package config

import "testing"

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func prefsWith(p AppPreferences) AppConfig {
	c := AppConfig{}
	c.Preferences = &p
	return c
}

// ── SecretInput 联合类型 JSON ────────────────────────────────────

func TestSecretInput_PlainRoundtrip(t *testing.T) {
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{ID: "cli_a", Secret: PlainSecret("shh"), Tenant: TenantFeishu}
	b := marshalJSON(t, cfg)
	var got AppConfig
	if err := jsonUnmarshal(t, b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Accounts.App.Secret.IsRef() || got.Accounts.App.Secret.Plain != "shh" {
		t.Fatalf("plain secret lost: %+v", got.Accounts.App.Secret)
	}
}

func TestSecretInput_RefRoundtrip(t *testing.T) {
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{
		ID:     "cli_a",
		Tenant: TenantLark,
		Secret: RefSecret(SecretRef{Source: "exec", Provider: "bridge", ID: "app-cli_a"}),
	}
	b := marshalJSON(t, cfg)
	// 引用形式必须序列化成 object，不能是字符串。
	if !containsStr(string(b), `"source":"exec"`) {
		t.Fatalf("ref secret should marshal to object: %s", b)
	}
	var got AppConfig
	jsonUnmarshal(t, b, &got)
	if !got.Accounts.App.Secret.IsRef() || got.Accounts.App.Secret.Ref.ID != "app-cli_a" {
		t.Fatalf("ref secret lost: %+v", got.Accounts.App.Secret)
	}
}

// ── IsComplete / HasSecret ──────────────────────────────────────

func TestIsComplete(t *testing.T) {
	full := AppConfig{}
	full.Accounts.App = AppCredentials{ID: "cli_a", Secret: PlainSecret("x"), Tenant: TenantFeishu}
	if !IsComplete(full) {
		t.Fatal("full cfg should be complete")
	}
	noSecret := full
	noSecret.Accounts.App.Secret = SecretInput{}
	if IsComplete(noSecret) {
		t.Fatal("missing secret should not be complete")
	}
	noTenant := full
	noTenant.Accounts.App.Tenant = ""
	if IsComplete(noTenant) {
		t.Fatal("missing tenant should not be complete")
	}
}

// ── 偏好 getter 默认值与 clamp ──────────────────────────────────

func TestGetMessageReplyMode(t *testing.T) {
	for _, raw := range []string{"card", "markdown", "text"} {
		p := AppPreferences{MessageReply: raw}
		if got := GetMessageReplyMode(prefsWith(p)); got != raw {
			t.Fatalf("got %q want %q", got, raw)
		}
	}
	if got := GetMessageReplyMode(AppConfig{}); got != "card" {
		t.Fatalf("default reply = %q want card", got)
	}
}

func TestGetShowToolCalls(t *testing.T) {
	if !GetShowToolCalls(AppConfig{}) {
		t.Fatal("default should be true")
	}
	p := AppPreferences{ShowToolCalls: boolPtr(false)}
	if GetShowToolCalls(prefsWith(p)) {
		t.Fatal("explicit false should be false")
	}
}

func TestGetModelDisplay_BoolAndEnum(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{nil, "running"},
		{"running", "running"},
		{"always", "always"},
		{"off", "off"},
		{"bogus", "running"},
		{true, "always"},
		{false, "off"},
	}
	for _, c := range cases {
		p := AppPreferences{ShowModel: c.in}
		got := GetModelDisplay(prefsWith(p))
		if got != c.want {
			t.Errorf("ShowModel=%v: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestGetMaxConcurrentRuns(t *testing.T) {
	if got := GetMaxConcurrentRuns(AppConfig{}); got != 10 {
		t.Fatalf("default = %d want 10", got)
	}
	p := AppPreferences{MaxConcurrentRuns: intPtr(0)}
	if got := GetMaxConcurrentRuns(prefsWith(p)); got != 10 {
		t.Fatalf("0/invalid -> 10, got %d", got)
	}
	p = AppPreferences{MaxConcurrentRuns: intPtr(999)}
	if got := GetMaxConcurrentRuns(prefsWith(p)); got != 50 {
		t.Fatalf("clamp to 50, got %d", got)
	}
	p = AppPreferences{MaxConcurrentRuns: intPtr(7)}
	if got := GetMaxConcurrentRuns(prefsWith(p)); got != 7 {
		t.Fatalf("valid 7 should pass, got %d", got)
	}
}

func TestGetRequireMentionInGroup(t *testing.T) {
	if !GetRequireMentionInGroup(AppConfig{}) {
		t.Fatal("default true")
	}
	p := AppPreferences{RequireMentionInGroup: boolPtr(false)}
	if GetRequireMentionInGroup(prefsWith(p)) {
		t.Fatal("explicit false")
	}
}

func TestGetPendingPolicy(t *testing.T) {
	if got := GetPendingPolicy(AppConfig{}); got != "steer" {
		t.Fatalf("default = %q want steer", got)
	}
	p := AppPreferences{PendingPolicy: "queue"}
	if got := GetPendingPolicy(prefsWith(p)); got != "queue" {
		t.Fatalf("queue lost: %q", got)
	}
}

func TestGetAgentStopGraceMs(t *testing.T) {
	if got := GetAgentStopGraceMs(AppConfig{}); got != 5000 {
		t.Fatalf("default = %d want 5000", got)
	}
	p := AppPreferences{AgentStopGraceMs: intPtr(10)}
	if got := GetAgentStopGraceMs(prefsWith(p)); got != 100 {
		t.Fatalf("clamp min 100, got %d", got)
	}
	p = AppPreferences{AgentStopGraceMs: intPtr(99999)}
	if got := GetAgentStopGraceMs(prefsWith(p)); got != 30000 {
		t.Fatalf("clamp max 30000, got %d", got)
	}
}

func TestGetRunIdleTimeoutMs(t *testing.T) {
	// 缺省 → 120000 on
	if ms, on := GetRunIdleTimeoutMs(AppConfig{}); !on || ms != 120000 {
		t.Fatalf("default = %d on=%v want 120000,true", ms, on)
	}
	// 0 → off
	p := AppPreferences{RunIdleTimeoutSeconds: intPtr(0)}
	if _, on := GetRunIdleTimeoutMs(prefsWith(p)); on {
		t.Fatal("0 should be off")
	}
	// clamp 下界 10s
	p = AppPreferences{RunIdleTimeoutSeconds: intPtr(3)}
	if ms, on := GetRunIdleTimeoutMs(prefsWith(p)); !on || ms != 10000 {
		t.Fatalf("clamp min 10s, got %d on=%v", ms, on)
	}
	// clamp 上界 3600s
	p = AppPreferences{RunIdleTimeoutSeconds: intPtr(99999)}
	if ms, on := GetRunIdleTimeoutMs(prefsWith(p)); !on || ms != 3600000 {
		t.Fatalf("clamp max 3600s, got %d on=%v", ms, on)
	}
}

// ── 访问控制 ────────────────────────────────────────────────────

func cfgWithAccess(a AppAccess) AppConfig {
	return prefsWith(AppPreferences{Access: &a})
}

func TestIsAdminAndOwner(t *testing.T) {
	cfg := cfgWithAccess(AppAccess{OwnerOpenID: "ou_owner", Admins: []string{"ou_adm"}})
	if !IsAdmin(cfg, "ou_owner") {
		t.Fatal("owner is admin")
	}
	if !IsAdmin(cfg, "ou_adm") {
		t.Fatal("admin is admin")
	}
	if IsAdmin(cfg, "ou_nope") {
		t.Fatal("stranger not admin")
	}
	if ResolveOwner(cfg) != "ou_owner" {
		t.Fatalf("owner = %q", ResolveOwner(cfg))
	}
}

func TestResolveOwner_FallbackToFirstAdmin(t *testing.T) {
	cfg := cfgWithAccess(AppAccess{Admins: []string{"ou_a", "ou_b"}})
	if ResolveOwner(cfg) != "ou_a" {
		t.Fatalf("fallback owner = %q want ou_a", ResolveOwner(cfg))
	}
}

func TestIsChatAllowed(t *testing.T) {
	if !IsChatAllowed(AppConfig{}, "oc_any") {
		t.Fatal("empty allowlist = all")
	}
	cfg := cfgWithAccess(AppAccess{AllowedChats: []string{"oc_1"}})
	if !IsChatAllowed(cfg, "oc_1") || IsChatAllowed(cfg, "oc_2") {
		t.Fatal("allowlist mismatch")
	}
}

func TestIsUserAllowedInProject(t *testing.T) {
	cfg := cfgWithAccess(AppAccess{OwnerOpenID: "ou_owner"})
	// admin 豁免
	if !IsUserAllowedInProject(cfg, []string{"ou_x"}, "ou_owner") {
		t.Fatal("admin exempt")
	}
	// 空 allowlist = all
	if !IsUserAllowedInProject(cfg, nil, "ou_any") {
		t.Fatal("empty allowlist = all")
	}
	// 非空 allowlist
	if !IsUserAllowedInProject(cfg, []string{"ou_x"}, "ou_x") {
		t.Fatal("in list")
	}
	if IsUserAllowedInProject(cfg, []string{"ou_x"}, "ou_y") {
		t.Fatal("not in list")
	}
}

// ── CliBridgePreferences 归一 ───────────────────────────────────

func cliCfg(cli *CliBridgePreferences) AppConfig {
	return prefsWith(AppPreferences{CliBridge: cli})
}

func TestGetCliBridgePreferences_AllDefaults(t *testing.T) {
	r := GetCliBridgePreferences(AppConfig{})
	if r.Enabled || r.Delivery != "away_only" || r.IncludeBridgeOwnedSessionsForDebugging {
		t.Fatal("default shape wrong")
	}
	if !r.Agents.Claude || !r.Agents.Codex || r.NotifyScope != "all" {
		t.Fatal("agents/notifyScope default wrong")
	}
	if !r.KeepAwake.Enabled || !r.Approval.Enabled || r.Approval.TimeoutSeconds != 86400 {
		t.Fatal("keepAwake/approval default wrong")
	}
	if !r.TaskCompletion.Enabled || !r.TaskCompletion.ReplyEnabled || r.TaskCompletion.ReplyTimeoutSeconds != 1800 {
		t.Fatal("taskCompletion default wrong")
	}
	if !r.AllowCache.Enabled || r.AllowCache.Scope != "session" {
		t.Fatal("allowCache default wrong")
	}
	if !r.CompletionSync.Enabled {
		t.Fatal("completionSync default wrong")
	}
	if !r.Presence.Enabled || r.Presence.Platform != "auto" || r.Presence.IdleThresholdSeconds != 120 {
		t.Fatal("presence default wrong")
	}
}

func TestGetCliBridgePreferences_PartialOverride(t *testing.T) {
	cli := &CliBridgePreferences{
		Enabled:     boolPtr(true),
		NotifyScope: "bound_projects",
		Approval:    &CliBridgeApproval{TimeoutSeconds: intPtr(100)},
		Agents:      &CliBridgeAgentToggles{Claude: boolPtr(false)},
	}
	r := GetCliBridgePreferences(cliCfg(cli))
	if !r.Enabled || r.NotifyScope != "bound_projects" || r.Approval.TimeoutSeconds != 100 {
		t.Fatalf("override lost: %+v", r)
	}
	if r.Agents.Claude != false || r.Agents.Codex != true {
		t.Fatalf("per-agent toggle wrong: %+v", r.Agents)
	}
}

func TestGetCliBridgePreferences_ClampNegAndMax(t *testing.T) {
	// 负数 → fallback
	cli := &CliBridgePreferences{Approval: &CliBridgeApproval{TimeoutSeconds: intPtr(-5)}}
	if r := GetCliBridgePreferences(cliCfg(cli)); r.Approval.TimeoutSeconds != 86400 {
		t.Fatalf("neg should fallback 86400, got %d", r.Approval.TimeoutSeconds)
	}
	// 超上限 → clamp
	cli = &CliBridgePreferences{Presence: &CliBridgePresence{IdleThresholdSeconds: intPtr(99999)}}
	if r := GetCliBridgePreferences(cliCfg(cli)); r.Presence.IdleThresholdSeconds != 3600 {
		t.Fatalf("over-max should clamp 3600, got %d", r.Presence.IdleThresholdSeconds)
	}
	// 下限 clamp
	cli = &CliBridgePreferences{Presence: &CliBridgePresence{IdleThresholdSeconds: intPtr(3)}}
	if r := GetCliBridgePreferences(cliCfg(cli)); r.Presence.IdleThresholdSeconds != 10 {
		t.Fatalf("under-min should clamp 10, got %d", r.Presence.IdleThresholdSeconds)
	}
}

func TestGetCliBridgePreferences_DeliveryAlwaysPreserved(t *testing.T) {
	// delivery=always 是受支持的合法模式（用户希望随时在手机审批，忽略 presence）。
	cli := &CliBridgePreferences{Delivery: "always"}
	if r := GetCliBridgePreferences(cliCfg(cli)); r.Delivery != "always" {
		t.Fatalf("delivery=always 应被保留，got %q", r.Delivery)
	}
	// 缺省（不写）仍为 away_only。
	if r := GetCliBridgePreferences(cliCfg(&CliBridgePreferences{})); r.Delivery != "away_only" {
		t.Fatalf("缺省 delivery 应为 away_only, got %q", r.Delivery)
	}
}

func TestGetCliBridgePreferences_UnknownDeliveryFallsBack(t *testing.T) {
	cli := &CliBridgePreferences{Delivery: "bogus"}
	if r := GetCliBridgePreferences(cliCfg(cli)); r.Delivery != "away_only" {
		t.Fatalf("unknown delivery should fallback away_only, got %q", r.Delivery)
	}
}

func TestGetCliBridgePreferences_UnknownNotifyScopeFallsBack(t *testing.T) {
	cli := &CliBridgePreferences{NotifyScope: "bogus"}
	if r := GetCliBridgePreferences(cliCfg(cli)); r.NotifyScope != "all" {
		t.Fatalf("unknown notifyScope should fallback all, got %q", r.NotifyScope)
	}
}

func TestCanEnableCliBridge_NeedsOwner(t *testing.T) {
	if CanEnableCliBridge(AppConfig{}) {
		t.Fatal("no owner -> cannot enable")
	}
	cfg := cfgWithAccess(AppAccess{OwnerOpenID: "ou_owner"})
	if !CanEnableCliBridge(cfg) {
		t.Fatal("with owner -> can enable")
	}
	id, ok := ResolveCliBridgeTarget(cfg)
	if !ok || id != "ou_owner" {
		t.Fatalf("target = %q ok=%v", id, ok)
	}
}
