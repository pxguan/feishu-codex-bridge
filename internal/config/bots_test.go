package config

import (
	"os"
	"path/filepath"
	"testing"
)

// redirectAppDir 把 AppDir 重定向到临时 HOME（隔离真实 ~/.feishu-codex-bridge）。
func redirectAppDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestLoadBots_Empty(t *testing.T) {
	redirectAppDir(t)
	reg, err := LoadBots()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Bots) != 0 {
		t.Fatalf("empty registry expected, got %v", reg.Bots)
	}
}

func TestSaveLoadBots_Roundtrip(t *testing.T) {
	redirectAppDir(t)
	on := true
	reg := BotsRegistry{
		Bots: []BotEntry{
			{Name: "alpha", AppID: "cli_a", Tenant: TenantFeishu, Active: &on},
			{Name: "beta", AppID: "cli_b", Tenant: TenantLark},
		},
		Current: "cli_a",
	}
	if err := SaveBots(reg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBots()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bots) != 2 || got.Current != "cli_a" {
		t.Fatalf("roundtrip wrong: %+v", got)
	}
	if got.Bots[0].Active == nil || !*got.Bots[0].Active {
		t.Fatal("active flag lost on roundtrip")
	}
}

func TestAddBot_FirstBecomesCurrent_OverwriteByAppID(t *testing.T) {
	redirectAppDir(t)
	reg, err := AddBot(BotEntry{Name: "a", AppID: "cli_a", Tenant: TenantFeishu})
	if err != nil {
		t.Fatal(err)
	}
	if reg.Current != "cli_a" {
		t.Fatal("first bot should become current")
	}
	if _, err := AddBot(BotEntry{Name: "a-renamed", AppID: "cli_a", Tenant: TenantFeishu}); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadBots()
	if len(got.Bots) != 1 || got.Bots[0].Name != "a-renamed" {
		t.Fatalf("overwrite by appId failed: %+v", got.Bots)
	}
}

func TestRemoveBot_CurrentFallback(t *testing.T) {
	redirectAppDir(t)
	AddBot(BotEntry{Name: "a", AppID: "cli_a", Tenant: TenantFeishu})
	AddBot(BotEntry{Name: "b", AppID: "cli_b", Tenant: TenantFeishu})
	reg, err := RemoveBot("cli_a")
	if err != nil {
		t.Fatal(err)
	}
	if reg.Current != "cli_b" {
		t.Fatalf("current should fall back to cli_b, got %q", reg.Current)
	}
}

func TestSetActiveBots_StampedAndCurrentSync(t *testing.T) {
	redirectAppDir(t)
	AddBot(BotEntry{Name: "a", AppID: "cli_a", Tenant: TenantFeishu})
	AddBot(BotEntry{Name: "b", AppID: "cli_b", Tenant: TenantFeishu})
	reg, err := SetActiveBots([]string{"cli_b"})
	if err != nil {
		t.Fatal(err)
	}
	if reg.Current != "cli_b" {
		t.Fatalf("current should sync to first active: %q", reg.Current)
	}
	got, _ := LoadBots()
	a := findBotEntry(got, "cli_a")
	b := findBotEntry(got, "cli_b")
	if a.Active == nil || *a.Active {
		t.Fatal("cli_a should be explicitly false")
	}
	if b.Active == nil || !*b.Active {
		t.Fatal("cli_b should be explicitly true")
	}
}

func TestActiveBots_ConfiguredVsLegacy(t *testing.T) {
	// configured：有显式 active → 返回 active==true 的
	reg := BotsRegistry{Current: "cli_a", Bots: []BotEntry{
		{AppID: "cli_a", Active: boolPtr(false)},
		{AppID: "cli_b", Active: boolPtr(true)},
	}}
	active := ActiveBots(reg)
	if len(active) != 1 || active[0].AppID != "cli_b" {
		t.Fatalf("configured set wrong: %+v", active)
	}
	// legacy：无 active 标志 → 回退 current
	legacy := BotsRegistry{Current: "cli_a", Bots: []BotEntry{{AppID: "cli_a"}, {AppID: "cli_b"}}}
	active = ActiveBots(legacy)
	if len(active) != 1 || active[0].AppID != "cli_a" {
		t.Fatalf("legacy fallback wrong: %+v", active)
	}
}

func TestUniqueName_SlugAndDisambig(t *testing.T) {
	reg := BotsRegistry{Bots: []BotEntry{{Name: "my-bot"}}}
	got := UniqueName(reg, "My Bot!") // slug "my-bot" 已占 → my-bot-2
	if got != "my-bot-2" {
		t.Fatalf("conflict disambig wrong: %q", got)
	}
	// 中文保留
	if got := UniqueName(BotsRegistry{}, "我的机器人"); got != "我的机器人" {
		t.Fatalf("chinese slug lost: %q", got)
	}
	// 全非法字符 → bot
	if got := UniqueName(BotsRegistry{}, "!!!"); got != "bot" {
		t.Fatalf("all-invalid fallback wrong: %q", got)
	}
}

func TestEnsureRegistry_MigrateFlat(t *testing.T) {
	home := redirectAppDir(t)
	flat := AppConfig{}
	flat.Accounts.App = AppCredentials{ID: "cli_legacy", Secret: PlainSecret("shh"), Tenant: TenantFeishu}
	if err := os.MkdirAll(AppDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".feishu-codex-bridge", "config.json"), marshalJSON(t, flat), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := EnsureRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Bots) != 1 || reg.Bots[0].AppID != "cli_legacy" || reg.Current != "cli_legacy" {
		t.Fatalf("migrate wrong: %+v", reg)
	}
	if _, err := os.Stat(BotConfigFile("cli_legacy")); err != nil {
		t.Fatalf("flat config should have moved to bots/<id>/: %v", err)
	}
}

func TestEnsureRegistry_NoopWhenRegistryExists(t *testing.T) {
	redirectAppDir(t)
	SaveBots(BotsRegistry{Bots: []BotEntry{{Name: "x", AppID: "cli_x", Tenant: TenantFeishu}}})
	reg, err := EnsureRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Bots) != 1 || reg.Bots[0].Name != "x" {
		t.Fatalf("existing registry should be untouched: %+v", reg)
	}
}

func findBotEntry(reg BotsRegistry, appID string) BotEntry {
	for _, b := range reg.Bots {
		if b.AppID == appID {
			return b
		}
	}
	return BotEntry{}
}
