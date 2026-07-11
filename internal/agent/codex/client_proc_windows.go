//go:build windows

package codex

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// client_proc_windows.go —— Windows：CREATE_NEW_PROCESS_GROUP + taskkill /T /F（杀整树含 MCP 孙进程）。

func setChildSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
}

func killTree(pid int, _ time.Duration) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
