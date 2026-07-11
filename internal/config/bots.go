package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// bots.go —— 多 bot 注册表（bots.json）+ legacy 单 bot 迁移（对齐 TS config/bots）。

// BotEntry 一个已注册 bot（明文 secret 在 keystore，key=app-<appId>）。
type BotEntry struct {
	Name      string      `json:"name"` // 短句柄（slug，唯一）
	AppID     string      `json:"appId"`
	Tenant    TenantBrand `json:"tenant"`
	BotName   string      `json:"botName,omitempty"` // 校验时取的展示名
	CreatedAt int64       `json:"createdAt"`
	// Active 是否随 run/start 启动；nil=未配置（legacy，回退 current）。
	Active *bool `json:"active,omitempty"`
}

// BotsRegistry bots.json 结构。
type BotsRegistry struct {
	Version int        `json:"version"`
	Current string     `json:"current,omitempty"` // 主 bot appId（与 active set 首个同步）
	Bots    []BotEntry `json:"bots"`
}

// LoadBots 读 bots.json；ENOENT 返回空 registry。
func LoadBots() (BotsRegistry, error) {
	b, err := os.ReadFile(BotsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return BotsRegistry{Version: 1, Bots: nil}, nil
		}
		return BotsRegistry{}, err
	}
	var reg BotsRegistry
	if err := json.Unmarshal(b, &reg); err != nil {
		return BotsRegistry{}, err
	}
	reg.Version = 1
	return reg, nil
}

// SaveBots 原子写 bots.json（0600）。
func SaveBots(reg BotsRegistry) error {
	reg.Version = 1
	return writeJSONAtomic(BotsFile(), reg, 0o600)
}

// EnsureRegistry 读注册表；若不存在则尝试迁移 legacy 单 bot 安装（幂等）。
func EnsureRegistry() (BotsRegistry, error) {
	if _, err := os.Stat(BotsFile()); err == nil {
		return LoadBots()
	} else if !os.IsNotExist(err) {
		return BotsRegistry{}, err
	}
	// 无 registry：检查 legacy flat config.json。
	flatPath := filepath.Join(AppDir(), "config.json")
	flat, err := LoadConfig(flatPath)
	if err != nil || !IsComplete(flat) {
		return BotsRegistry{Version: 1}, nil
	}
	appID := flat.Accounts.App.ID
	tenant := flat.Accounts.App.Tenant
	dest := BotDir(appID)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return BotsRegistry{}, err
	}
	for _, file := range []string{"config.json", "projects.json", "sessions.json", "processes.json"} {
		moveIfExists(filepath.Join(AppDir(), file), filepath.Join(dest, file))
	}
	reg := BotsRegistry{
		Version: 1,
		Current: appID,
		Bots:    []BotEntry{{Name: "default", AppID: appID, Tenant: tenant, CreatedAt: time.Now().UnixMilli()}},
	}
	if err := SaveBots(reg); err != nil {
		return BotsRegistry{}, err
	}
	return reg, nil
}

func moveIfExists(src, dst string) {
	if _, err := os.Stat(src); err != nil {
		return
	}
	_ = os.Rename(src, dst)
}

// FindBot 按 name 或 appId 查找。
func FindBot(reg BotsRegistry, nameOrAppID string) (BotEntry, bool) {
	for _, b := range reg.Bots {
		if b.Name == nameOrAppID || b.AppID == nameOrAppID {
			return b, true
		}
	}
	return BotEntry{}, false
}

// CurrentBot 返回 current 指向的 bot。
func CurrentBot(reg BotsRegistry) (BotEntry, bool) {
	if reg.Current == "" {
		return BotEntry{}, false
	}
	return FindBot(reg, reg.Current)
}

// ActiveBots 返回 run/start 应启动的集合：
// 任一 bot 有显式 active 标志 → 返回 active==true 的；否则回退 current。
func ActiveBots(reg BotsRegistry) []BotEntry {
	configured := false
	for _, b := range reg.Bots {
		if b.Active != nil {
			configured = true
			break
		}
	}
	if configured {
		var out []BotEntry
		for _, b := range reg.Bots {
			if b.Active != nil && *b.Active {
				out = append(out, b)
			}
		}
		return out
	}
	if cur, ok := CurrentBot(reg); ok {
		return []BotEntry{cur}
	}
	return nil
}

// SetActiveBots 覆盖 active set 为 appIds；给每个 bot 打显式 active 标志；
// current 指向首个仍 active 的 bot；空集保留 current 不变。
func SetActiveBots(appIDs []string) (BotsRegistry, error) {
	reg, err := LoadBots()
	if err != nil {
		return BotsRegistry{}, err
	}
	want := map[string]bool{}
	for _, id := range appIDs {
		want[id] = true
	}
	for i := range reg.Bots {
		on := want[reg.Bots[i].AppID]
		reg.Bots[i].Active = &on
	}
	for _, b := range reg.Bots {
		if b.Active != nil && *b.Active {
			reg.Current = b.AppID
			break
		}
	}
	if err := SaveBots(reg); err != nil {
		return BotsRegistry{}, err
	}
	return reg, nil
}

// AddBot 新增（或按 appId 覆盖）bot；首个成为 current。
func AddBot(entry BotEntry) (BotsRegistry, error) {
	reg, err := LoadBots()
	if err != nil {
		return BotsRegistry{}, err
	}
	filtered := reg.Bots[:0]
	for _, b := range reg.Bots {
		if b.AppID != entry.AppID {
			filtered = append(filtered, b)
		}
	}
	filtered = append(filtered, entry)
	reg.Bots = filtered
	if reg.Current == "" {
		reg.Current = entry.AppID
	}
	if err := SaveBots(reg); err != nil {
		return BotsRegistry{}, err
	}
	return reg, nil
}

// SetCurrent 设置 current appId。
func SetCurrent(appID string) error {
	reg, err := LoadBots()
	if err != nil {
		return err
	}
	reg.Current = appID
	return SaveBots(reg)
}

// RemoveBot 删除 bot；若删的是 current，回退首个剩余。
func RemoveBot(appID string) (BotsRegistry, error) {
	reg, err := LoadBots()
	if err != nil {
		return BotsRegistry{}, err
	}
	filtered := reg.Bots[:0]
	for _, b := range reg.Bots {
		if b.AppID != appID {
			filtered = append(filtered, b)
		}
	}
	reg.Bots = filtered
	if reg.Current == appID {
		reg.Current = ""
		if len(reg.Bots) > 0 {
			reg.Current = reg.Bots[0].AppID
		}
	}
	if err := SaveBots(reg); err != nil {
		return BotsRegistry{}, err
	}
	return reg, nil
}

// UniqueName 由 desired 派生注册表唯一短名（slugify + 冲突加 -2/-3…）。
func UniqueName(reg BotsRegistry, desired string) string {
	base := slugify(desired)
	if base == "" {
		base = "bot"
	}
	if !hasBotName(reg, base) {
		return base
	}
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s-%d", base, i)
		if !hasBotName(reg, c) {
			return c
		}
	}
}

func hasBotName(reg BotsRegistry, name string) bool {
	for _, b := range reg.Bots {
		if b.Name == name {
			return true
		}
	}
	return false
}

var (
	slugNonAlnum = regexp.MustCompile(`[^a-z0-9\p{Han}]+`)
	slugEdge     = regexp.MustCompile(`^-+|-+$`)
)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = slugEdge.ReplaceAllString(s, "")
	r := []rune(s)
	if len(r) > 32 {
		r = r[:32]
	}
	return string(r)
}
