package platform

import (
	"fmt"
	"io"
	"os/exec"
	"time"
)

// spawn.go —— 跨平台进程 spawn + 进程组 kill（对齐 TS platform/spawn）。
//
//   - Spawn：os/exec 封装；Windows 隐藏控制台窗口、POSIX 可选 detached 进程组。
//   - KillProcessGroup：SIGTERM 整组 → grace → SIGKILL 整组（POSIX），Windows taskkill /T /F。
//     接管 cmd.Wait()（Wait 只能调一次）。
//   - MergeEnv：合并 os.Environ() 与 overrides；Windows 大小写不敏感去重（Path≡PATH）。

// SpawnOptions spawn 行为。
type SpawnOptions struct {
	Env         map[string]string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	WindowsHide bool // Windows 隐藏控制台（POSIX 忽略）
	Detached    bool // POSIX 设独立进程组（Setpgid），便于整组 kill
}

// Spawn 启动子进程并返回 *exec.Cmd（已 Start）。
func Spawn(name string, args []string, opts SpawnOptions) (*exec.Cmd, error) {
	cmd := exec.Command(name, args...)
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}
	cmd.Env = MergeEnv(envEnviron(), opts.Env)
	applySysProcAttr(cmd, opts)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", name, err)
	}
	return cmd, nil
}

// KillProcessGroup 杀子进程整组：先 SIGTERM，等 grace 后 SIGKILL；接管 Wait()。
// cmd 已是 Spawn 返回（带 Setpgid 时 pgid==pid）。
func KillProcessGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	_ = killGroup(pid, sigTerm())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(grace):
		_ = killGroup(pid, sigKill())
		return <-done
	}
}
