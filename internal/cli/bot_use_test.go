package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/spf13/cobra"
)

func writeTestRegistry(t *testing.T, home string, bots []config.BotEntry) {
	t.Helper()
	dir := filepath.Join(home, ".feishu-codex-bridge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := config.BotsRegistry{Version: 1, Bots: bots}
	b, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bots.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func sampleBots() []config.BotEntry {
	return []config.BotEntry{
		{Name: "alpha", AppID: "cli_aaa", Tenant: config.TenantLark, CreatedAt: 1},
		{Name: "beta", AppID: "cli_bbb", Tenant: config.TenantLark, CreatedAt: 2},
		{Name: "gamma", AppID: "cli_ccc", Tenant: config.TenantLark, CreatedAt: 3},
	}
}

func newUseCmd() *cobra.Command {
	c := &cobra.Command{Use: "use [names...]", RunE: botUse}
	c.Flags().Bool("all", false, "")
	c.Flags().Bool("none", false, "")
	return c
}

func TestInteractivePickBots(t *testing.T) {
	reg := config.BotsRegistry{Version: 1, Bots: sampleBots()}
	allIDs := []string{"cli_aaa", "cli_bbb", "cli_ccc"}

	cases := []struct {
		name  string
		input string
		want  []string
		err   bool
	}{
		{"all", "all\n", allIDs, false},
		{"none", "none\n", nil, false},
		{"numbered", "1,3\n", []string{"cli_aaa", "cli_ccc"}, false},
		{"single", "2\n", []string{"cli_bbb"}, false},
		{"out of range", "9\n", nil, true},
		{"bad token", "x\n", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := interactivePickBots(&out, strings.NewReader(tc.input), reg)
			if tc.err {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestBotUse_FlagsAndNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTestRegistry(t, home, sampleBots())

	assertActive := func(t *testing.T, want ...string) {
		t.Helper()
		reg, err := config.LoadBots()
		if err != nil {
			t.Fatal(err)
		}
		active := map[string]bool{}
		for _, b := range reg.Bots {
			active[b.AppID] = b.Active != nil && *b.Active
		}
		wantSet := map[string]bool{}
		for _, w := range want {
			wantSet[w] = true
		}
		for id, on := range active {
			if on != wantSet[id] {
				t.Fatalf("bot %s active=%v want %v", id, on, wantSet[id])
			}
		}
	}

	t.Run("all", func(t *testing.T) {
		cmd := newUseCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--all"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		assertActive(t, "cli_aaa", "cli_bbb", "cli_ccc")
	})

	t.Run("none", func(t *testing.T) {
		cmd := newUseCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--none"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		assertActive(t)
	})

	t.Run("by name and appId", func(t *testing.T) {
		cmd := newUseCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"alpha", "cli_ccc"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		assertActive(t, "cli_aaa", "cli_ccc")
	})

	t.Run("unknown name errors", func(t *testing.T) {
		cmd := newUseCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"ghost"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected error for unknown bot name")
		}
	})

	t.Run("no args non-tty errors", func(t *testing.T) {
		cmd := newUseCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{})
		// 测试环境 stdin 非 TTY → 应报错提示用法。
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected error when no args and non-tty")
		}
	})
}
