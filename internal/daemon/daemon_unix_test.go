//go:build !windows

package daemon

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// canRunChildProcesses 探测当前环境子进程是否能正常推进（自然退出）。
// 部分容器/CI 会冻结子进程（sleep 1 永不超时），此时 spawn 类生命周期测试
// 会假死，应跳过。返回 false 时调用方应 t.Skip。
func canRunChildProcesses(t *testing.T) bool {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return false
	}
	pid := cmd.Process.Pid
	deadline := time.After(3 * time.Second)
	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		if cmd.Process.Signal(syscall.Signal(0)) != nil {
			return true // 已自然退出 → 子进程能推进
		}
		select {
		case <-deadline:
			t.Logf("环境冻结子进程（sleep 1 未退出），spawn 类测试跳过")
			return false
		default:
		}
	}
	_ = pid
	t.Logf("环境冻结子进程（sleep 1 未退出），spawn 类测试跳过")
	return false
}

// TestStartStop_SelfExiting 用会自行退出的子进程验证：start 能 detach 启动并写
// pid 文件、Status 在子进程自然退出后判定为未运行、Stop 清理 pid 文件。
func TestStartStop_SelfExiting(t *testing.T) {
	if !canRunChildProcesses(t) {
		t.Skip("环境冻结子进程，跳过")
	}
	m := tempManager(t)
	m.Self = "/bin/sleep"
	m.CommandArgs = []string{"1"}

	if err := m.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	info, _ := m.Status()
	if !info.Running {
		t.Fatal("status should report running right after start")
	}
	if _, err := os.Stat(m.PIDFile); err != nil {
		t.Fatal("pid file should exist after start")
	}

	// 等子进程自行退出（sleep 1）。
	deadline := time.After(5 * time.Second)
	for {
		info, _ = m.Status()
		if !info.Running {
			break
		}
		select {
		case <-deadline:
			t.Fatal("child did not exit on its own")
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Stop 应清理 pid 文件（子已死，无需 kill）。
	if err := m.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if _, err := os.Stat(m.PIDFile); err == nil {
		t.Fatal("pid file should be removed after stop")
	}
}

// TestStart_AlreadyRunning 验证第二次 start 返回 ErrAlreadyRunning。
func TestStart_AlreadyRunning(t *testing.T) {
	if !canRunChildProcesses(t) {
		t.Skip("环境冻结子进程，跳过")
	}
	m := tempManager(t)
	m.Self = "/bin/sleep"
	m.CommandArgs = []string{"30"}
	if err := m.Start(); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	defer m.Stop()
	time.Sleep(300 * time.Millisecond)
	if err := m.Start(); err == nil {
		t.Fatal("second start should return ErrAlreadyRunning")
	}
}

// TestFollowLogs 验证 follow 能收到启动后追加的日志行（纯文件操作，无 spawn）。
func TestFollowLogs(t *testing.T) {
	m := tempManager(t)
	if err := os.WriteFile(m.LogFile, []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := m.FollowLogs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(m.LogFile, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("new line 1\nnew line 2\n")
	f.Close()

	got := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case line := <-ch:
			got[line] = true
		case <-deadline:
			t.Fatalf("follow did not receive new lines, got=%v", got)
		}
	}
	if !got["new line 1"] || !got["new line 2"] {
		t.Fatalf("missing expected lines: %v", got)
	}
}
