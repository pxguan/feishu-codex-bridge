package bot

import (
	"path/filepath"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func setupProjects(t *testing.T, projects ...project.Project) *project.Store {
	t.Helper()
	s := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	for _, p := range projects {
		if err := s.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFindProjectByCwd_ExactMatch(t *testing.T) {
	dir := abs(t, t.TempDir())
	s := setupProjects(t,
		project.Project{Name: "p1", ChatID: "oc_1", Cwd: dir, Backend: "codex-appserver"},
	)
	p, ok := FindProjectByCwd(s, dir, "codex-appserver")
	if !ok || p.Name != "p1" {
		t.Fatalf("exact match: %+v ok=%v", p, ok)
	}
}

func TestFindProjectByCwd_BackendMismatch(t *testing.T) {
	dir := abs(t, t.TempDir())
	s := setupProjects(t,
		project.Project{Name: "p1", ChatID: "oc_1", Cwd: dir, Backend: "codex-appserver"},
	)
	// codex 项目 + 查 claude → 不命中。
	_, ok := FindProjectByCwd(s, dir, "claude-agent")
	if ok {
		t.Fatal("codex project should NOT match claude source")
	}
}

func TestFindProjectByCwd_SourceEmptyWildcard(t *testing.T) {
	dir := abs(t, t.TempDir())
	s := setupProjects(t,
		project.Project{Name: "p1", ChatID: "oc_1", Cwd: dir, Backend: "codex-appserver"},
	)
	// source 空 → 通配（notify-scope）。
	p, ok := FindProjectByCwd(s, dir, "")
	if !ok || p.Name != "p1" {
		t.Fatalf("wildcard: %+v ok=%v", p, ok)
	}
}

func TestFindProjectByCwd_LegacyDefaultCodex(t *testing.T) {
	dir := abs(t, t.TempDir())
	s := setupProjects(t,
		project.Project{Name: "p1", ChatID: "oc_1", Cwd: dir, Backend: ""}, // legacy 缺省
	)
	// legacy 缺省 backend → 默认 codex。
	p, ok := FindProjectByCwd(s, dir, "codex-appserver")
	if !ok || p.Name != "p1" {
		t.Fatalf("legacy default codex: %+v ok=%v", p, ok)
	}
}

func TestFindProjectByCwd_SubdirMatch(t *testing.T) {
	parent := abs(t, t.TempDir())
	s := setupProjects(t,
		project.Project{Name: "p1", ChatID: "oc_1", Cwd: parent, Backend: "codex-appserver"},
	)
	// cwd 在 parent 子树内。
	child := filepath.Join(parent, "src", "deep")
	p, ok := FindProjectByCwd(s, child, "codex-appserver")
	if !ok || p.Name != "p1" {
		t.Fatalf("subdir match: %+v ok=%v", p, ok)
	}
}

func TestFindProjectByCwd_NotFound(t *testing.T) {
	s := setupProjects(t,
		project.Project{Name: "p1", ChatID: "oc_1", Cwd: abs(t, "/some/path"), Backend: "codex-appserver"},
	)
	_, ok := FindProjectByCwd(s, "/completely/different", "codex-appserver")
	if ok {
		t.Fatal("unrelated cwd should not match")
	}
}

func TestMatchBackend(t *testing.T) {
	if !matchBackend("codex-appserver", "") {
		t.Fatal("empty source → wildcard")
	}
	if !matchBackend("", "codex-appserver") {
		t.Fatal("empty project backend → default codex")
	}
	if matchBackend("codex-appserver", "claude-agent") {
		t.Fatal("mismatch → false")
	}
	if !matchBackend("claude-agent", "claude-agent") {
		t.Fatal("exact match → true")
	}
	// 验证默认值常量。
	_ = agent.DEFAULT_BACKEND_ID
}

func TestIsSubdir(t *testing.T) {
	if !isSubdir("/a/b", "/a/b") {
		t.Fatal("self should be subdir")
	}
	if !isSubdir("/a/b/c", "/a/b") {
		t.Fatal("child should be subdir")
	}
	if isSubdir("/a/other", "/a/b") {
		t.Fatal("sibling should NOT be subdir")
	}
	if isSubdir("/a", "/a/b") {
		t.Fatal("parent should NOT be subdir of child")
	}
}
