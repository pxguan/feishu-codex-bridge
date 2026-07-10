package project

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// gitinfo.go —— 当前 git 分支（对齐 TS project/git-info）。
// 懒读（入站消息/run 结束时刷新公告横幅用）。

// CurrentBranch 返回 cwd 的当前 git 分支；非 git 仓库/detached(HEAD)/出错返回空。
func CurrentBranch(cwd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(string(out))
	if b == "" || b == "HEAD" {
		return ""
	}
	return b
}
