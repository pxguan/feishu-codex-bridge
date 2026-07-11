package bot

// register_bot.go —— day-0 凭据直填注册 bot（对齐 TS bot/register-bot）。
// 校验 appId → 探活 → keystore 存 secret → BuildEncryptedAccountConfig → SaveConfig → AddBot。
// 绝不 throw：所有失败落 RegisterBotResult.Failure。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/utils"
)

var appIDRe = regexp.MustCompile(`^cli_[A-Za-z0-9]{6,}$`)

// RegisterBotInput 直填注册输入。
type RegisterBotInput struct {
	AppID       string
	AppSecret   string
	Tenant      string // feishu | lark
	DesiredName string
	OwnerOpenID string
}

// RegisterBotResult 注册结果（Ok=true 成功；Ok=false 时 Code/Reason 含失败原因）。
type RegisterBotResult struct {
	Ok            bool
	Code          string // invalid_input | credential_rejected | persist_failed（Ok=false 时）
	Reason        string
	Name          string
	AppID         string
	Tenant        string
	BotName       string
	MissingScopes []string
}

// RegisterBotFromCredentials 校验 + 探活 + 落盘注册一个 bot。绝不 throw。
func RegisterBotFromCredentials(ctx context.Context, input RegisterBotInput, hc *http.Client) RegisterBotResult {
	appID := strings.TrimSpace(input.AppID)
	appSecret := strings.TrimSpace(input.AppSecret)
	tenant := config.TenantFeishu
	if input.Tenant == "lark" {
		tenant = config.TenantLark
	}

	if appID == "" || appSecret == "" {
		return RegisterBotResult{Code: "invalid_input", Reason: "App ID 与 App Secret 都不能为空。"}
	}
	if !appIDRe.MatchString(appID) {
		return RegisterBotResult{Code: "invalid_input", Reason: "App ID 格式不对：应为开发者后台「凭证与基础信息」里的 App ID（形如 cli_ 开头）。"}
	}

	// 真探活。
	v := utils.ValidateAppCredentials(ctx, appID, appSecret, tenant, hc)
	if !v.Ok {
		return RegisterBotResult{Code: "credential_rejected", Reason: "凭据校验失败：" + v.Reason + "。请核对 App ID / App Secret。"}
	}

	// secret 进 keystore → BuildEncryptedAccountConfig → SaveConfig → AddBot。
	ks := config.NewKeystore(config.SecretsFile(), config.KeystoreSaltFile())
	if err := ks.Set(config.SecretKeyForApp(appID), appSecret); err != nil {
		return RegisterBotResult{Code: "persist_failed", Reason: "保存密钥失败：" + err.Error()}
	}

	configFile := config.BotConfigFile(appID)
	existing, err := config.LoadConfig(configFile)
	if err != nil {
		return RegisterBotResult{Code: "persist_failed", Reason: "读旧 config 失败：" + err.Error()}
	}
	var prefs *config.AppPreferences
	if config.IsComplete(existing) {
		prefs = existing.Preferences
	}
	prefs = withOwnerAdmin(prefs, input.OwnerOpenID)

	cfg, err := config.BuildEncryptedAccountConfig(appID, tenant, prefs)
	if err != nil {
		return RegisterBotResult{Code: "persist_failed", Reason: "构建 config 失败：" + err.Error()}
	}
	if err := config.SaveConfig(configFile, cfg); err != nil {
		return RegisterBotResult{Code: "persist_failed", Reason: "写 config 失败：" + err.Error()}
	}

	reg, err := config.LoadBots()
	if err != nil {
		return RegisterBotResult{Code: "persist_failed", Reason: "读 registry 失败：" + err.Error()}
	}
	desired := input.DesiredName
	if desired == "" {
		desired = v.BotName
	}
	if desired == "" {
		desired = appID
	}
	name := config.UniqueName(reg, desired)
	if _, err := config.AddBot(config.BotEntry{
		Name: name, AppID: appID, Tenant: tenant, BotName: v.BotName, CreatedAt: nowUnixMilli(),
	}); err != nil {
		return RegisterBotResult{Code: "persist_failed", Reason: "写 registry 失败：" + err.Error()}
	}

	return RegisterBotResult{
		Ok:            true,
		Name:          name,
		AppID:         appID,
		Tenant:        string(tenant),
		BotName:       v.BotName,
		MissingScopes: v.MissingScopes,
	}
}

// withOwnerAdmin 把 ownerOpenId 落成 owner + admin（幂等：admins 去重）。
func withOwnerAdmin(base *config.AppPreferences, ownerOpenID string) *config.AppPreferences {
	if ownerOpenID == "" {
		return base
	}
	var access config.AppAccess
	if base != nil && base.Access != nil {
		access = *base.Access
	}
	access.OwnerOpenID = ownerOpenID
	adminSet := map[string]bool{}
	for _, a := range access.Admins {
		adminSet[a] = true
	}
	if !adminSet[ownerOpenID] {
		access.Admins = append(access.Admins, ownerOpenID)
	}
	prefs := config.AppPreferences{}
	if base != nil {
		prefs = *base
	}
	prefs.Access = &access
	return &prefs
}

func nowUnixMilli() int64 {
	// 避免直接用 time.Now()（测试可控）；生产用 time.Now().UnixMilli()。
	return timeNowMilli()
}

// timeNowMilli 可注入的时间（测试用）。默认 time.Now().UnixMilli()。
var timeNowMilli = func() int64 { return time.Now().UnixMilli() }

// 保证 httptest 被引用（测试用 mock server）。
var _ = httptest.NewServer

// fmt 保持引用（错误信息格式化）。
var _ = fmt.Sprintf
