package clibridge

// ipc_e2e_test.go —— 端到端：agent hook 客户端经真实 Unix socket 连到 Service，
// 人在飞书上「允许」后，决策经 socket 回到 agent，再序列化为 hook stdout。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

func TestCliBridgeIpcEndToEndApprove(t *testing.T) {
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("cb-it-%d.sock", os.Getpid()))
	_ = os.Remove(socket)
	defer os.Remove(socket)

	cfg := config.AppConfig{
		Preferences: &config.AppPreferences{
			Access:    &config.AppAccess{OwnerOpenID: "ou_owner"},
			CliBridge: &config.CliBridgePreferences{Enabled: boolPtr(true)},
		},
	}
	var sent []card.CardObject
	deps := ServiceDeps{
		Cfg:       cfg,
		SocketPath: socket,
		SendOwnerCard: func(_ context.Context, c card.CardObject) (string, error) {
			sent = append(sent, c)
			return "msg-" + strconv.Itoa(len(sent)), nil
		},
		UpdateOwnerCard:      func(_ context.Context, _ string, _ card.CardObject) bool { return true },
		SendGroupTopic:       func(_ context.Context, _ string, _ string, _ bool) error { return nil },
		AddTypingReaction:    func(_ context.Context, _ string) (string, error) { return "r", nil },
		RemoveTypingReaction: func(_ context.Context, _ string, _ string) error { return nil },
		IsBoundProject:       func(_ string) bool { return true },
		FindProjectByCwd:     func(_ string) (*ProjectRef, error) { return nil, nil },
		CreateProjectForCwd:  func(_ string, _ string) (*ProjectRef, error) { return nil, nil },
		Presence:             func() (CliPresenceRoute, error) { return CliPresenceRoute{RouteToFeishu: true, Reason: "away"}, nil },
		LocalActivity:        func() (bool, error) { return false, nil },
	}
	svc := CreateCliBridgeService(deps)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start ipc server: %v", err)
	}
	defer svc.Shutdown(context.Background())

	msg := CliHookMessage{
		Type:          MsgTypePermissionRequest,
		Source:        AgentClaude,
		SessionID:     "sess-e2e",
		Cwd:           "/proj",
		ToolName:      "Bash",
		ToolInput:     map[string]any{"command": "git push --force"},
		HookEventName: "PreToolUse",
	}

	// agent 侧：连 socket、发请求、收决策。
	agentRespCh := make(chan CliHookResponse, 1)
	agentErrCh := make(chan error, 1)
	go func() {
		resp, err := SendCliHookMessage(socket, msg)
		if err != nil {
			agentErrCh <- err
			return
		}
		agentRespCh <- resp
	}()

	// 桥侧：等 pending 出现，模拟人在飞书上点「允许」。
	pending := waitForPending(t, PendingPermission)
	if !svc.resolveAction(CLI.ApproveOnce, pending.ID) {
		t.Fatal("resolveAction(ApproveOnce) returned false")
	}

	select {
	case err := <-agentErrCh:
		t.Fatalf("agent send failed: %v", err)
	case resp := <-agentRespCh:
		if resp.Decision != DecisionAllow {
			t.Fatalf("agent expected allow, got %q", resp.Decision)
		}
		stdout := BuildHookStdout(msg, resp)
		if !strings.Contains(stdout, "allow") {
			t.Fatalf("hook stdout missing 'allow': %s", stdout)
		}
	}
}

func TestCliBridgeIpcEndToEndDeny(t *testing.T) {
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("cb-it-deny-%d.sock", os.Getpid()))
	_ = os.Remove(socket)
	defer os.Remove(socket)

	cfg := config.AppConfig{
		Preferences: &config.AppPreferences{
			Access:    &config.AppAccess{OwnerOpenID: "ou_owner"},
			CliBridge: &config.CliBridgePreferences{Enabled: boolPtr(true)},
		},
	}
	var sent []card.CardObject
	deps := ServiceDeps{
		Cfg:       cfg,
		SocketPath: socket,
		SendOwnerCard: func(_ context.Context, c card.CardObject) (string, error) {
			sent = append(sent, c)
			return "msg-" + strconv.Itoa(len(sent)), nil
		},
		UpdateOwnerCard:      func(_ context.Context, _ string, _ card.CardObject) bool { return true },
		SendGroupTopic:       func(_ context.Context, _ string, _ string, _ bool) error { return nil },
		AddTypingReaction:    func(_ context.Context, _ string) (string, error) { return "r", nil },
		RemoveTypingReaction: func(_ context.Context, _ string, _ string) error { return nil },
		IsBoundProject:       func(_ string) bool { return true },
		FindProjectByCwd:     func(_ string) (*ProjectRef, error) { return nil, nil },
		CreateProjectForCwd:  func(_ string, _ string) (*ProjectRef, error) { return nil, nil },
		Presence:             func() (CliPresenceRoute, error) { return CliPresenceRoute{RouteToFeishu: true, Reason: "away"}, nil },
		LocalActivity:        func() (bool, error) { return false, nil },
	}
	svc := CreateCliBridgeService(deps)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start ipc server: %v", err)
	}
	defer svc.Shutdown(context.Background())

	msg := CliHookMessage{
		Type:          MsgTypePermissionRequest,
		Source:        AgentClaude,
		SessionID:     "sess-deny",
		Cwd:           "/proj",
		ToolName:      "Bash",
		ToolInput:     map[string]any{"command": "rm -rf /"},
		HookEventName: "PreToolUse",
	}

	agentRespCh := make(chan CliHookResponse, 1)
	agentErrCh := make(chan error, 1)
	go func() {
		resp, err := SendCliHookMessage(socket, msg)
		if err != nil {
			agentErrCh <- err
			return
		}
		agentRespCh <- resp
	}()

	pending := waitForPending(t, PendingPermission)
	if !svc.resolveAction(CLI.Deny, pending.ID) {
		t.Fatal("resolveAction(Deny) returned false")
	}

	select {
	case err := <-agentErrCh:
		t.Fatalf("agent send failed: %v", err)
	case resp := <-agentRespCh:
		if resp.Decision != DecisionDeny {
			t.Fatalf("agent expected deny, got %q", resp.Decision)
		}
		if !resp.Interrupt {
			t.Fatal("expected Interrupt=true on deny")
		}
		stdout := BuildHookStdout(msg, resp)
		if !strings.Contains(stdout, "deny") {
			t.Fatalf("hook stdout missing 'deny': %s", stdout)
		}
	}
}
