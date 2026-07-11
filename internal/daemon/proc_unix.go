//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
	"time"
)

// startCommand POSIX：Setpgid 让子进程成为独立进程组组长（pgid==pid），
// 父进程退出后子进程由 init 接管继续运行；killTree(-pid) 可杀整棵树。
// 注：环境（部分容器/CI）不允许 Setsid 系统调用，故仅用 Setpgid 即可满足
// daemon 脱离 + 整组kill 的需求。
func startCommand(self string, args []string) *exec.Cmd {
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// killTree 杀整个进程组（含 run 拉起的 codex/claude 孙进程）。
// Setsid 后子进程是组 leader，pgid == pid，所以 -pid 覆盖全树。
func killTree(pid int, grace time.Duration) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if grace > 0 {
		time.Sleep(grace)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
