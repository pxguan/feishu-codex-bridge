package bot

import (
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func TestResolveThreadKey(t *testing.T) {
	if key := resolveThreadKey(TurnInput{ThreadID: "ot_1", ChatID: "oc_1"}); key != "ot_1" {
		t.Fatalf("multi key: %q", key)
	}
	if key := resolveThreadKey(TurnInput{ChatID: "oc_1"}); key != "oc_1" {
		t.Fatalf("single key: %q", key)
	}
}

func TestEffectiveMode(t *testing.T) {
	if effectiveMode(nil) != "full" {
		t.Fatal("nil → full")
	}
	p := &project.Project{Mode: "qa"}
	if effectiveMode(p) != "qa" {
		t.Fatal("qa")
	}
}

func TestEffectiveNetwork(t *testing.T) {
	if effectiveNetwork(nil) {
		t.Fatal("nil → false")
	}
	b := true
	p := &project.Project{Network: &b}
	if !effectiveNetwork(p) {
		t.Fatal("true → true")
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("hello", 10); got != "hello" {
		t.Fatal("short → as-is")
	}
	got := truncateStr("hello world this is long", 5)
	runes := []rune(got)
	if len(runes) != 6 { // 5 + …
		t.Fatalf("truncated len: %d %q", len(runes), got)
	}
}

func TestEvictLiveSession_NoPanic(t *testing.T) {
	o := newTestOrchestrator(t)
	o.EvictLiveSession("oc_1") // 空 sessions → no-op
}
