package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// githubRewriteTransport 把对 api.github.com 的请求重写到 mock server（update.Latest 写死 GitHub 域名）。
type githubRewriteTransport struct {
	target *httptest.Server
}

func (rt githubRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, "api.github.com") {
		u := *req.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(rt.target.URL, "http://")
		r2 := req.Clone(req.Context())
		r2.URL = &u
		return http.DefaultClient.Do(r2)
	}
	return http.DefaultClient.Do(req)
}

// newMockGitHub 起一个 mock GitHub，响应 releases/latest；返回 server 与命中计数指针。
func newMockGitHub(t *testing.T, status int, body string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/latest") {
			hits++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// mockVersionClient 返回把 GitHub 请求重写到 mock 的客户端。
func mockVersionClient(gh *httptest.Server) *http.Client {
	return &http.Client{Transport: githubRewriteTransport{target: gh}}
}

func TestHandleVersionCheck_AvailableAndCached(t *testing.T) {
	gh, hits := newMockGitHub(t, http.StatusOK,
		`{"tag_name":"v9.9.9","html_url":"https://github.com/modelzen/feishu-codex-bridge/releases/tag/v9.9.9"}`)

	s := &Server{Token: "tok", VersionClient: mockVersionClient(gh)}

	// 首次：冷缓存 → 查 GitHub（hits=1），cached=false，available=true。
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version?token=tok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var v1 struct {
		Current  string `json:"current"`
		Latest   string `json:"latest"`
		URL      string `json:"url"`
		Available bool  `json:"available"`
		Cached   bool   `json:"cached"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v1.Latest != "v9.9.9" || !v1.Available || v1.Cached {
		t.Fatalf("first call unexpected: %+v", v1)
	}
	if *hits != 1 {
		t.Fatalf("expected 1 github hit, got %d", *hits)
	}

	// 二次：TTL 内命中缓存 → 不再查 GitHub（hits 仍为 1），cached=true。
	rec2 := httptest.NewRecorder()
	s.handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/version?token=tok", nil))
	var v2 struct {
		Available bool `json:"available"`
		Cached    bool `json:"cached"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &v2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !v2.Cached || !v2.Available {
		t.Fatalf("second call should be cached+available: %+v", v2)
	}
	if *hits != 1 {
		t.Fatalf("cache should prevent 2nd github hit, got %d", *hits)
	}
}

func TestHandleVersionCheck_GitHubError(t *testing.T) {
	gh, _ := newMockGitHub(t, http.StatusInternalServerError, `{"message":"boom"}`)
	s := &Server{Token: "tok", VersionClient: mockVersionClient(gh)}

	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version?token=tok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var v struct {
		Available bool   `json:"available"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.Available || v.Error == "" {
		t.Fatalf("error case should report available=false + error: %+v", v)
	}
}

func TestStatusReflectsCachedVersion(t *testing.T) {
	gh, _ := newMockGitHub(t, http.StatusOK,
		`{"tag_name":"v9.9.9","html_url":"https://github.com/modelzen/feishu-codex-bridge/releases/tag/v9.9.9"}`)
	s := &Server{Token: "tok", VersionClient: mockVersionClient(gh)}

	// 先触发一次版本检查填充缓存。
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version?token=tok", nil))

	// /api/status 应反映缓存中的 latest_version / update_available。
	srec := httptest.NewRecorder()
	s.handler().ServeHTTP(srec, httptest.NewRequest(http.MethodGet, "/api/status?token=tok", nil))
	var stt struct {
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable bool   `json:"update_available"`
	}
	if err := json.Unmarshal(srec.Body.Bytes(), &stt); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if stt.LatestVersion != "v9.9.9" || !stt.UpdateAvailable {
		t.Fatalf("status should reflect cached version: %+v", stt)
	}
}
