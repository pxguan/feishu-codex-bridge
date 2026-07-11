// server.go —— 本机 Web 控制台（Phase 2）。
//
// 仅监听 127.0.0.1，且需 token 鉴权（token 存于 config.WebTokenFile，首次启动生成）。
// 提供：/healthz（无鉴权，给探针用）、/api/status（JSON）、/（极简状态页）。

package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
	"github.com/modelzen/feishu-codex-bridge/internal/service"
	"github.com/modelzen/feishu-codex-bridge/internal/update"
)

// Status 是 /api/status 的返回体。
type Status struct {
	Version       string   `json:"version"`
	UptimeSeconds int64    `json:"uptime_seconds"`
	Backends      []string `json:"backends"`
	Bots          int      `json:"bots"`
	StartedAt     int64    `json:"started_at"`
	// 版本检查（缓存驱动；仅反映最近一次 /api/version 探测结果，不在此触发网络）。
	LatestVersion  string `json:"latest_version,omitempty"`
	UpdateAvailable bool  `json:"update_available,omitempty"`
}

// Deps 是 Web 控制台「写」操作所需的外部依赖（由 CLI 在启动 daemon 时注入）。
// 未注入（如单独 `web` 命令）时，写端点返回 501。
type Deps struct {
	Projects   *project.Store
	Reconnect  func(ctx context.Context) error
	LogFile    string
	SvcInstall   func(ctx context.Context) error            // 注册守护进程为系统服务
	SvcUninstall func(ctx context.Context) error            // 注销系统服务
	SvcStatus    func(ctx context.Context) (service.Status, error) // 查询安装/运行态
	// SetCompletionReminder 由 daemon 注入：Web 控制台设置「任务结束提醒」策略时调用（appId 已在路径中）。
	SetCompletionReminder func(mode string, longTaskMinutes int) error
}

// Server 持有 Web 控制台配置。
type Server struct {
	ListenAddr string // 默认 127.0.0.1:18789
	Token      string
	Version    string
	StartedAt  time.Time
	Deps       *Deps // 写操作依赖（nil=只读）
	// VersionClient 用于版本检查的 HTTP 客户端（可注入测试用；nil=默认）。
	VersionClient *http.Client
	vcache        versionCheckCache // 版本检查短时缓存（避免轮询频繁打 GitHub）
}

// New 构造 Server（ListenAddr 默认 127.0.0.1:18789）。
func New() *Server {
	return &Server{
		ListenAddr: "127.0.0.1:18789",
		Version:    core.Version(),
		StartedAt:  time.Now(),
	}
}

// loadOrCreateToken 读取 WebTokenFile；缺失则生成 32 字节 hex 并写回（0600）。
func loadOrCreateToken() (string, error) {
	p := config.WebTokenFile()
	b, err := os.ReadFile(p)
	if err == nil {
		// trim 掉可能的尾部换行（旧版本写入带 \n，或手动编辑引入空白），
		// 否则内存 token 含换行会与该端点从 header/query 取的 token 永不匹配 → 401。
		tok := strings.TrimSpace(string(b))
		if len(tok) >= 16 {
			return tok, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// computeStatus 汇总探测结果。
func (s *Server) computeStatus() Status {
	backends := []string{}
	for _, rt := range agent.DetectAgents() {
		backends = append(backends, string(rt.ID))
	}
	bots := 0
	if reg, err := config.LoadBots(); err == nil {
		bots = len(reg.Bots)
	}
	latest, avail := s.cachedVersionInfo()
	return Status{
		Version:         s.Version,
		UptimeSeconds:   int64(time.Since(s.StartedAt).Seconds()),
		Backends:        backends,
		Bots:            bots,
		StartedAt:       s.StartedAt.UnixMilli(),
		LatestVersion:   latest,
		UpdateAvailable: avail,
	}
}

// cachedVersionInfo 仅读取版本检查缓存（不触发网络），返回最近一次探测的最新版本与是否可更新。
func (s *Server) cachedVersionInfo() (latest string, available bool) {
	s.vcache.mu.Lock()
	defer s.vcache.mu.Unlock()
	if !s.vcache.has {
		return "", false
	}
	latest = s.vcache.latest
	available = s.vcache.latest != "" && update.CompareVersion(core.Version(), s.vcache.latest) < 0
	return latest, available
}

// auth 中间件：校验 Bearer token 或 ?token=。
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("token")
		if tok == "" {
			if ah := r.Header.Get("Authorization"); len(ah) > 7 && ah[:7] == "Bearer " {
				tok = ah[7:]
			}
		}
		if tok != s.Token {
			http.Error(w, "401 unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handler 构建路由。
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/status", s.auth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.computeStatus())
	}))
	// ── 写端点（需 Deps）──
	mux.HandleFunc("/api/projects", s.auth(s.handleListProjects))
	mux.HandleFunc("/api/projects/{name}/settings", s.auth(s.handleProjectSettings))
	mux.HandleFunc("/api/logs", s.auth(s.handleLogs))
	mux.HandleFunc("/api/bot/reconnect", s.auth(s.handleBotReconnect))
	mux.HandleFunc("/api/bot/register", s.auth(s.handleBotRegister))
	mux.HandleFunc("/api/bot/service", s.auth(s.handleBotService))
	mux.HandleFunc("/api/bot/install", s.auth(s.handleBotInstall))
	mux.HandleFunc("/api/bot/uninstall", s.auth(s.handlerBotUninstall))
	mux.HandleFunc("/api/update/status", s.auth(s.handleUpdateStatus))
	mux.HandleFunc("/api/version", s.auth(s.handleVersionCheck))
	mux.HandleFunc("/api/bots/{appId}/completion-reminder", s.auth(s.handleBotCompletionReminder))
	mux.HandleFunc("/", s.auth(func(w http.ResponseWriter, r *http.Request) {
		s.renderHome(w)
	}))
	return mux
}

// requireDeps 检查写依赖是否就绪；未就绪返回 501 + 错误 JSON。
func (s *Server) requireDeps(w http.ResponseWriter) *Deps {
	if s.Deps == nil {
		s.writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "Web 控制台以只读模式运行（写操作需 daemon 注入依赖）"})
		return nil
	}
	return s.Deps
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handleListProjects GET /api/projects —— 列出全部项目（含设置）。
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	list, err := deps.Projects.List()
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, list)
}

// handleProjectSettings POST /api/projects/{name}/settings —— 更新项目设置。
func (s *Server) handleProjectSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	name := r.PathValue("name")
	var body struct {
		NoMention     *bool   `json:"noMention"`
		AutoCompact   *bool   `json:"autoCompact"`
		DefaultModel  string  `json:"defaultModel"`
		DefaultEffort string  `json:"defaultEffort"`
		SourceURL     string  `json:"sourceUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	err := deps.Projects.Update(name, func(p *project.Project) {
		if body.NoMention != nil {
			p.NoMention = body.NoMention
		}
		if body.AutoCompact != nil {
			p.AutoCompact = body.AutoCompact
		}
		if body.DefaultModel != "" {
			p.DefaultModel = body.DefaultModel
		}
		if body.DefaultEffort != "" {
			p.DefaultEffort = agent.ReasoningEffort(body.DefaultEffort)
		}
		if body.SourceURL != "" {
			p.SourceURL = body.SourceURL
		}
	})
	if err != nil {
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"ok": "updated", "name": name})
}

// handleLogs GET /api/logs?lines=N[&follow=1] —— 尾随 daemon 日志（follow=1 走 SSE）。
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lines = n
		}
	}
	path := deps.LogFile
	if path == "" {
		path = config.ServiceLog()
	}
	f, err := os.Open(path)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "open log: " + err.Error()})
		return
	}
	defer f.Close()

	if r.URL.Query().Get("follow") == "1" {
		flusher, ok := w.(http.Flusher)
		if !ok {
			s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		for _, line := range tailLines(f, lines) {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		flusher.Flush()
		lastOff, _ := f.Seek(0, io.SeekEnd)
		ticker := time.NewTicker(800 * time.Millisecond)
		defer ticker.Stop()
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				off, _ := f.Seek(0, io.SeekCurrent)
				if off < lastOff {
					lastOff, _ = f.Seek(0, io.SeekEnd) // 文件被轮转
				}
				var sb bytes.Buffer
				buf := make([]byte, 4096)
				for {
					n, rerr := f.Read(buf)
					if n > 0 {
						sb.Write(buf[:n])
					}
					if rerr != nil {
						break
					}
				}
				if sb.Len() > 0 {
					for _, line := range strings.Split(strings.TrimRight(sb.String(), "\n"), "\n") {
						if line != "" {
							fmt.Fprintf(w, "data: %s\n\n", line)
						}
					}
					flusher.Flush()
				}
				lastOff, _ = f.Seek(0, io.SeekCurrent)
			}
		}
	}

	// 非 follow：直接返回最近 N 行文本。
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, line := range tailLines(f, lines) {
		fmt.Fprintln(w, line)
	}
}

// tailLines 读文件最后 n 行（有界读取末尾最多 1MB，避免大日志全量载入）。
func tailLines(f *os.File, n int) []string {
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	const maxRead = 1 << 20 // 1MB
	start := size - maxRead
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	// 从中间截断时，丢掉首个换行之前的半行。
	if start > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// handleBotReconnect POST /api/bot/reconnect —— 重连飞书长连接。
func (s *Server) handleBotReconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	if deps.Reconnect == nil {
		s.writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "reconnect 未注入"})
		return
	}
	if err := deps.Reconnect(r.Context()); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"ok": "reconnected"})
}

// handleBotRegister POST /api/bot/register —— 注册新 bot（app_id/app_secret）。
func (s *Server) handleBotRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	var req struct {
		AppID     string `json:"appId"`
		AppSecret string `json:"appSecret"`
		Name      string `json:"name"`
		Tenant    string `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if req.AppID == "" || req.AppSecret == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "appId 与 appSecret 必填"})
		return
	}
	// 密钥存 keystore（明文绝不落 config.json），bot 条目存 bots.json。
	ks := config.NewKeystore(config.SecretsFile(), config.KeystoreSaltFile())
	if err := ks.Set(config.SecretKeyForApp(req.AppID), req.AppSecret); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save secret: " + err.Error()})
		return
	}
	name := req.Name
	if name == "" {
		name = req.AppID
	}
	tenant := config.TenantBrand(req.Tenant)
	if tenant == "" {
		tenant = config.TenantFeishu
	}
	if _, err := config.AddBot(config.BotEntry{Name: name, AppID: req.AppID, Tenant: tenant}); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "add bot: " + err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"ok": "registered", "appId": req.AppID})
}

// handleBotService GET /api/bot/service —— 查询守护进程系统服务安装/运行态。
func (s *Server) handleBotService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	if deps.SvcStatus == nil {
		s.writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "service 状态查询未注入"})
		return
	}
	st, err := deps.SvcStatus(r.Context())
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, st)
}

// handleBotInstall POST /api/bot/install —— 把守护进程注册为系统服务并启动。
func (s *Server) handleBotInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	if deps.SvcInstall == nil {
		s.writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "install 未注入"})
		return
	}
	if err := deps.SvcInstall(r.Context()); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"ok": "installed"})
}

// handlerBotUninstall POST /api/bot/uninstall —— 注销系统服务并停止守护进程。
func (s *Server) handlerBotUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	if deps.SvcUninstall == nil {
		s.writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "uninstall 未注入"})
		return
	}
	if err := deps.SvcUninstall(r.Context()); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"ok": "uninstalled"})
}

// handleUpdateStatus GET /api/update/status —— 读取最近一次升级进度/结果（供网页端轮询；失败不再静默）。
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	st := update.ReadUpdateStatus()
	if st == nil {
		s.writeJSON(w, http.StatusOK, map[string]any{"phase": "", "available": false})
		return
	}
	s.writeJSON(w, http.StatusOK, st)
}

// versionCheckTTL 版本检查缓存有效期（对齐上游 2499db6「版本检查改缓存驱动」：避免网页轮询频繁打 GitHub）。
const versionCheckTTL = 5 * time.Minute

// versionCheckCache 最近一次 GitHub release 查询结果的短时缓存。
type versionCheckCache struct {
	mu     sync.Mutex
	has    bool
	latest string
	url    string
	at     time.Time
}

// latestVersion 缓存驱动地查询最新 release 并与当前版本比较。
// client 可为 nil（用默认）。返回 latest 标签、release URL、是否可更新、是否命中缓存、错误。
func (s *Server) latestVersion(ctx context.Context, client *http.Client) (latest, url string, available, cached bool, err error) {
	s.vcache.mu.Lock()
	if s.vcache.has && time.Since(s.vcache.at) < versionCheckTTL {
		latest := s.vcache.latest
		url := s.vcache.url
		s.vcache.mu.Unlock()
		avail := latest != "" && update.CompareVersion(core.Version(), latest) < 0
		return latest, url, avail, true, nil
	}
	s.vcache.mu.Unlock()

	rel, e := update.Latest(ctx, update.DefaultRepo, client)
	if e != nil {
		// 查询失败：保留旧缓存（若有），不刷新。
		s.vcache.mu.Lock()
		has := s.vcache.has
		s.vcache.mu.Unlock()
		return "", "", false, has, e
	}
	latest = rel.TagName
	url = rel.HTMLURL
	s.vcache.mu.Lock()
	s.vcache.has = true
	s.vcache.latest = latest
	s.vcache.url = url
	s.vcache.at = time.Now()
	s.vcache.mu.Unlock()
	available = update.CompareVersion(core.Version(), latest) < 0
	return latest, url, available, false, nil
}

// handleVersionCheck GET /api/version —— 缓存驱动地返回当前/最新版本与是否可更新（对齐上游 2499db6）。
func (s *Server) handleVersionCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	latest, url, available, cached, err := s.latestVersion(r.Context(), s.VersionClient)
	if err != nil {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"current":  core.Version(),
			"latest":   latest,
			"available": false,
			"cached":   cached,
			"error":    err.Error(),
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"current":  core.Version(),
		"latest":   latest,
		"url":      url,
		"available": available,
		"cached":   cached,
	})
}
// appId 仅用于路由标识（配置落盘由注入的回调按当前激活 bot 写）；写依赖未注入返回 501。
func (s *Server) handleBotCompletionReminder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	deps := s.requireDeps(w)
	if deps == nil {
		return
	}
	if deps.SetCompletionReminder == nil {
		s.writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "completion-reminder 未注入"})
		return
	}
	appID := r.PathValue("appId")
	var body struct {
		Mode            string `json:"mode"`
		LongTaskMinutes *int   `json:"longTaskMinutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if body.Mode == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode 必填"})
		return
	}
	ltm := 0
	if body.LongTaskMinutes != nil {
		ltm = *body.LongTaskMinutes
	}
	if err := deps.SetCompletionReminder(body.Mode, ltm); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"ok": "updated", "appId": appID})
}

// renderHome 输出极简状态页。
func (s *Server) renderHome(w http.ResponseWriter) {
	st := s.computeStatus()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">
<title>feishu-codex-bridge</title>
<style>body{font-family:ui-monospace,Menlo,monospace;background:#0f1115;color:#e6e6e6;padding:2rem}code{color:#7ee787}dt{color:#8b949e}</style>
</head><body>
<h1>feishu-codex-bridge</h1>
<dl>
<dt>version</dt><dd><code>%s</code></dd>
<dt>uptime</dt><dd><code>%d s</code></dd>
<dt>backends</dt><dd><code>%v</code></dd>
<dt>bots</dt><dd><code>%d</code></dd>
</dl>
<p>本控制台仅监听 127.0.0.1，需 token 访问。</p>
</body></html>`, st.Version, st.UptimeSeconds, st.Backends, st.Bots)
}

// EnsureToken 读取或生成 token 并写入 s.Token（供 CLI 启动前打印）。
func (s *Server) EnsureToken() (string, error) {
	tok, err := loadOrCreateToken()
	if err != nil {
		return "", err
	}
	s.Token = tok
	return tok, nil
}

// Run 启动 HTTP server，直到 ctx 取消。
func (s *Server) Run(ctx context.Context) error {
	tok, err := loadOrCreateToken()
	if err != nil {
		return fmt.Errorf("生成 web token 失败：%w", err)
	}
	s.Token = tok
	srv := &http.Server{Addr: s.ListenAddr, Handler: s.handler()}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
