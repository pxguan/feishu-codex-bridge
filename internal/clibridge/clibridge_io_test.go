package clibridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIpcRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "cli-bridge.sock")
	srv, err := StartCliBridgeIpcServer(sock, func(msg CliHookMessage) (CliHookResponse, error) {
		if msg.Type == MsgTypePermissionRequest {
			return CliHookResponse{Decision: DecisionAllow}, nil
		}
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "nope"}, nil
	})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	resp, err := SendCliHookMessage(sock, CliHookMessage{
		Type: MsgTypePermissionRequest, Source: AgentClaude, SessionID: "s", Cwd: "/x",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if resp.Decision != DecisionAllow {
		t.Fatalf("expected allow, got %+v", resp)
	}

	resp2, err := SendCliHookMessage(sock, CliHookMessage{
		Type: MsgTypePreToolUse, Source: AgentClaude,
	})
	if err != nil {
		t.Fatalf("send2: %v", err)
	}
	if resp2.Decision != DecisionFallbackLocal {
		t.Fatalf("expected fallback, got %+v", resp2)
	}
}

func TestInstallUninstallHooks(t *testing.T) {
	home := t.TempDir()
	cmd := ResolveBridgeHookCommand("app123")
	installOpts := InstallCliBridgeHooksOptions{HomeDir: home, Command: cmd}
	installOpts.Agents.Claude = true
	installOpts.Agents.Codex = true
	if err := InstallCliBridgeHooks(installOpts); err != nil {
		t.Fatalf("install: %v", err)
	}

	claude, codex := InspectCliBridgeHooks(InspectCliBridgeHooksOptions{HomeDir: home})
	if claude.Status != HookInstalled {
		t.Fatalf("claude status=%s details=%v", claude.Status, claude.Details)
	}
	if codex.Status != HookInstalled {
		t.Fatalf("codex status=%s details=%v", codex.Status, codex.Details)
	}

	// config.toml 应含 [features] hooks=true。
	tomlData, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read toml: %v", err)
	}
	if !hasCodexHooksFeature(string(tomlData)) {
		t.Fatalf("config.toml missing [features] hooks=true:\n%s", tomlData)
	}

	if err := UninstallCliBridgeHooks(InspectCliBridgeHooksOptions{HomeDir: home}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	claude2, codex2 := InspectCliBridgeHooks(InspectCliBridgeHooksOptions{HomeDir: home})
	if claude2.Status != HookNotInstalled {
		t.Fatalf("claude after uninstall status=%s", claude2.Status)
	}
	if codex2.Status != HookNotInstalled {
		t.Fatalf("codex after uninstall status=%s", codex2.Status)
	}
}

func TestResolveBridgeHookCommand(t *testing.T) {
	cmd := ResolveBridgeHookCommand("app1")
	if !contains(cmd, "--bot") || !contains(cmd, "hook") {
		t.Fatalf("command missing bot/hook: %q", cmd)
	}
	if contains(cmd, agent2LarkMarker) {
		t.Fatalf("should not contain agent2lark marker")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
