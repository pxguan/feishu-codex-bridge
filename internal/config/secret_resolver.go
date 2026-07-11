package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// secret_resolver.go —— 解析 bot App Secret（对齐 TS config/secret-resolver）。
//
// 支持：明文 / "${VAR}" 模板 / {source:env|file|exec} SecretRef。
// exec 分支自桥短路：当 provider 是本桥 secrets-getter 时，直接读 keystore，不 spawn 自己。

const (
	defaultExecTimeout = 5 * time.Second
	defaultExecMaxOut  = 64 * 1024
	defaultProviderKey = "default"
)

var envTemplateRE = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]{0,127})\}$`)

// ResolveAppSecret 用默认 keystore 解析 bot App Secret。
func ResolveAppSecret(cfg AppConfig) (string, error) {
	return ResolveAppSecretWith(cfg, NewKeystore(SecretsFile(), KeystoreSaltFile()))
}

// ResolveAppSecretWith 用给定 keystore 解析（测试注入）。
func ResolveAppSecretWith(cfg AppConfig, ks *Keystore) (string, error) {
	return resolveSecretInput(cfg.Accounts.App.Secret, cfg.Secrets, cfg.Accounts.App.ID, ks)
}

func resolveSecretInput(input SecretInput, secrets *SecretsConfig, appID string, ks *Keystore) (string, error) {
	if input.IsZero() {
		return "", errors.New("app secret is missing")
	}
	if !input.IsRef() {
		return resolvePlainOrTemplate(input.Plain)
	}
	ref := input.Ref
	pc, _ := lookupProvider(secrets, ref)
	switch ref.Source {
	case "env":
		return resolveEnvRef(ref, pc)
	case "file":
		return resolveFileRef(ref, pc)
	case "exec":
		return resolveExecRef(ref, pc, appID, ks)
	default:
		return "", fmt.Errorf("unknown secret source: %s", ref.Source)
	}
}

func resolvePlainOrTemplate(v string) (string, error) {
	if v == "" {
		return "", errors.New("app secret is empty")
	}
	if m := envTemplateRE.FindStringSubmatch(v); m != nil {
		name := m[1]
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("env var %s referenced by secret is not set", name)
		}
		return val, nil
	}
	return v, nil
}

func lookupProvider(secrets *SecretsConfig, ref *SecretRef) (ProviderConfig, bool) {
	if secrets == nil || secrets.Providers == nil {
		return ProviderConfig{}, false
	}
	name := ref.Provider
	if name == "" {
		switch ref.Source {
		case "env":
			name = secrets.Defaults.Env
		case "file":
			name = secrets.Defaults.File
		case "exec":
			name = secrets.Defaults.Exec
		}
		if name == "" {
			name = defaultProviderKey
		}
	}
	p, ok := secrets.Providers[name]
	return p, ok
}

func resolveEnvRef(ref *SecretRef, pc ProviderConfig) (string, error) {
	if len(pc.Allowlist) > 0 && !contains(pc.Allowlist, ref.ID) {
		return "", fmt.Errorf("env var %s is not allowlisted in provider", ref.ID)
	}
	v, ok := os.LookupEnv(ref.ID)
	if !ok {
		return "", fmt.Errorf("env var %s is not set", ref.ID)
	}
	return v, nil
}

func resolveFileRef(ref *SecretRef, pc ProviderConfig) (string, error) {
	p := ref.ID
	if pc.Path != "" {
		p = filepath.Join(pc.Path, ref.ID)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func resolveExecRef(ref *SecretRef, pc ProviderConfig, appID string, ks *Keystore) (string, error) {
	if pc.Command == "" {
		return "", errors.New("exec provider missing `command`")
	}
	if isSelfBridgeCommand(pc) {
		// 自桥短路：直接读 keystore，不 spawn 自己。
		if v, ok, err := ks.Get(ref.ID); err == nil && ok {
			return v, nil
		}
		conventional := SecretKeyForApp(appID)
		if v, ok, err := ks.Get(conventional); err == nil && ok {
			return v, nil
		}
		return "", fmt.Errorf("keystore has no entry for %q or %q", ref.ID, conventional)
	}
	return spawnExecProvider(ref, pc)
}

// isSelfBridgeCommand 判定 provider 是否指向本桥 secrets-getter。
func isSelfBridgeCommand(pc ProviderConfig) bool {
	if pc.Command == SecretsGetterScript() {
		return true
	}
	if len(pc.Args) >= 2 {
		a := pc.Args[len(pc.Args)-2]
		b := pc.Args[len(pc.Args)-1]
		if a == "secrets" && b == "get" {
			return true
		}
	}
	return false
}

func spawnExecProvider(ref *SecretRef, pc ProviderConfig) (string, error) {
	timeout := defaultExecTimeout
	if pc.NoOutputTimeoutMs > 0 {
		timeout = time.Duration(pc.NoOutputTimeoutMs) * time.Millisecond
	}
	maxOut := defaultExecMaxOut
	if pc.MaxOutputBytes > 0 {
		maxOut = pc.MaxOutputBytes
	}
	providerName := ref.Provider
	if providerName == "" {
		providerName = defaultProviderKey
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, pc.Command, pc.Args...)
	cmd.Env = buildProviderEnv(pc)
	reqJSON, _ := json.Marshal(map[string]any{
		"protocolVersion": 1,
		"provider":        providerName,
		"ids":             []string{ref.ID},
	})
	cmd.Stdin = bytes.NewReader(reqJSON)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("exec provider timed out after %s", timeout)
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail != "" {
			return "", fmt.Errorf("exec provider failed: %v: %s", err, detail)
		}
		return "", fmt.Errorf("exec provider failed: %w", err)
	}
	if out.Len() > maxOut {
		return "", fmt.Errorf("exec provider stdout exceeded %d bytes", maxOut)
	}
	var resp struct {
		Values map[string]string `json:"values"`
		Errors map[string]struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return "", fmt.Errorf("exec provider returned invalid JSON: %w", err)
	}
	if v, ok := resp.Values[ref.ID]; ok {
		return v, nil
	}
	if e, ok := resp.Errors[ref.ID]; ok && e.Message != "" {
		return "", fmt.Errorf("exec provider did not return secret for %s: %s", ref.ID, e.Message)
	}
	return "", fmt.Errorf("exec provider did not return secret for %s", ref.ID)
}

func buildProviderEnv(pc ProviderConfig) []string {
	env := []string{}
	if pc.PassEnv != nil {
		for _, k := range pc.PassEnv {
			if v, ok := os.LookupEnv(k); ok {
				env = append(env, k+"="+v)
			}
		}
	}
	for k, v := range pc.Env {
		env = append(env, k+"="+v)
	}
	return env
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
