package claude

import (
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// TestClaudeEffortMapping 锁定 claude CLI `--effort` 取值映射：
// claude 合法值为 low/medium/high/xhigh/max；ultra→max；none/minimal/空不传。
func TestClaudeEffortMapping(t *testing.T) {
	cases := []struct {
		in   agent.ReasoningEffort
		want string
	}{
		{agent.EffortNone, ""},
		{agent.EffortMinimal, ""},
		{agent.EffortLow, "low"},
		{agent.EffortMedium, "medium"},
		{agent.EffortHigh, "high"},
		{agent.EffortXhigh, "xhigh"},
		{agent.EffortMax, "max"},
		{agent.EffortUltra, "max"}, // claude 无 ultra → max
		{"", ""},
	}
	for _, c := range cases {
		if got := claudeEffort(c.in); got != c.want {
			t.Errorf("claudeEffort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBuildArgsIncludesEffort 确认 buildArgs 在 effort 有效时注入 --effort。
func TestBuildArgsIncludesEffort(t *testing.T) {
	args := buildArgs(nil, "sonnet", "", "", "hi", agent.EffortHigh, false)
	found := false
	for i, a := range args {
		if a == "--effort" && i+1 < len(args) && args[i+1] == "high" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --effort high in %v", args)
	}

	// none → 不应出现 --effort
	argsNone := buildArgs(nil, "sonnet", "", "", "hi", agent.EffortNone, false)
	for _, a := range argsNone {
		if a == "--effort" {
			t.Fatalf("did not expect --effort in %v", argsNone)
		}
	}
}
