package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveAppSecret_Plain(t *testing.T) {
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{ID: "cli_a", Secret: PlainSecret("literal"), Tenant: TenantFeishu}
	got, err := ResolveAppSecret(cfg)
	if err != nil || got != "literal" {
		t.Fatalf("plain: %q %v", got, err)
	}
}

func TestResolveAppSecret_TemplateEnv(t *testing.T) {
	t.Setenv("MY_APP_SECRET_TPL", "env-val")
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{ID: "cli_a", Secret: PlainSecret("${MY_APP_SECRET_TPL}"), Tenant: TenantFeishu}
	got, err := ResolveAppSecret(cfg)
	if err != nil || got != "env-val" {
		t.Fatalf("template env: %q %v", got, err)
	}
}

func TestResolveAppSecret_EnvRef(t *testing.T) {
	t.Setenv("MY_APP_SECRET_REF", "env-ref-val")
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{
		ID: "cli_a", Tenant: TenantFeishu,
		Secret: RefSecret(SecretRef{Source: "env", ID: "MY_APP_SECRET_REF"}),
	}
	got, err := ResolveAppSecret(cfg)
	if err != nil || got != "env-ref-val" {
		t.Fatalf("env ref: %q %v", got, err)
	}
}

func TestResolveAppSecret_FileRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("  file-val\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{
		ID: "cli_a", Tenant: TenantFeishu,
		Secret: RefSecret(SecretRef{Source: "file", ID: path}),
	}
	got, err := ResolveAppSecret(cfg)
	if err != nil || got != "file-val" {
		t.Fatalf("file ref (should trim): %q %v", got, err)
	}
}

func TestResolveAppSecret_ExecSelfBridgeShortCircuit(t *testing.T) {
	redirectAppDir(t)
	wrapper, _ := EnsureSecretsGetterWrapper()
	ks := NewKeystore(filepath.Join(t.TempDir(), "s.enc"), filepath.Join(t.TempDir(), "salt")).WithSeed("s")
	if err := ks.Set("app-cli_a", "from-keystore"); err != nil {
		t.Fatal(err)
	}
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{
		ID: "cli_a", Tenant: TenantFeishu,
		Secret: RefSecret(SecretRef{Source: "exec", Provider: "bridge", ID: "app-cli_a"}),
	}
	cfg.Secrets = &SecretsConfig{Providers: map[string]ProviderConfig{
		"bridge": {Source: "exec", Command: wrapper},
	}}
	got, err := ResolveAppSecretWith(cfg, ks)
	if err != nil || got != "from-keystore" {
		t.Fatalf("self-bridge short-circuit: %q %v", got, err)
	}
}

func TestResolveAppSecret_Missing(t *testing.T) {
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{ID: "cli_a", Secret: SecretInput{}, Tenant: TenantFeishu}
	if _, err := ResolveAppSecret(cfg); err == nil {
		t.Fatal("missing secret should error")
	}
}

func TestResolveAppSecret_ExecExternalProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh helper is unix-only")
	}
	helper := filepath.Join(t.TempDir(), "provider.sh")
	// 协议：读 stdin（丢弃），固定输出 values[app-x]。
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat > /dev/null\necho '{\"values\":{\"app-x\":\"from-exec\"}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := AppConfig{}
	cfg.Accounts.App = AppCredentials{
		ID: "x", Tenant: TenantFeishu,
		Secret: RefSecret(SecretRef{Source: "exec", ID: "app-x"}),
	}
	cfg.Secrets = &SecretsConfig{Providers: map[string]ProviderConfig{
		"default": {Source: "exec", Command: helper},
	}}
	got, err := ResolveAppSecret(cfg)
	if err != nil || got != "from-exec" {
		t.Fatalf("external exec provider: %q %v", got, err)
	}
}
