package claude

import (
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestPermissionFlags_Full(t *testing.T) {
	args := permissionFlags(agent.PermissionFull, true)
	if !contains(args, "--permission-mode") || !contains(args, "bypassPermissions") {
		t.Fatalf("full must use bypassPermissions: %v", args)
	}
	if !contains(args, "--dangerously-skip-permissions") {
		t.Fatalf("full must skip sandbox: %v", args)
	}
}

func TestPermissionFlags_QA(t *testing.T) {
	args := permissionFlags(agent.PermissionQA, true)
	if contains(args, "--dangerously-skip-permissions") {
		t.Fatalf("qa must NOT skip sandbox: %v", args)
	}
	idx := indexOf(args, "--disallowed-tools")
	if idx < 0 {
		t.Fatalf("qa must disallow write tools: %v", args)
	}
	if !strings.Contains(args[idx+1], "Write") || !strings.Contains(args[idx+1], "Edit") {
		t.Fatalf("qa must strip Write/Edit: %v", args)
	}
}

func TestPermissionFlags_OfflineWrite(t *testing.T) {
	args := permissionFlags(agent.PermissionWrite, false)
	idx := indexOf(args, "--disallowed-tools")
	if idx < 0 {
		t.Fatalf("offline must disallow network tools: %v", args)
	}
	if !strings.Contains(args[idx+1], "WebFetch") || !strings.Contains(args[idx+1], "WebSearch") {
		t.Fatalf("offline must strip WebFetch/WebSearch: %v", args)
	}
}

func TestPermissionFlags_OnlineWrite(t *testing.T) {
	args := permissionFlags(agent.PermissionWrite, true)
	if contains(args, "--disallowed-tools") {
		t.Fatalf("online write must keep network tools: %v", args)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
