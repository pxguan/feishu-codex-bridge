package codex

import (
	"errors"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestSandboxParams_FullDangerAccess(t *testing.T) {
	p, err := SandboxParams(agent.PermissionFull, true, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if p["sandbox"] != "danger-full-access" {
		t.Fatalf("full should be danger-full-access: %v", p)
	}
	// 空 mode 同 full（保留历史默认）。
	p, err = SandboxParams("", false, "darwin")
	if err != nil || p["sandbox"] != "danger-full-access" {
		t.Fatalf("empty mode should be full: %v %v", p, err)
	}
}

func TestSandboxParams_QA_Darwin_ReadConfinement(t *testing.T) {
	p, err := SandboxParams(agent.PermissionQA, false, "darwin")
	if err != nil {
		t.Fatalf("qa on darwin should succeed: %v", err)
	}
	cfg, _ := p["config"].(map[string]any)
	if cfg == nil || cfg["default_permissions"] != "feishu" {
		t.Fatalf("missing feishu profile: %v", cfg)
	}
	feishu, _ := cfg["permissions"].(map[string]any)["feishu"].(map[string]any)
	fs, _ := feishu["filesystem"].(map[string]any)
	if fs[":minimal"] != "read" {
		t.Fatalf(":minimal should be read: %v", fs)
	}
	roots, _ := fs[":workspace_roots"].(map[string]any)
	if roots["."] != "read" {
		t.Fatalf("qa should confine . to read: %v", roots)
	}
	net, _ := feishu["network"].(map[string]any)
	if net["enabled"] != false {
		t.Fatalf("network should follow param: %v", net)
	}
}

func TestSandboxParams_Write_Darwin_WriteRoot(t *testing.T) {
	p, err := SandboxParams(agent.PermissionWrite, true, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := p["config"].(map[string]any)
	feishu, _ := cfg["permissions"].(map[string]any)["feishu"].(map[string]any)
	fs, _ := feishu["filesystem"].(map[string]any)
	roots, _ := fs[":workspace_roots"].(map[string]any)
	if roots["."] != "write" {
		t.Fatalf("write should allow . write: %v", roots)
	}
	net, _ := feishu["network"].(map[string]any)
	if net["enabled"] != true {
		t.Fatalf("network should be enabled: %v", net)
	}
}

func TestSandboxParams_QA_LinuxFailClosed(t *testing.T) {
	// Linux/WSL 沙箱只挡写不挡读 → 隐私会泄露，必须 fail-closed 拒绝（绝不降级 full）。
	_, err := SandboxParams(agent.PermissionQA, false, "linux")
	if err == nil {
		t.Fatal("qa on linux should fail-closed")
	}
	if !errors.Is(err, err) || err.Error() == "" {
		t.Fatalf("error should have message: %v", err)
	}
}

func TestWithAutoCompact_DisabledSetsLimit(t *testing.T) {
	off := false
	params := map[string]any{"cwd": "/proj", "config": map[string]any{"foo": "bar"}}
	out := WithAutoCompact(params, &off)
	cfg, _ := out["config"].(map[string]any)
	if cfg["model_auto_compact_token_limit"] != autoCompactOffLimit {
		t.Fatalf("auto-compact off should set limit=1e9: %v", cfg)
	}
	// 原 config 的键应保留。
	if cfg["foo"] != "bar" {
		t.Fatalf("existing config keys lost: %v", cfg)
	}
	// 非 config 键保留。
	if out["cwd"] != "/proj" {
		t.Fatalf("non-config keys lost: %v", out)
	}
	// 原 params 不被改（深拷贝了 config）。
	origCfg, _ := params["config"].(map[string]any)
	if _, exists := origCfg["model_auto_compact_token_limit"]; exists {
		t.Fatal("original params config should not be mutated")
	}
}

func TestWithAutoCompact_OnOrNilLeavesUnchanged(t *testing.T) {
	on := true
	params := map[string]any{"cwd": "/proj"}
	if out := WithAutoCompact(params, &on); out["cwd"] != "/proj" || out["config"] != nil {
		t.Fatalf("on should leave params unchanged: %v", out)
	}
	if out := WithAutoCompact(params, nil); out["cwd"] != "/proj" {
		t.Fatalf("nil should leave params unchanged: %v", out)
	}
}
