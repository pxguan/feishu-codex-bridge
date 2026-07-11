package project

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func boolP(b bool) *bool { return &b }

// ── 纯函数 ───────────────────────────────────────────────────────

func TestDefaultNoMention(t *testing.T) {
	cases := []struct {
		name string
		p    Project
		want bool
	}{
		{"created multi（默认）", Project{}, true},
		{"created single", Project{Origin: "created", Kind: "single"}, true},
		{"joined multi", Project{Origin: "joined", Kind: "multi"}, true},
		{"joined single（唯一关）", Project{Origin: "joined", Kind: "single"}, false},
	}
	for _, c := range cases {
		if got := DefaultNoMention(c.p); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestEffectiveMode(t *testing.T) {
	if EffectiveMode(Project{}) != agent.PermissionFull {
		t.Fatal("empty mode → full")
	}
	if EffectiveMode(Project{Mode: agent.PermissionQA}) != agent.PermissionQA {
		t.Fatal("qa mode passthrough")
	}
}

func TestEffectiveGuestMode(t *testing.T) {
	if EffectiveGuestMode(Project{Mode: agent.PermissionQA}) != agent.PermissionQA {
		t.Fatal("empty guest → same as mode")
	}
	if EffectiveGuestMode(Project{Mode: agent.PermissionFull, GuestMode: agent.PermissionQA}) != agent.PermissionQA {
		t.Fatal("explicit guest mode")
	}
}

func TestTurnTier(t *testing.T) {
	p := Project{Mode: agent.PermissionFull, GuestMode: agent.PermissionQA}
	m, role, split := TurnTier(p, true)
	if m != agent.PermissionFull || role != "admin" || !split {
		t.Fatalf("admin tier wrong: mode=%v role=%v split=%v", m, role, split)
	}
	m, role, _ = TurnTier(p, false)
	if m != agent.PermissionQA || role != "guest" {
		t.Fatalf("guest tier wrong: mode=%v role=%v", m, role)
	}
	_, _, split = TurnTier(Project{Mode: agent.PermissionFull}, true)
	if split {
		t.Fatal("no guestMode → no split")
	}
}

// ── Store CRUD ──────────────────────────────────────────────────

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "projects.json"))
}

func TestStore_AddGetRemove(t *testing.T) {
	s := newTestStore(t)
	p := Project{Name: "alpha", ChatID: "oc_1", Cwd: "/proj/a", CreatedAt: 1}
	if err := s.Add(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetByName("alpha")
	if err != nil || got == nil || got.ChatID != "oc_1" {
		t.Fatalf("GetByName: %+v %v", got, err)
	}
	byChat, _ := s.GetByChatID("oc_1")
	if byChat == nil || byChat.Name != "alpha" {
		t.Fatalf("GetByChatID wrong: %+v", byChat)
	}
	removed, err := s.Remove("alpha")
	if err != nil || removed == nil || removed.Name != "alpha" {
		t.Fatalf("Remove: %+v %v", removed, err)
	}
	if got, _ := s.GetByName("alpha"); got != nil {
		t.Fatal("should be removed")
	}
}

func TestStore_AddDuplicateName(t *testing.T) {
	s := newTestStore(t)
	s.Add(Project{Name: "a", ChatID: "oc_1"})
	err := s.Add(Project{Name: "a", ChatID: "oc_2"})
	if !errors.Is(err, ErrProjectNameExists) {
		t.Fatalf("dup name should ErrProjectNameExists, got %v", err)
	}
}

func TestStore_AddDuplicateChat(t *testing.T) {
	s := newTestStore(t)
	s.Add(Project{Name: "a", ChatID: "oc_1"})
	err := s.Add(Project{Name: "b", ChatID: "oc_1"})
	if !errors.Is(err, ErrChatAlreadyBound) {
		t.Fatalf("dup chat should ErrChatAlreadyBound, got %v", err)
	}
}

func TestStore_Update(t *testing.T) {
	s := newTestStore(t)
	s.Add(Project{Name: "a", ChatID: "oc_1", Cwd: "/x"})
	// 函数式 updater（数组 append 不丢更新）。
	err := s.Update("a", func(p *Project) {
		p.AllowedUsers = append(p.AllowedUsers, "ou_1", "ou_2")
		p.NoMention = boolP(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByName("a")
	if len(got.AllowedUsers) != 2 || got.AllowedUsers[1] != "ou_2" || !*got.NoMention {
		t.Fatalf("update lost: %+v", got)
	}
	// 不存在的 name → no-op。
	if err := s.Update("nope", func(p *Project) {}); err != nil {
		t.Fatalf("update missing should be no-op, got %v", err)
	}
}

func TestStore_ListEmptyWhenNoFile(t *testing.T) {
	s := newTestStore(t)
	list, err := s.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("empty store should return nil-slice nil-err: %v %v", list, err)
	}
}

func TestStore_ConcurrentAddsNoLoss(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup
	const n = 20
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Add(Project{Name: "p" + string(rune('a'+i%26)) + itoaConcurrent(i), ChatID: "oc_" + itoaConcurrent(i)})
		}()
	}
	wg.Wait()
	list, _ := s.List()
	if len(list) != n {
		t.Fatalf("concurrent add lost: got %d want %d", len(list), n)
	}
}

func itoaConcurrent(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
