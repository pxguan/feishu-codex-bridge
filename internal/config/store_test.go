package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadConfig_ENOENT(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || IsComplete(cfg) {
		t.Fatalf("ENOENT should give empty cfg nil err: %+v %v", cfg, err)
	}
}

func TestSaveLoadConfig_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{ID: "cli_a", Secret: PlainSecret("shh"), Tenant: TenantLark}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsComplete(got) || got.Accounts.App.ID != "cli_a" {
		t.Fatalf("roundtrip wrong: %+v", got)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("perm=%o want 0600", fi.Mode().Perm())
		}
	}
}

func TestBuildEncryptedAccountConfig(t *testing.T) {
	redirectAppDir(t)
	cfg, err := BuildEncryptedAccountConfig("cli_a", TenantFeishu, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Accounts.App.Secret.IsRef() {
		t.Fatal("secret should be exec ref, not plaintext")
	}
	ref := cfg.Accounts.App.Secret.Ref
	if ref.Source != "exec" || ref.Provider != "bridge" || ref.ID != "app-cli_a" {
		t.Fatalf("ref wrong: %+v", ref)
	}
	if cfg.Secrets == nil || cfg.Secrets.Providers["bridge"].Command == "" {
		t.Fatal("bridge provider missing")
	}
}

func TestEnsureSecretsGetterWrapper(t *testing.T) {
	redirectAppDir(t)
	wrapper, err := EnsureSecretsGetterWrapper()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "secrets get") {
		t.Fatalf("wrapper content wrong: %s", b)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(wrapper)
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("perm=%o want 0700", fi.Mode().Perm())
		}
	}
}
