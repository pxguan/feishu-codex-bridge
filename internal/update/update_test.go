package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

const sampleRelease = `{
  "tag_name": "v1.2.0",
  "name": "v1.2.0",
  "html_url": "https://github.com/modelzen/feishu-codex-bridge/releases/tag/v1.2.0",
  "body": "bug fixes",
  "assets": [
    {"name": "feishu-codex-bridge-darwin-arm64", "browser_download_url": "https://example.com/darwin-arm64", "size": 123},
    {"name": "feishu-codex-bridge-linux-amd64", "browser_download_url": "https://example.com/linux-amd64", "size": 456},
    {"name": "feishu-codex-bridge-windows-x86_64.exe", "browser_download_url": "https://example.com/win-x64", "size": 789}
  ]
}`

// proxyClient 返回一个把 api.github.com 请求重写到测试服务器的 http.Client，
// 避免测试真的打外部网络。
func proxyClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	base := srv.Client().Transport
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "api.github.com" {
				req.URL.Scheme = "http"
				req.URL.Host = srv.Listener.Addr().String()
			}
			return base.RoundTrip(req)
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/x/y/releases/latest" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(sampleRelease))
			return
		}
		if r.URL.Path == "/download/darwin-arm64" {
			_, _ = w.Write([]byte("fake-binary-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	return srv, proxyClient(t, srv)
}

func TestLatest_Parse(t *testing.T) {
	srv, client := newTestServer(t)
	defer srv.Close()
	rel, err := Latest(context.Background(), "x/y", client)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.0" {
		t.Fatalf("tag = %s", rel.TagName)
	}
	if len(rel.Assets) != 3 {
		t.Fatalf("assets = %d", len(rel.Assets))
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "v1.2.0", 0},
		{"v1.1.0", "v1.2.0", -1},
		{"v1.2.0", "v1.1.0", 1},
		{"v1.2.0", "1.2.0", 0},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.2.0-rc1", "v1.2.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersion(c.a, c.b); got != c.want {
			t.Fatalf("CompareVersion(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetForPlatform(t *testing.T) {
	var rel Release
	if err := json.Unmarshal([]byte(sampleRelease), &rel); err != nil {
		t.Fatal(err)
	}
	a, ok := AssetForPlatform(&rel, "darwin", "arm64")
	if !ok || a.BrowserDownloadURL != "https://example.com/darwin-arm64" {
		t.Fatalf("darwin/arm64 mismatch: %+v ok=%v", a, ok)
	}
	a, ok = AssetForPlatform(&rel, "windows", "amd64")
	if !ok || a.BrowserDownloadURL != "https://example.com/win-x64" {
		t.Fatalf("windows/amd64 mismatch: %+v ok=%v", a, ok)
	}
	if _, ok := AssetForPlatform(&rel, "plan9", "arm64"); ok {
		t.Fatal("plan9 should not match")
	}
}

func TestDownloadToTemp(t *testing.T) {
	srv, client := newTestServer(t)
	defer srv.Close()
	a := Asset{Name: "x", BrowserDownloadURL: srv.URL + "/download/darwin-arm64"}
	path, err := DownloadToTemp(context.Background(), a, client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path) }()
}
