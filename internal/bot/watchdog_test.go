package bot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// ── Semaphore ───────────────────────────────────────────────────

func TestSemaphore_AcquireRelease(t *testing.T) {
	s := NewSemaphore(2)
	r1 := s.Acquire()
	r2 := s.Acquire()
	if s.HasFree() {
		t.Fatal("2 max + 2 active should be full (no free slot)")
	}
	r1()
	if !s.HasFree() {
		t.Fatal("release should free a slot")
	}
	r2()
}

func TestSemaphore_QueueAndCancel(t *testing.T) {
	s := NewSemaphore(1)
	r1 := s.Acquire()
	q := s.Enqueue(nil)
	if q.Position() != 1 {
		t.Fatalf("queued position should be 1: got %d", q.Position())
	}
	// 取消排队。
	if !q.Cancel() {
		t.Fatal("cancel should succeed while queued")
	}
	if _, ok := q.Wait(); ok {
		t.Fatal("cancelled wait should return false")
	}
	r1()
}

func TestSemaphore_QueueGrantedAfterRelease(t *testing.T) {
	s := NewSemaphore(1)
	r1 := s.Acquire()
	q := s.Enqueue(nil)
	done := make(chan bool)
	go func() {
		release, ok := q.Wait()
		if ok {
			release()
			done <- true
		} else {
			done <- false
		}
	}()
	r1() // 释放 → 排队的被授予。
	if !<-done {
		t.Fatal("queued should be granted after release")
	}
}

// ── GracefulInterrupt ───────────────────────────────────────────

func TestGracefulInterrupt_NormalAbort(t *testing.T) {
	aborted := ""
	forced := false
	g := NewGracefulInterrupt(
		func() string { return "turn_1" },
		func(tid string) { aborted = tid },
		func() { forced = true },
		1000,
	)
	g.Interrupt()
	if aborted != "turn_1" {
		t.Fatalf("should abort turn_1: got %q", aborted)
	}
	if forced {
		t.Fatal("should not force-stop immediately (timer)")
	}
	if !g.Interrupted() {
		t.Fatal("should be interrupted")
	}
	g.Dispose()
}

func TestGracefulInterrupt_NoTurnIDForces(t *testing.T) {
	forced := false
	g := NewGracefulInterrupt(
		func() string { return "" },
		func(string) {},
		func() { forced = true },
		1000,
	)
	g.Interrupt()
	if !forced {
		t.Fatal("no turnID should force-stop immediately")
	}
	if !g.Forced() {
		t.Fatal("should be forced")
	}
}

func TestGracefulInterrupt_Idempotent(t *testing.T) {
	count := 0
	g := NewGracefulInterrupt(
		func() string { return "t" },
		func(string) { count++ },
		func() {},
		1000,
	)
	g.Interrupt()
	g.Interrupt()
	if count != 1 {
		t.Fatalf("abort should fire once (idempotent): got %d", count)
	}
}

func TestGracefulInterrupt_DisposeStopsTimer(t *testing.T) {
	forced := false
	g := NewGracefulInterrupt(
		func() string { return "t" },
		func(string) {},
		func() { forced = true },
		50, // 50ms 超时
	)
	g.Interrupt()
	g.Dispose() // 立即 dispose → 定时器停止
	time.Sleep(100 * time.Millisecond)
	if forced {
		t.Fatal("dispose should stop timer, no force-stop")
	}
}

// ── SessionStore ────────────────────────────────────────────────

func newTestSessionStore(t *testing.T) *SessionStore {
	t.Helper()
	return NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
}

func TestSessionStore_UpsertGet(t *testing.T) {
	s := newTestSessionStore(t)
	rec := SessionRecord{ThreadID: "t1", ChatID: "c1", Cwd: "/p", SessionID: "s1", Backend: "codex-appserver", CreatedAt: 1}
	if err := s.Upsert(rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("t1")
	if err != nil || got == nil || got.SessionID != "s1" {
		t.Fatalf("Get: %+v %v", got, err)
	}
}

func TestSessionStore_Patch(t *testing.T) {
	s := newTestSessionStore(t)
	s.Upsert(SessionRecord{ThreadID: "t1", ChatID: "c1", SessionID: "s1", Backend: "codex-appserver", CreatedAt: 1})
	s.Patch("t1", func(r *SessionRecord) {
		r.Model = "gpt-5"
		r.Summary = "updated"
	})
	got, _ := s.Get("t1")
	if got.Model != "gpt-5" || got.Summary != "updated" {
		t.Fatalf("patch lost: %+v", got)
	}
}

func TestSessionStore_MigrateV1(t *testing.T) {
	dir := t.TempDir()
	// v1 文件（codexThreadId 字段，无 backend）。
	path := filepath.Join(dir, "sessions.json")
	v1Content := `{"version":1,"sessions":[{"threadId":"t1","chatId":"c1","cwd":"/p","codexThreadId":"legacy_s1","createdAt":1}]}`
	os.WriteFile(path, []byte(v1Content), 0o600)
	store := NewSessionStore(path)
	sessions, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session: %d", len(sessions))
	}
	if sessions[0].SessionID != "legacy_s1" {
		t.Fatalf("v1 codexThreadId should migrate to sessionId: %q", sessions[0].SessionID)
	}
	if sessions[0].Backend != agent.DEFAULT_BACKEND_ID {
		t.Fatalf("missing backend should default to codex: %q", sessions[0].Backend)
	}
}

func TestSessionStore_NotFound(t *testing.T) {
	s := newTestSessionStore(t)
	got, _ := s.Get("nope")
	if got != nil {
		t.Fatal("missing thread should return nil")
	}
}
