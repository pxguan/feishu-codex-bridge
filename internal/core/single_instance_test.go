package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 进程内契约（注入 probeLive/createTime），不依赖真实多进程。

func TestAcquire_NewFile_Success(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	inst, err := AcquirePIDLock(path, "app1")
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Release()
	if inst.PID() != os.Getpid() {
		t.Fatalf("pid = %d, want %d", inst.PID(), os.Getpid())
	}
}

func TestAcquire_ReleaseThenReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	inst, err := AcquirePIDLock(path, "app1")
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("release should unlink lock file")
	}
	inst2, err := AcquirePIDLock(path, "app1")
	if err != nil {
		t.Fatal(err)
	}
	defer inst2.Release()
}

func TestAcquire_SelfPIDResidual_Takeover(t *testing.T) {
	// 锁文件里记录的是「自己 pid」→ 视为残留，接管。
	path := filepath.Join(t.TempDir(), "lock")
	mustWrite(t, path, fmt.Sprintf(`{"pid":%d,"appId":"app1","startedAt":%d}`, os.Getpid(), time.Now().UnixMilli()))
	inst, err := AcquirePIDLock(path, "app1")
	if err != nil {
		t.Fatalf("self residual should be taken over, got %v", err)
	}
	defer inst.Release()
}

func TestAcquire_DeadSameApp_Takeover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	// 记录另一个 pid，注入 probeLive 返回「死」→ 接管。
	mustWrite(t, path, `{"pid":99999,"appId":"app1","startedAt":1000}`)
	inst, err := AcquirePIDLock(path, "app1", WithProbeLive(func(int, int64) (bool, error) { return false, nil }))
	if err != nil {
		t.Fatalf("dead same-app pid should be taken over, got %v", err)
	}
	defer inst.Release()
}

func TestAcquire_LiveSameApp_Rejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	mustWrite(t, path, `{"pid":99999,"appId":"app1","startedAt":1000}`)
	_, err := AcquirePIDLock(path, "app1",
		WithProbeLive(func(int, int64) (bool, error) { return true, nil }),
		WithAcquireAttempts(1),
	)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("live same-app pid should be rejected with ErrAlreadyRunning, got %v", err)
	}
}

func TestAcquire_LiveOtherApp_Rejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	mustWrite(t, path, `{"pid":99999,"appId":"other","startedAt":1000}`)
	_, err := AcquirePIDLock(path, "app1",
		WithProbeLive(func(int, int64) (bool, error) { return true, nil }),
		WithAcquireAttempts(1),
	)
	if !errors.Is(err, ErrHeldByOther) {
		t.Fatalf("live other-app pid should be rejected with ErrHeldByOther, got %v", err)
	}
}

func TestAcquire_DeadOtherApp_Takeover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	mustWrite(t, path, `{"pid":99999,"appId":"other","startedAt":1000}`)
	inst, err := AcquirePIDLock(path, "app1",
		WithProbeLive(func(int, int64) (bool, error) { return false, nil }),
	)
	if err != nil {
		t.Fatalf("dead other-app pid should be taken over, got %v", err)
	}
	defer inst.Release()
}

func TestAcquire_Corrupt_FailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	mustWrite(t, path, "{not json")
	_, err := AcquirePIDLock(path, "app1", WithAcquireAttempts(1))
	if !errors.Is(err, ErrCorruptLockFile) {
		t.Fatalf("corrupt lock file should fail-closed with ErrCorruptLockFile, got %v", err)
	}
}

func TestAcquire_PIDReuse_Takeover(t *testing.T) {
	// pid 复用：probeLive 注入「复用→false」，等价死 → 接管。
	path := filepath.Join(t.TempDir(), "lock")
	mustWrite(t, path, `{"pid":99999,"appId":"app1","startedAt":1000}`)
	inst, err := AcquirePIDLock(path, "app1",
		WithProbeLive(func(int, int64) (bool, error) { return false, nil }),
	)
	if err != nil {
		t.Fatalf("reused pid should be taken over, got %v", err)
	}
	defer inst.Release()
}

// ── 真子进程并发：4 进程抢同一锁，恰一个 ACQUIRED ────────────────

func TestMain(m *testing.M) {
	if os.Getenv("FCB_LOCK_CHILD") == "1" {
		os.Exit(runLockChild())
	}
	os.Exit(m.Run())
}

func runLockChild() int {
	path := os.Getenv("FCB_LOCK_PATH")
	appID := os.Getenv("FCB_LOCK_APP")
	inst, err := AcquirePIDLock(path, appID)
	if err != nil {
		fmt.Println("REJECTED")
		return 0
	}
	fmt.Println("ACQUIRED")
	time.Sleep(2 * time.Second) // 持有，让兄弟进程看到活持有者
	_ = inst.Release()
	return 0
}

func TestAcquire_Concurrent_RealChildProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrent child test skipped in -short")
	}
	path := filepath.Join(t.TempDir(), "proc.lock")
	bin, err := os.Executable()
	if err != nil {
		t.Skipf("cannot resolve test binary: %v", err)
	}

	const n = 4
	results := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := exec.Command(bin)
			c.Env = append(os.Environ(),
				"FCB_LOCK_CHILD=1",
				"FCB_LOCK_PATH="+path,
				"FCB_LOCK_APP=app-concurrent",
			)
			out, _ := c.CombinedOutput()
			results[i] = string(out)
		}()
	}
	wg.Wait()

	acq := 0
	for _, r := range results {
		if strings.HasPrefix(r, "ACQUIRED") {
			acq++
		}
	}
	if acq != 1 {
		t.Fatalf("concurrent acquire: ACQUIRED=%d, want exactly 1\noutputs=%q", acq, results)
	}
}
