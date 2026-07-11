package claude

// acceptance_test.go —— 真实机验收（real-machine acceptance）。
//
// 驱动 Go Claude 后端走完整的「backend → thread → claude 子进程 → 事件归一化」闭环，
// 针对本机真实 claude CLI 跑一个最小可用对话（含 resume 续会话）。
//
// 默认跳过：CI / 沙箱无法真正起 claude 子进程，且会消耗 token、依赖本机登录态。
// 在已登录且 claude 网关可用的本机显式开启：
//
//	RUN_CLAUDE_ACCEPTANCE=1 go test ./internal/agent/claude/ -run TestClaudeBackendAcceptance -v
//
// 可选环境变量：
//   - CLAUDE_ACCEPTANCE_TIMEOUT：单轮超时（默认 120s），沙箱里想快速看 Go 侧管线可设小值。
//   - CLAUDE_ACCEPTANCE_MODEL：覆盖默认模型。
//   - CLAUDE_ACCEPTANCE_DIR：覆盖 cwd（默认用 t.TempDir，不碰用户项目）。

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func acceptanceTimeout() time.Duration {
	if v := os.Getenv("CLAUDE_ACCEPTANCE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 120 * time.Second
}

func TestClaudeBackendAcceptance(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_ACCEPTANCE") != "1" {
		t.Skip("set RUN_CLAUDE_ACCEPTANCE=1 to run real claude CLI acceptance")
	}
	bin := ResolveClaudeBin(false)
	if bin == "" {
		t.Skip("claude CLI not installed (install @anthropic-ai/claude-code and run `claude`)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), acceptanceTimeout())
	defer cancel()

	backend := &ClaudeBackend{}
	if !backend.IsAvailable(ctx) {
		t.Fatal("claude backend not available (not logged in / no ANTHROPIC_API_KEY / gateway unreachable)")
	}

	cwd := os.Getenv("CLAUDE_ACCEPTANCE_DIR")
	if cwd == "" {
		cwd = t.TempDir()
	}
	model := os.Getenv("CLAUDE_ACCEPTANCE_MODEL")

	// ── 第一轮：最小闭环 ──────────────────────────────────────────
	th, err := backend.StartThread(ctx, agent.StartThreadOptions{
		Cwd:     cwd,
		Model:   model,
		Mode:    agent.PermissionQA, // 只读 + 限工具，最安全
		Network: false,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer th.Close(ctx)

	run := th.RunStreamed(ctx, agent.AgentInput{Text: "Reply with exactly the single word: pong"}, nil)
	turnID := run.TurnID()

	var (
		gotTurnStart bool
		gotDone      bool
		gotError     bool
		errMsg       string
		sb           strings.Builder
	)
	for ev := range run.Events {
		switch ev.Type {
		case agent.EvTurnStarted:
			gotTurnStart = true
		case agent.EvText, agent.EvTextDelta:
			sb.WriteString(ev.Text)
			sb.WriteString(ev.Delta)
		case agent.EvDone:
			gotDone = true
		case agent.EvError:
			gotError = true
			errMsg = ev.Message
		}
	}
	if !gotTurnStart {
		t.Error("no turn_started event (backend did not start the turn)")
	}
	if gotError {
		// 本机 claude 网关/鉴权不可用时会走到这里——属于环境问题，明确标注而非误判为代码 bug。
		t.Fatalf("claude returned an error (likely claude gateway/auth, not bridge code): %s", errMsg)
	}
	if !gotDone {
		t.Fatal("no done event: turn did not complete (claude may be blocked on auth/gateway)")
	}
	reply := strings.ToLower(strings.TrimSpace(sb.String()))
	t.Logf("claude reply (turn=%s): %q", turnID, sb.String())
	if !strings.Contains(reply, "pong") {
		t.Errorf("expected reply to contain 'pong', got: %q", sb.String())
	}

	// ── 第二轮：resume 续会话，验证 sessionId 跨进程连续性 ───────────
	sid := th.SessionID()
	if sid == "" {
		t.Skip("no session_id captured from first turn (cannot test resume)")
	}
	rth, err := backend.ResumeThread(ctx, agent.ResumeThreadOptions{
		SessionID: sid,
		StartThreadOptions: agent.StartThreadOptions{
			Cwd: cwd, Model: model, Mode: agent.PermissionQA, Network: false,
		},
	})
	if err != nil {
		t.Fatalf("ResumeThread: %v", err)
	}
	defer rth.Close(ctx)

	run2 := rth.RunStreamed(ctx, agent.AgentInput{Text: "What single word did I just ask you to say?"}, nil)
	var sb2 strings.Builder
	ok2 := false
	for ev := range run2.Events {
		switch ev.Type {
		case agent.EvText, agent.EvTextDelta:
			sb2.WriteString(ev.Text)
			sb2.WriteString(ev.Delta)
		case agent.EvDone:
			ok2 = true
		case agent.EvError:
			t.Fatalf("resume turn error (likely claude gateway/auth): %s", ev.Message)
		}
	}
	if !ok2 {
		t.Fatal("resume turn did not complete")
	}
	t.Logf("claude resume reply: %q", sb2.String())
	if !strings.Contains(strings.ToLower(sb2.String()), "pong") {
		t.Errorf("resume did not recall the earlier turn (expected 'pong'): %q", sb2.String())
	}
}
