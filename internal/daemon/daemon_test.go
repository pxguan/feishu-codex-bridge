package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return &Manager{
		PIDFile:    filepath.Join(dir, "daemon.pid"),
		LogFile:    filepath.Join(dir, "service.log"),
		ErrLogFile: filepath.Join(dir, "service.err.log"),
	}
}

func TestStatus_MissingPIDFile_NotRunning(t *testing.T) {
	m := tempManager(t)
	info, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if info.Running {
		t.Fatal("missing pid file should report not running")
	}
}

func TestTailLines(t *testing.T) {
	m := tempManager(t)
	content := strings.Repeat("line\n", 20)
	if err := os.WriteFile(m.LogFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := tailLines(m.LogFile, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
}
