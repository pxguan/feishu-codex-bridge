package project

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCurrentBranch_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	if got := CurrentBranch(dir); got != "" {
		t.Fatalf("non-git dir should return empty, got %q", got)
	}
}

func TestCurrentBranch_GitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	// git init + 配置 + 一个 commit 让 HEAD 指向分支（非 unborn）。
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"checkout", "-q", "-b", "main"},
	} {
		if err := exec.Command("git", append([]string{"-C", dir}, args...)...).Run(); err != nil {
			t.Skipf("git setup failed: %v", err)
		}
	}
	if err := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", "init").Run(); err != nil {
		t.Skipf("git commit failed: %v", err)
	}
	if got := CurrentBranch(dir); got != "main" {
		t.Fatalf("CurrentBranch = %q, want main", got)
	}
}

func TestCurrentBranch_RelativePathFile(t *testing.T) {
	// 确保 cwd 是目录路径而非文件（防御）。
	if got := CurrentBranch(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Fatalf("non-existent dir should return empty, got %q", got)
	}
}
