//go:build !windows

package platform

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// proc_unix.go —— POSIX（macOS/Linux）进程组 + env 合并。

func envEnviron() []string { return os.Environ() }

func applySysProcAttr(cmd *exec.Cmd, opts SpawnOptions) {
	sys := &syscall.SysProcAttr{}
	if opts.Detached {
		sys.Setpgid = true
	}
	cmd.SysProcAttr = sys
}

func killGroup(pid int, sig syscall.Signal) error {
	// Setpgid 后子进程 pgid == pid；负 pid 表示杀整组。
	return syscall.Kill(-pid, sig)
}

func sigTerm() syscall.Signal { return syscall.SIGTERM }
func sigKill() syscall.Signal { return syscall.SIGKILL }

// MergeEnv 合并 base（KEY=VAL 切片）与 overrides；POSIX 大小写敏感。
func MergeEnv(base []string, overrides map[string]string) []string {
	m := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(m))
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		if _, exists := m[k]; !exists {
			order = append(order, k)
		}
		m[k] = v
	}
	for k, v := range overrides {
		if _, exists := m[k]; !exists {
			order = append(order, k)
		}
		m[k] = v
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+m[k])
	}
	return out
}
