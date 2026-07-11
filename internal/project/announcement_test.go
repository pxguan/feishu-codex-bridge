package project

import (
	"strings"
	"testing"
)

func TestAnnouncementText_Full(t *testing.T) {
	p := Project{Name: "my-project", Cwd: "/home/user/proj"}
	got := AnnouncementText(p, "main")
	if !strings.Contains(got, "📁 my-project") {
		t.Fatalf("missing name: %q", got)
	}
	if !strings.Contains(got, "📣 /home/user/proj") {
		t.Fatalf("missing cwd: %q", got)
	}
	if !strings.Contains(got, "🌿 main") {
		t.Fatalf("missing branch: %q", got)
	}
	if !strings.Contains(got, " · ") {
		t.Fatalf("should use · separator: %q", got)
	}
}

func TestAnnouncementText_NoBranch(t *testing.T) {
	p := Project{Name: "x", Cwd: "/x"}
	got := AnnouncementText(p, "")
	if strings.Contains(got, "🌿") {
		t.Fatalf("no branch should not show 🌿: %q", got)
	}
}

func TestAnnouncementText_Empty(t *testing.T) {
	got := AnnouncementText(Project{}, "")
	if got != "" {
		t.Fatalf("empty project → empty: %q", got)
	}
}

func TestShouldRefreshBranch(t *testing.T) {
	if !ShouldRefreshBranch("main", "dev") {
		t.Fatal("different → refresh")
	}
	if ShouldRefreshBranch("main", "main") {
		t.Fatal("same → no refresh")
	}
	if !ShouldRefreshBranch("", "main") {
		t.Fatal("empty → refresh")
	}
}
