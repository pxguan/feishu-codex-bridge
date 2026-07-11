//go:build !windows

package codex

import (
	"os/exec"
	"syscall"
	"time"
)

// client_proc_unix.go —— POSIX：子进程独立进程组（Setpgid）+ 整组 SIGTERM→SIGKILL。

func setChildSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

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
