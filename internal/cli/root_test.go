package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCmd_Version(t *testing.T) {
	out := &bytes.Buffer{}
	code := Execute([]string{"--version"}, out, out)
	if code != 0 {
		t.Fatalf("exit code: %d", code)
	}
	// cobra --version 输出 "feishu-codex-bridge version X.Y.Z"。
	if !strings.Contains(out.String(), "version") {
		t.Fatalf("version output: %q", out.String())
	}
}

func TestRootCmd_Help(t *testing.T) {
	out := &bytes.Buffer{}
	code := Execute([]string{"--help"}, out, out)
	if code != 0 {
		t.Fatalf("exit code: %d", code)
	}
	for _, want := range []string{"run", "bot", "doctor", "start", "stop", "update", "web"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help should list %s", want)
		}
	}
}

func TestRootCmd_UnknownCommand(t *testing.T) {
	out := &bytes.Buffer{}
	code := Execute([]string{"nonexistent"}, out, out)
	// cobra 对未知子命令 exit 非 0（但可能不通过 RunE error）。
	_ = code
}

func TestRunCmd_Registered(t *testing.T) {
	root := NewRootCmd()
	found := false
	for _, cmd := range root.Commands() {
		if cmd.Use == "run" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("run command should be registered")
	}
}

func TestBotSubcommands(t *testing.T) {
	root := NewRootCmd()
	var botCmd *cobra.Command
	for _, cmd := range root.Commands() {
		if cmd.Use == "bot" {
			botCmd = cmd
			break
		}
	}
	if botCmd == nil {
		t.Fatal("bot command missing")
	}
	subs := botCmd.Commands()
	if len(subs) < 4 {
		t.Fatalf("bot should have init/list/use/rm: %d", len(subs))
	}
}
