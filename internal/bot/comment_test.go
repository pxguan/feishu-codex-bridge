package bot

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func newStoreWith(t *testing.T, projects ...project.Project) *project.Store {
	t.Helper()
	st := project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
	for _, p := range projects {
		if err := st.Add(p); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	return st
}

func TestResolveCommentProject_Single(t *testing.T) {
	st := newStoreWith(t, project.Project{Name: "only", Cwd: "/tmp/only"})
	o := &Orchestrator{ProjectStore: st}
	p, err := o.resolveCommentProject(context.Background(), "docxXYZ")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.Name != "only" {
		t.Fatalf("want only, got %s", p.Name)
	}
}

func TestResolveCommentProject_BySourceURL(t *testing.T) {
	st := newStoreWith(t,
		project.Project{Name: "a", Cwd: "/tmp/a", SourceURL: "https://feishu.cn/docx/AAA"},
		project.Project{Name: "b", Cwd: "/tmp/b", SourceURL: "https://feishu.cn/docx/BBB"},
	)
	o := &Orchestrator{ProjectStore: st}
	p, err := o.resolveCommentProject(context.Background(), "BBB")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.Name != "b" {
		t.Fatalf("want b, got %s", p.Name)
	}
}

func TestResolveCommentProject_MultiWithoutSourceURL_Ambiguous(t *testing.T) {
	st := newStoreWith(t,
		project.Project{Name: "a", Cwd: "/tmp/a"},
		project.Project{Name: "b", Cwd: "/tmp/b"},
	)
	o := &Orchestrator{ProjectStore: st}
	_, err := o.resolveCommentProject(context.Background(), "docxXYZ")
	if err == nil {
		t.Fatal("多项目未配置 SourceURL 应返回错误")
	}
}

func TestResolveCommentProject_Empty(t *testing.T) {
	o := &Orchestrator{ProjectStore: newStoreWith(t)}
	_, err := o.resolveCommentProject(context.Background(), "docxXYZ")
	if err == nil {
		t.Fatal("无项目应返回错误")
	}
}
