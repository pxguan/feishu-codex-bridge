package card

import (
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func intP(i int) *int { return &i }

// ── reduce ──────────────────────────────────────────────────────

func TestReduce_TextDeltaAndText(t *testing.T) {
	s := InitialState()
	s = Reduce(s, agent.EvTextD("i1", "hel"))
	s = Reduce(s, agent.EvTextD("i1", "lo"))
	if len(s.Blocks) != 1 || s.Blocks[0].Content != "hello" || !s.Blocks[0].Streaming {
		t.Fatalf("text_delta accumulate/streaming wrong: %+v", s.Blocks)
	}
	if s.Footer != FooterStreaming {
		t.Fatalf("footer should be streaming: %v", s.Footer)
	}
	// item/completed → 全量替换 + 关闭 streaming。
	s = Reduce(s, agent.EvTextFull("i1", "hello world"))
	if s.Blocks[0].Content != "hello world" || s.Blocks[0].Streaming {
		t.Fatalf("text replace wrong: %+v", s.Blocks[0])
	}
}

func TestReduce_ThinkingAndReasoningContent(t *testing.T) {
	s := InitialState()
	s = Reduce(s, agent.EvThinkingD("r1", "th"))
	s = Reduce(s, agent.EvThinkingFull("r1", "full thought"))
	if len(s.Reasoning) != 1 || s.Reasoning[0].Text != "full thought" {
		t.Fatalf("reasoning wrong: %+v", s.Reasoning)
	}
	if !s.ReasoningActive {
		t.Fatal("reasoning should be active")
	}
	if ReasoningContent(s) != "full thought" {
		t.Fatalf("reasoning content wrong: %q", ReasoningContent(s))
	}
}

func TestReduce_ToolUseAndResult(t *testing.T) {
	s := InitialState()
	s = Reduce(s, agent.EvToolU("t1", "ls -la", "/tmp"))
	if len(s.Blocks) != 1 || s.Blocks[0].Kind != "tool" || s.Blocks[0].Tool.Status != ToolRunning {
		t.Fatalf("tool_use wrong: %+v", s.Blocks)
	}
	if s.Footer != FooterToolRunning {
		t.Fatal("footer should be tool_running")
	}
	// exit 0 → done。
	s = Reduce(s, agent.EvToolR("t1", "output", intP(0)))
	if s.Blocks[0].Tool.Status != ToolDone || s.Blocks[0].Tool.Output != "output" {
		t.Fatalf("tool_result done wrong: %+v", s.Blocks[0].Tool)
	}
	// exit !=0 → error。
	s2 := Reduce(InitialState(), agent.EvToolU("t1", "cmd", ""))
	s2 = Reduce(s2, agent.EvToolR("t1", "err", intP(2)))
	if s2.Blocks[0].Tool.Status != ToolError {
		t.Fatalf("non-zero exit should be error: %+v", s2.Blocks[0].Tool)
	}
}

func TestReduce_ContextUsage(t *testing.T) {
	s := InitialState()
	w := 200000
	s = Reduce(s, agent.EvContext(5000, &w))
	if s.Usage == nil || s.Usage.Used != 5000 || s.Usage.Window == nil || *s.Usage.Window != 200000 {
		t.Fatalf("usage wrong: %+v", s.Usage)
	}
}

func TestReduce_ErrorWillRetryNotTerminal(t *testing.T) {
	s := InitialState()
	s = Reduce(s, agent.EvErrorT("transient", true))
	if s.Terminal != TermRunning {
		t.Fatal("willRetry error must NOT be terminal")
	}
	if s.Footer != FooterRetrying {
		t.Fatalf("footer should be retrying: %v", s.Footer)
	}
}

func TestReduce_ErrorFatalTerminal(t *testing.T) {
	s := InitialState()
	s = Reduce(s, agent.EvErrorT("boom", false))
	if s.Terminal != TermError || s.ErrorMsg != "boom" {
		t.Fatalf("fatal error should be terminal: %+v", s)
	}
}

func TestReduce_Done(t *testing.T) {
	s := InitialState()
	s = Reduce(s, agent.EvTextD("i1", "x"))
	s = Reduce(s, agent.EvDoneT("t1"))
	if s.Terminal != TermDone {
		t.Fatal("done should be terminal done")
	}
	if s.Blocks[0].Streaming {
		t.Fatal("done should close streaming text")
	}
}

func TestFinalMessageText_LastNonEmpty(t *testing.T) {
	s := InitialState()
	s = Reduce(s, agent.EvTextFull("i1", "preamble"))
	s = Reduce(s, agent.EvTextFull("i2", "final answer"))
	if got := FinalMessageText(s); got != "final answer" {
		t.Fatalf("finalMessageText=%q want final answer", got)
	}
}

func TestMarkInterruptedAndIdleTimeout(t *testing.T) {
	s := Reduce(InitialState(), agent.EvTextD("i1", "x"))
	s = MarkInterrupted(s)
	if s.Terminal != TermInterrupted || s.Blocks[0].Streaming {
		t.Fatalf("interrupted wrong: %+v", s)
	}
	s2 := MarkIdleTimeout(Reduce(InitialState(), agent.EvTextD("i1", "x")), 300)
	if s2.Terminal != TermIdleTimeout || s2.IdleTimeoutSeconds != 300 {
		t.Fatalf("idle timeout wrong: %+v", s2)
	}
}

func TestFinalizeIfRunning(t *testing.T) {
	s := FinalizeIfRunning(InitialState())
	if s.Terminal != TermDone {
		t.Fatal("finalize running → done")
	}
	// 已终态不重复改。
	done := RunState{Terminal: TermError}
	if got := FinalizeIfRunning(done); got.Terminal != TermError {
		t.Fatal("finalize should not override non-running terminal")
	}
}

// ── context-gauge ───────────────────────────────────────────────

func TestCtxTierFor(t *testing.T) {
	if CtxTierFor(0.5).Level != 0 {
		t.Fatal("0.5 → level 0")
	}
	if CtxTierFor(0.7).Level != 1 {
		t.Fatal("0.7 → level 1 (yellow)")
	}
	if CtxTierFor(0.85).Level != 2 {
		t.Fatal("0.85 → level 2 (orange)")
	}
	if CtxTierFor(0.95).Level != 3 {
		t.Fatal("0.95 → level 3 (red)")
	}
}

func TestCtxPercent(t *testing.T) {
	w := 200000
	pct, ok := CtxPercent(50000, &w)
	if !ok || pct != 25 {
		t.Fatalf("CtxPercent = %d ok=%v, want 25 true", pct, ok)
	}
	if _, ok := CtxPercent(100, nil); ok {
		t.Fatal("nil window → ok=false")
	}
	pct, _ = CtxPercent(999999, &w)
	if pct != 100 {
		t.Fatalf("over 100 should clamp: %d", pct)
	}
}

func TestRunCardGaugeVisible(t *testing.T) {
	w := 100000
	if RunCardGaugeVisible(50000, &w) {
		t.Fatal("50% < 70% warn → not visible")
	}
	if !RunCardGaugeVisible(75000, &w) {
		t.Fatal("75% ≥ 70% → visible")
	}
	if RunCardGaugeVisible(75000, nil) {
		t.Fatal("nil window → not visible")
	}
}

func TestK(t *testing.T) {
	if K(999) != "999" {
		t.Fatal("K(999)")
	}
	if K(5000) != "5k" {
		t.Fatalf("K(5000)=%q want 5k", K(5000))
	}
}
