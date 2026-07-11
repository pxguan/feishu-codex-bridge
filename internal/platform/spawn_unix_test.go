//go:build !windows

package platform

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"time"
)

// POSIX：detached 进程组 spawn + KillProcessGroup + env 注入。

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func TestSpawn_DetachedAndKillGroup(t *testing.T) {
	cmd, err := Spawn("sleep", []string{"30"}, SpawnOptions{Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	if !processAlive(cmd.Process.Pid) {
		t.Fatal("child should be alive right after spawn")
	}
	// KillProcessGroup 接管 Wait；被信号杀的进程 Wait 返回 *ExitError，非 nil 但属预期。
	_ = KillProcessGroup(cmd, time.Second)
	if processAlive(cmd.Process.Pid) {
		t.Fatal("child should be killed after KillProcessGroup")
	}
}

func TestSpawn_EnvOverrideApplied(t *testing.T) {
	var out bytes.Buffer
	cmd, err := Spawn("sh", []string{"-c", "printf %s $FCB_SPAWN_TEST_VAR"}, SpawnOptions{
		Env:    map[string]string{"FCB_SPAWN_TEST_VAR": "hello-env"},
		Stdout: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello-env" {
		t.Fatalf("env override not applied to child: %q", out.String())
	}
}

func TestKillProcessGroup_NilSafe(t *testing.T) {
	// nil cmd 不应 panic。
	if err := KillProcessGroup(nil, time.Second); err != nil {
		t.Fatalf("nil cmd should be no-op, got %v", err)
	}
}
