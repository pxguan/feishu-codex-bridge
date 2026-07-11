package utils

import "testing"

func TestOpenURL_NonTTYDoesNotOpen(t *testing.T) {
	old := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	defer func() { stdinIsTTY = old }()
	if OpenURL("https://example.com") {
		t.Fatal("non-TTY should not attempt to open browser")
	}
}

func TestOpenCommand_NonEmptyForCurrentPlatform(t *testing.T) {
	cmd, args := openCommand("https://example.com")
	if cmd == "" || len(args) == 0 {
		t.Fatalf("openCommand returned empty for current platform: cmd=%q args=%v", cmd, args)
	}
}
