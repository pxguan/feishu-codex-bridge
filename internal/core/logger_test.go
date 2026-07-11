package core

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewTraceID_LengthCharsetUnique(t *testing.T) {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	seen := make(map[string]bool, 2000)
	for i := 0; i < 2000; i++ {
		id := NewTraceID()
		if len(id) != traceIDLen {
			t.Fatalf("trace id len = %d, want %d (%q)", len(id), traceIDLen, id)
		}
		for _, c := range id {
			if !strings.ContainsRune(digits, c) {
				t.Fatalf("trace id %q has invalid char %q", id, c)
			}
		}
		if seen[id] {
			t.Fatalf("trace id collision within 2000 draws: %q", id)
		}
		seen[id] = true
	}
}

func TestWithTrace_Roundtrip(t *testing.T) {
	ctx := WithTrace(context.Background(), "abc12345", "oc_chat", "om_msg")
	tid, cid, mid, ok := TraceFields(ctx)
	if !ok || tid != "abc12345" || cid != "oc_chat" || mid != "om_msg" {
		t.Fatalf("TraceFields = (%q,%q,%q,%v), want roundtrip", tid, cid, mid, ok)
	}
}

func TestTraceFields_Empty(t *testing.T) {
	if _, _, _, ok := TraceFields(context.Background()); ok {
		t.Fatal("empty ctx should yield ok=false")
	}
}

func TestWithTrace_PartialUpdateKeepsPrior(t *testing.T) {
	// 契约：空串不覆盖（允许链式部分更新）。
	ctx := WithTrace(context.Background(), "trace1", "", "")
	ctx = WithTrace(ctx, "", "chat1", "")
	tid, cid, _, _ := TraceFields(ctx)
	if tid != "trace1" || cid != "chat1" {
		t.Fatalf("partial update lost prior value: tid=%q cid=%q", tid, cid)
	}
}

func TestTodayLogFile_Format(t *testing.T) {
	p := TodayLogFile("/tmp/logs")
	wantSuffix := time.Now().Format(dateFormat) + ".log"
	if !strings.HasSuffix(p, wantSuffix) {
		t.Fatalf("TodayLogFile = %q, want suffix %q", p, wantSuffix)
	}
}

func TestInitFileLogging_WritesFileWithTrace(t *testing.T) {
	dir := t.TempDir()
	if _, err := InitFileLogging(dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetLogger(newDefaultLogger()) })

	ctx := WithTrace(context.Background(), "traceABCD", "oc_chat1", "")
	Info(ctx, "test_event", "test_phase", "hello world")

	data, err := os.ReadFile(TodayLogFile(dir))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "test_event") || !strings.Contains(string(data), "hello world") {
		t.Fatalf("log file missing event/msg: %s", data)
	}
	// 最后一行必须是合法 JSON 且含 traceId/event/phase 字段。
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("last log line not JSON: %s\nerr: %v", lines[len(lines)-1], err)
	}
	if rec["traceId"] != "traceABCD" {
		t.Fatalf("traceId not injected by traceHandler: %v", rec["traceId"])
	}
	if rec["event"] != "test_event" || rec["phase"] != "test_phase" {
		t.Fatalf("event/phase field wrong: event=%v phase=%v", rec["event"], rec["phase"])
	}
	if rec["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", rec["level"])
	}
}

func TestGCOldLogs_DeletesOldKeepsRecent(t *testing.T) {
	dir := t.TempDir()
	old := "2000-01-01.log"
	recent := time.Now().Format(dateFormat) + ".log"
	mustWrite(t, filepath.Join(dir, old), "old")
	mustWrite(t, filepath.Join(dir, recent), "new")

	removed, err := GCOldLogs(dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
		t.Fatal("old log not deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, recent)); err != nil {
		t.Fatalf("recent log missing after GC: %v", err)
	}
}

func TestGCOldLogs_IgnoresNonDateFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "service.log"), "keep") // 非日期名，保留
	mustWrite(t, filepath.Join(dir, "2000-01-01.log"), "old")
	removed, _ := GCOldLogs(dir, 7)
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only dated file)", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "service.log")); err != nil {
		t.Fatal("non-dated file should not be touched")
	}
}

func TestReadRecentLogs_TailToday(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format(dateFormat) + ".log"
	mustWrite(t, filepath.Join(dir, today), strings.Repeat("a", 4096))
	out, err := ReadRecentLogs(dir, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1024 || string(out) != strings.Repeat("a", 1024) {
		t.Fatalf("tail content/len mismatch: len=%d", len(out))
	}
}

func TestReadRecentLogs_FillsFromYesterday(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format(dateFormat) + ".log"
	yesterday := time.Now().AddDate(0, 0, -1).Format(dateFormat) + ".log"
	mustWrite(t, filepath.Join(dir, today), "TODAY")
	mustWrite(t, filepath.Join(dir, yesterday), "YESTERDAY")
	out, err := ReadRecentLogs(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "YESTERDAY") || !strings.Contains(s, "TODAY") {
		t.Fatalf("expected yesterday-then-today, got %q", s)
	}
}

func TestListLogFiles_SortedAscIgnoresNoise(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"2026-07-03", "2026-07-01", "2026-07-02"} {
		mustWrite(t, filepath.Join(dir, d+".log"), "x")
	}
	mustWrite(t, filepath.Join(dir, "not-a-date.log"), "x")
	days, err := ListLogFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-07-01", "2026-07-02", "2026-07-03"}
	if len(days) != 3 || days[0] != want[0] || days[2] != want[2] {
		t.Fatalf("ListLogFiles = %v, want %v", days, want)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
