package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// feishuauth.go —— 校验 bot 凭据（换 tenant_access_token + 取 bot 信息 + 比对 scope）。
// 对齐 TS utils/feishu-auth。绝不返回 error——所有失败（网络/非 0 码）落 ValidationResult.Reason。

// ValidationResult 凭据校验结果。
type ValidationResult struct {
	Ok                bool
	Reason            string
	BotName           string
	BotOpenID         string
	MissingScopes     []string // nil=无法获取；空切片=全部已授
	MissingJoinScopes []string
}

func tenantBase(tenant config.TenantBrand) string {
	if tenant == config.TenantLark {
		return "https://open.larksuite.com"
	}
	return "https://open.feishu.cn"
}

// ValidateAppCredentials 用默认 base（按 tenant）校验。
func ValidateAppCredentials(ctx context.Context, appID, appSecret string, tenant config.TenantBrand, hc *http.Client) ValidationResult {
	return validateAt(ctx, hc, tenantBase(tenant), appID, appSecret)
}

func validateAt(ctx context.Context, hc *http.Client, base, appID, appSecret string) ValidationResult {
	if hc == nil {
		hc = http.DefaultClient
	}
	// 1. tenant_access_token
	token, reason := fetchTenantToken(ctx, hc, base, appID, appSecret)
	if reason != "" {
		return ValidationResult{Ok: false, Reason: reason}
	}
	// 2. bot info（best-effort）
	info := fetchBotInfo(ctx, hc, base, token)
	// 3. granted scopes（best-effort；nil=无法获取）
	granted := fetchGrantedScopes(ctx, hc, base, token)

	res := ValidationResult{Ok: true}
	if info != nil {
		res.BotName = info.Bot.AppName
		res.BotOpenID = info.Bot.OpenID
	}
	res.MissingScopes = diffScopes(granted, config.REQUIRED_SCOPES)
	res.MissingJoinScopes = diffScopes(granted, config.JOIN_GROUP_SCOPES)
	return res
}

func diffScopes(granted map[string]bool, list []string) []string {
	if granted == nil {
		return nil // 无法获取时显式 nil（区别于「全部已授」的空切片）
	}
	out := []string{}
	for _, s := range list {
		if !granted[s] {
			out = append(out, s)
		}
	}
	return out
}

type tokenResp struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
}

func fetchTenantToken(ctx context.Context, hc *http.Client, base, appID, appSecret string) (string, string) {
	body, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	code, resp, err := postJSON(ctx, hc, base+"/open-apis/auth/v3/tenant_access_token/internal", body)
	if err != nil {
		return "", "网络错误：" + err.Error()
	}
	if code != http.StatusOK {
		return "", fmt.Sprintf("token HTTP %d", code)
	}
	var tr tokenResp
	if err := json.Unmarshal(resp, &tr); err != nil {
		return "", "响应不是合法 JSON"
	}
	if tr.Code != 0 || tr.TenantAccessToken == "" {
		return "", fmt.Sprintf("token code=%d msg=%s", tr.Code, fallback(tr.Msg, "<no msg>"))
	}
	return tr.TenantAccessToken, ""
}

type botInfoResp struct {
	Code int `json:"code"`
	Bot  struct {
		ActivateStatus int    `json:"activate_status"`
		AppName        string `json:"app_name"`
		OpenID         string `json:"open_id"`
	} `json:"bot"`
}

func fetchBotInfo(ctx context.Context, hc *http.Client, base, token string) *botInfoResp {
	code, body, err := getJSON(ctx, hc, base+"/open-apis/bot/v3/info", "Bearer "+token)
	if err != nil || code != http.StatusOK {
		return nil
	}
	var info botInfoResp
	if err := json.Unmarshal(body, &info); err != nil {
		return nil
	}
	return &info
}

type scopeListResp struct {
	Data struct {
		Scopes []struct {
			ScopeName   string `json:"scope_name"`
			GrantStatus int    `json:"grant_status"`
		} `json:"scopes"`
	} `json:"data"`
}

// fetchGrantedScopes 返回 grant_status==1 的 scope 名集合；任一失败返回 nil。
func fetchGrantedScopes(ctx context.Context, hc *http.Client, base, token string) map[string]bool {
	code, body, err := getJSON(ctx, hc, base+"/open-apis/application/v6/scopes", "Bearer "+token)
	if err != nil || code != http.StatusOK {
		return nil
	}
	var sr scopeListResp
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, s := range sr.Data.Scopes {
		if s.GrantStatus == 1 {
			out[s.ScopeName] = true
		}
	}
	return out
}

// ── HTTP 小工具 ──────────────────────────────────────────────────

func postJSON(ctx context.Context, hc *http.Client, url string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return doReq(ctx, hc, req)
}

func getJSON(ctx context.Context, hc *http.Client, url, bearer string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	return doReq(ctx, hc, req)
}

func doReq(ctx context.Context, hc *http.Client, req *http.Request) (int, []byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := readAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func fallback(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}
