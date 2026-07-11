package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

func writeJSON(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestValidateAppCredentials_OK(t *testing.T) {
	srv := mockFeishuAuthServer(true)
	defer srv.Close()
	res := validateAt(context.Background(), &http.Client{}, srv.URL, "cli_a", "secret")
	if !res.Ok {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.BotName != "MyBot" || res.BotOpenID != "ou_x" {
		t.Fatalf("bot info wrong: %+v", res)
	}
	// granted 只含 group_at_msg + chat:create → 其余 required 缺失（非 nil、非空）。
	if res.MissingScopes == nil {
		t.Fatal("MissingScopes should be computed (non-nil) when scopes fetchable")
	}
	if !containsStr(res.MissingScopes, "im:message.group_msg") {
		t.Fatalf("im:message.group_msg should be missing: %v", res.MissingScopes)
	}
}

func TestValidateAppCredentials_BadSecret(t *testing.T) {
	srv := mockFeishuAuthServer(false)
	defer srv.Close()
	res := validateAt(context.Background(), &http.Client{}, srv.URL, "cli_a", "wrong")
	if res.Ok {
		t.Fatal("bad secret should not be ok")
	}
	if !strings.Contains(res.Reason, "99991663") {
		t.Fatalf("reason should mention error code: %q", res.Reason)
	}
}

func TestValidateAppCredentials_NetworkError(t *testing.T) {
	srv := mockFeishuAuthServer(true)
	srv.Close() // 立即关闭 → 连接失败
	res := validateAt(context.Background(), &http.Client{}, srv.URL, "cli_a", "secret")
	if res.Ok {
		t.Fatal("dead server should not be ok")
	}
	if !strings.Contains(res.Reason, "网络错误") {
		t.Fatalf("reason should be network error: %q", res.Reason)
	}
}

func TestValidateAppCredentials_AllScopesGranted_EmptySlice(t *testing.T) {
	// 授予全部 required + join_group → MissingScopes 是空切片（len 0），区别于「无法获取」的 nil。
	granted := append(append([]string{}, config.REQUIRED_SCOPES...), config.JOIN_GROUP_SCOPES...)
	scopesPayload := make([]map[string]any, 0, len(granted))
	for _, s := range granted {
		scopesPayload = append(scopesPayload, map[string]any{"scope_name": s, "grant_status": 1})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			writeJSON(w, map[string]any{"code": 0, "tenant_access_token": "tok"})
		case strings.HasSuffix(r.URL.Path, "/bot/v3/info"):
			writeJSON(w, map[string]any{"code": 0, "bot": map[string]any{"app_name": "B", "open_id": "o"}})
		case strings.HasSuffix(r.URL.Path, "/application/v6/scopes"):
			writeJSON(w, map[string]any{"data": map[string]any{"scopes": scopesPayload}})
		}
	}))
	defer srv.Close()
	res := validateAt(context.Background(), &http.Client{}, srv.URL, "cli_a", "secret")
	if !res.Ok {
		t.Fatalf("expected ok: %+v", res)
	}
	if res.MissingScopes == nil || len(res.MissingScopes) != 0 {
		t.Fatalf("all required granted → empty (non-nil) slice, got %v", res.MissingScopes)
	}
}

func mockFeishuAuthServer(tokenOK bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			if tokenOK {
				writeJSON(w, map[string]any{"code": 0, "tenant_access_token": "tok"})
			} else {
				writeJSON(w, map[string]any{"code": 99991663, "msg": "bad secret"})
			}
		case strings.HasSuffix(r.URL.Path, "/bot/v3/info"):
			writeJSON(w, map[string]any{"code": 0, "bot": map[string]any{"app_name": "MyBot", "open_id": "ou_x"}})
		case strings.HasSuffix(r.URL.Path, "/application/v6/scopes"):
			writeJSON(w, map[string]any{"data": map[string]any{"scopes": []map[string]any{
				{"scope_name": "im:message.group_at_msg:readonly", "grant_status": 1},
				{"scope_name": "im:chat:create", "grant_status": 1},
			}}})
		}
	}))
}
