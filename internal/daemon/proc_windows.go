//go:build windows

package daemon

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// startCommand Windows：CREATE_NEW_PROCESS_GROUP 让子进程独立成组，
// 父进程退出不连带终止；不继承控制台窗口。
func startCommand(self string, args []string) *exec.Cmd {
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
	return cmd
}

// killTree 用 taskkill /T /F 杀整棵树（含孙进程）。
func killTree(pid int, grace time.Duration) {
	if pid <= 0 {
		return
	}
	if grace > 0 {
		_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		time.Sleep(grace)
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
