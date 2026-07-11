package project

import (
	"path/filepath"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestAssertBackendUsable_EmptyPasses(t *testing.T) {
	if err := AssertBackendUsable("", agent.PermissionFull, func(agent.BackendCatalogEntry) bool { return true }); err != nil {
		t.Fatal("empty backend should pass")
	}
}

func TestAssertBackendUsable_CodexAlwaysAvailable(t *testing.T) {
	// codex external-cli 恒可用（ProjectCreatableBackends 特判）。
	err := AssertBackendUsable(agent.DEFAULT_BACKEND_ID, agent.PermissionFull, func(agent.BackendCatalogEntry) bool { return false })
	if err != nil {
		t.Fatalf("codex should always be available: %v", err)
	}
}

func TestAssertBackendUsable_NotInstalledRejected(t *testing.T) {
	// claude 后端未装 → 拒。
	err := AssertBackendUsable("claude-agent", agent.PermissionFull, func(agent.BackendCatalogEntry) bool { return false })
	if err == nil {
		t.Fatal("uninstalled claude should be rejected")
	}
}

func TestAssertBackendUsable_InstalledPasses(t *testing.T) {
	err := AssertBackendUsable("claude-agent", agent.PermissionFull, func(e agent.BackendCatalogEntry) bool { return e.ID == "claude-agent" })
	if err != nil {
		t.Fatalf("installed claude should pass: %v", err)
	}
}

func TestResolveCwd_ExistingPath(t *testing.T) {
	dir := t.TempDir()
	cwd, blank, err := ResolveCwd("test", dir, "/projects")
	if err != nil {
		t.Fatal(err)
	}
	if cwd != dir || blank {
		t.Fatalf("existing path: cwd=%q blank=%v", cwd, blank)
	}
}

func TestResolveCwd_NonExistingPath(t *testing.T) {
	_, _, err := ResolveCwd("test", "/nonexistent/path/xyz", "/projects")
	if err == nil {
		t.Fatal("non-existing path should error")
	}
}

func TestResolveCwd_BlankProject(t *testing.T) {
	cwd, blank, err := ResolveCwd("myproject", "", "/projects")
	if err != nil {
		t.Fatal(err)
	}
	if !blank || cwd != filepath.Join("/projects", "myproject") {
		t.Fatalf("blank project: cwd=%q blank=%v", cwd, blank)
	}
}

func TestValidateCreateProjectInput_EmptyName(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "p.json"))
	if err := ValidateCreateProjectInput(s, "  "); err == nil {
		t.Fatal("empty name should error")
	}
}

func TestValidateCreateProjectInput_DuplicateName(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "p.json"))
	s.Add(Project{Name: "exists", ChatID: "oc_1"})
	if err := ValidateCreateProjectInput(s, "exists"); err == nil {
		t.Fatal("duplicate name should error")
	}
}

func TestValidateCreateProjectInput_OK(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "p.json"))
	if err := ValidateCreateProjectInput(s, "new"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateJoinGroupInput_ChatAlreadyBound(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "p.json"))
	s.Add(Project{Name: "existing", ChatID: "oc_1"})
	if err := ValidateJoinGroupInput(s, "new", "oc_1"); err == nil {
		t.Fatal("already-bound chat should error")
	}
}
