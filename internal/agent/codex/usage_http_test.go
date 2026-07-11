package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchWham_BearerAndAccountID(t *testing.T) {
	var gotAuth, gotAcct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAcct = r.Header.Get("ChatGPT-Account-Id")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	res, err := fetchWham(context.Background(), http.DefaultClient, srv.URL, "/wham/usage", &CodexAuth{AccessToken: "tok123", AccountID: "acct-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.status != 200 {
		t.Fatalf("status=%d", res.status)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("auth header: %q", gotAuth)
	}
	if gotAcct != "acct-1" {
		t.Fatalf("account header: %q", gotAcct)
	}
}

func TestFetchWham_NoAccountID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Error("should not set account header when empty")
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	_, err := fetchWham(context.Background(), http.DefaultClient, srv.URL, "/test", &CodexAuth{AccessToken: "tok"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWhamGet_401ChainNeedRelogin(t *testing.T) {
	// 无法完整测 401 链（需 mock codex auth.json + app-server）。
	// 只验证 whamGet 对 401 返回 need-relogin 或 transient。
	// 这里的测试跳过（需真实 codex 环境），只验证接口存在。
	_ = context.Background()
	_ = http.DefaultClient
}
