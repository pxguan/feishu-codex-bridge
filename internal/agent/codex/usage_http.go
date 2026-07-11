package codex

// usage_http.go —— Codex 用量 HTTP 拉数层（对齐 TS codex-appserver/usage 的 HTTP 部分）。
// 直连 ChatGPT 后端 HTTP（wham/usage + wham/profiles/me）+ JWT exp 临期刷新 + 401 兜底链。
// 依赖 usage 纯映射（已 port）+ client-pool UtilityRequest（已 port）+ ReadCodexAuth/JwtExpMs/ChatgptBaseUrl（已 port）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

const (
	usageHTTPTimeout = 15 * time.Second
	usageRefreshTO   = 20 * time.Second
	usageExpSkewMS   = 60_000
	usageCacheUsage  = 30 * time.Second
	usageCacheProf   = 5 * time.Minute
)

// whamResult HTTP 结果。
type whamResult struct {
	status int
	body   []byte // 仅 2xx 时有
}

// fetchWham GET {base}{path}（Bearer + ChatGPT-Account-Id + codex-cli UA）。
func fetchWham(ctx context.Context, hc *http.Client, base, path string, auth *CodexAuth) (whamResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return whamResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	if auth.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.AccountID)
	}
	req.Header.Set("User-Agent", "codex-cli")
	resp, err := hc.Do(req)
	if err != nil {
		return whamResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return whamResult{status: resp.StatusCode, body: body}, nil
}

// refreshOutcome 刷新结果。
type refreshOutcome int

const (
	refreshNone          refreshOutcome = 0
	refreshSuccess       refreshOutcome = 1
	refreshPermanentFail refreshOutcome = 2
)

// refreshViaAppServer 经 app-server account/read 强刷（互斥，绝不并行强刷）。
var refreshMu sync.Mutex

func refreshViaAppServer(ctx context.Context) (*CodexAuth, refreshOutcome) {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	before, _ := ReadCodexAuth()
	if ResolveCodexBin(false) == "" {
		return nil, refreshNone
	}
	rctx, cancel := context.WithTimeout(ctx, usageRefreshTO)
	defer cancel()
	res, err := UtilityRequest(rctx, "account/read", map[string]any{"refreshToken": true}, 0)
	if err != nil {
		core.Fail(ctx, "usage", "refresh", err)
		return nil, refreshNone
	}
	// 检查 account === null（permanent 失败信号）。
	var acResp struct {
		Account *json.RawMessage `json:"account"`
	}
	json.Unmarshal(res, &acResp)
	accountIsNull := acResp.Account != nil && string(*acResp.Account) == "null"

	after, _ := ReadCodexAuth()
	if after != nil && before != nil && after.AccessToken != before.AccessToken {
		return after, refreshSuccess
	}
	if accountIsNull {
		return nil, refreshPermanentFail
	}
	return nil, refreshNone
}

// whamGet GET {base}{path}，401 兜底链（每次最多 1 次刷新 + 2 次重试，绝不循环）。
func whamGet(ctx context.Context, hc *http.Client, path string) ([]byte, error) {
	auth, err := ReadCodexAuth()
	if err != nil {
		return nil, err
	}
	// 请求前解 exp 临期先刷。
	if exp, ok := JwtExpMs(auth.AccessToken); ok && exp <= timeNowMillis()+usageExpSkewMS {
		refreshed, outcome := refreshViaAppServer(ctx)
		if outcome == refreshPermanentFail {
			return nil, agent.NewUsageError(agent.UsageErrNeedRelogin, "Codex 登录态已失效")
		}
		if refreshed != nil {
			auth = refreshed
		} else {
			return nil, agent.NewUsageError(agent.UsageErrTransient, "登录态临期且暂时无法刷新")
		}
	}
	base := ChatgptBaseUrl()
	attempt := func(a *CodexAuth) (whamResult, error) {
		return fetchWham(ctx, hc, base, path, a)
	}
	res, err := attempt(auth)
	if err != nil {
		return nil, agent.NewUsageError(agent.UsageErrTransient, "请求失败："+err.Error())
	}
	if res.status == 401 {
		// 第一步：现读 auth.json。
		fresh, _ := ReadCodexAuth()
		if fresh.AccessToken != auth.AccessToken {
			auth = fresh
			if r2, e2 := attempt(auth); e2 == nil {
				res = r2
			}
		}
	}
	if res.status == 401 {
		// 第二步：唯一一次官方刷新。
		refreshed, outcome := refreshViaAppServer(ctx)
		if outcome == refreshPermanentFail {
			return nil, agent.NewUsageError(agent.UsageErrNeedRelogin, "Codex 登录态已失效")
		}
		if refreshed == nil {
			return nil, agent.NewUsageError(agent.UsageErrTransient, "暂时无法刷新 Codex 登录态")
		}
		res, err = attempt(refreshed)
		if err != nil || res.status == 401 {
			return nil, agent.NewUsageError(agent.UsageErrNeedRelogin, "刷新后仍 401，账号侧已拒绝")
		}
	}
	if res.status != 200 || len(res.body) == 0 {
		return nil, agent.NewUsageError(agent.UsageErrTransient, fmt.Sprintf("HTTP %d (%s)", res.status, path))
	}
	return res.body, nil
}

// ── 缓存 + 拉数 ─────────────────────────────────────────────────

var (
	usageCacheMu sync.Mutex
	usageCache   *usageCacheEntry
	profileCache *usageCacheEntry
)

type usageCacheEntry struct {
	at   time.Time
	data interface{}
}

// FetchUsageSnapshot 拉限额数据（缓存 30s）。
func FetchUsageSnapshot(ctx context.Context, hc *http.Client, force bool) (agent.AccountUsageSnapshot, error) {
	usageCacheMu.Lock()
	if !force && usageCache != nil && time.Since(usageCache.at) < usageCacheUsage {
		snap := usageCache.data.(agent.AccountUsageSnapshot)
		usageCacheMu.Unlock()
		return snap, nil
	}
	usageCacheMu.Unlock()

	body, err := whamGet(ctx, hc, "/wham/usage")
	if err != nil {
		return agent.AccountUsageSnapshot{}, err
	}
	var raw rawUsageResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return agent.AccountUsageSnapshot{}, agent.NewUsageError(agent.UsageErrTransient, "usage 响应解析失败："+err.Error())
	}
	snap := MapUsageResponse(raw, timeNowMillis())
	usageCacheMu.Lock()
	usageCache = &usageCacheEntry{at: time.Now(), data: snap}
	usageCacheMu.Unlock()
	return snap, nil
}

// FetchProfileStats 拉统计画像（缓存 5min）。
func FetchProfileStats(ctx context.Context, hc *http.Client, force bool) (agent.AccountProfileStats, error) {
	profileCacheMu.Lock()
	if !force && profileCache != nil && time.Since(profileCache.at) < usageCacheProf {
		p := profileCache.data.(agent.AccountProfileStats)
		profileCacheMu.Unlock()
		return p, nil
	}
	profileCacheMu.Unlock()

	body, err := whamGet(ctx, hc, "/wham/profiles/me")
	if err != nil {
		return agent.AccountProfileStats{}, err
	}
	var raw rawProfileResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return agent.AccountProfileStats{}, agent.NewUsageError(agent.UsageErrTransient, "profile 响应解析失败："+err.Error())
	}
	p := MapProfileResponse(raw)
	profileCacheMu.Lock()
	profileCache = &usageCacheEntry{at: time.Now(), data: p}
	profileCacheMu.Unlock()
	return p, nil
}

// profileCacheMu 单独锁（与 usageCache 独立，避免互锁）。
var profileCacheMu sync.Mutex

// FetchUsageBundle 一次拉齐两端点。
func FetchUsageBundle(ctx context.Context, hc *http.Client, force bool) (*agent.AccountUsageBundle, error) {
	profile, err := FetchProfileStats(ctx, hc, force)
	if err != nil {
		return nil, err
	}
	usage, err := FetchUsageSnapshot(ctx, hc, force)
	if err != nil {
		return nil, err
	}
	return &agent.AccountUsageBundle{Profile: profile, Usage: usage}, nil
}

func timeNowMillis() int64 { return time.Now().UnixMilli() }
