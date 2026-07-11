package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// 小工具（避免在每个测试文件重复 import json）。
func marshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func jsonUnmarshal(t *testing.T, b []byte, v any) error {
	t.Helper()
	return json.Unmarshal(b, v)
}
func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

func TestRequiredScopes_Contents(t *testing.T) {
	want := []string{
		"im:message.group_at_msg:readonly",
		"im:message.group_msg",
		"im:message:send_as_bot",
		"im:chat.managers:write_only",
		"cardkit:card:write",
	}
	for _, w := range want {
		if !sliceHas(REQUIRED_SCOPES, w) {
			t.Errorf("REQUIRED_SCOPES missing %q", w)
		}
	}
}

func TestDiscoveryScopes_InGrantNotInRequired(t *testing.T) {
	for _, s := range DISCOVERY_SCOPES {
		if !sliceHas(GRANT_SCOPES, s) {
			t.Errorf("DISCOVERY scope %q should be in GRANT", s)
		}
		if sliceHas(REQUIRED_SCOPES, s) {
			t.Errorf("DISCOVERY scope %q must NOT be in REQUIRED (gates install)", s)
		}
	}
}

func TestGrantScopes_IsUnion(t *testing.T) {
	wantCount := len(REQUIRED_SCOPES) + len(COMMENT_SCOPES) + len(JOIN_GROUP_SCOPES) + len(CONTACT_SCOPES) + len(APP_VERSION_SCOPES) + len(DISCOVERY_SCOPES)
	if len(GRANT_SCOPES) != wantCount {
		t.Fatalf("GRANT_SCOPES len = %d, want %d (union of all groups)", len(GRANT_SCOPES), wantCount)
	}
}

func TestLabelScope(t *testing.T) {
	if got := LabelScope("im:resource"); !containsStr(got, "图片") || !containsStr(got, "im:resource") {
		t.Fatalf("known scope label wrong: %q", got)
	}
	if got := LabelScope("im:unknown:foo"); got != "im:unknown:foo" {
		t.Fatalf("unknown scope should fall back to raw token, got %q", got)
	}
}

func TestBuildScopeGrantUrl(t *testing.T) {
	u := BuildScopeGrantUrl("cli_abc", TenantFeishu)
	if !strings.HasPrefix(u, "https://open.feishu.cn/app/cli_abc/auth?q=") {
		t.Fatalf("feishu url shape wrong: %q", u)
	}
	// scope 里的冒号必须被编码为 %3A（防注入）；URL 里只允许 https:// 的冒号，
	// 不允许原始 scope token 的冒号原样出现（编码后应是 im%3Amessage）。
	if strings.Contains(u, "im:message.group_at_msg") {
		t.Fatalf("scope token not URL-encoded in url: %q", u)
	}
	u2 := BuildScopeGrantUrl("cli_abc", TenantLark)
	if !strings.HasPrefix(u2, "https://open.larksuite.com/app/cli_abc/auth?q=") {
		t.Fatalf("lark host wrong: %q", u2)
	}
}

func TestBuildEventConfigUrl(t *testing.T) {
	u := BuildEventConfigUrl("cli_x", TenantFeishu)
	if u != "https://open.feishu.cn/app/cli_x/event" {
		t.Fatalf("event url = %q", u)
	}
}

func sliceHas(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
