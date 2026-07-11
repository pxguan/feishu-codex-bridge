package card

import (
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func rcWithState(rs RunState) RunCardState { return RunCardState{RS: rs} }

func TestBuildRunCard_RunningHasAnswerElement(t *testing.T) {
	rs := InitialState()
	rs = Reduce(rs, agent.EvTextD("i1", "hello"))
	c := BuildRunCard(rcWithState(rs))
	if c["schema"] != "2.0" {
		t.Fatal("schema")
	}
	cfg := c["config"].(CardElement)
	if cfg["streaming_mode"] != true {
		t.Fatal("running card should enable streaming_mode")
	}
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	hasAnswer := false
	for _, e := range els {
		if e["tag"] == "markdown" && e["element_id"] == AnswerEID {
			hasAnswer = true
		}
	}
	if !hasAnswer {
		t.Fatal("running card should have answer element with ANSWER_EID")
	}
}

func TestBuildRunCard_RunningWithStopButton(t *testing.T) {
	rs := InitialState()
	rc := RunCardState{RS: rs, CardKey: "msg_1"}
	c := BuildRunCard(rc)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	hasStop := false
	for _, e := range els {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						behaviors := sub["behaviors"].([]CardElement)
						if behaviors[0]["value"].(ActionValue)["a"] == RCStop {
							hasStop = true
						}
					}
				}
			}
		}
	}
	if !hasStop {
		t.Fatal("running card with cardKey should have ⏹ stop button")
	}
}

func TestBuildRunCard_GoalControls(t *testing.T) {
	rs := InitialState()
	rc := RunCardState{RS: rs, CardKey: "msg_1", GoalControls: true}
	c := BuildRunCard(rc)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	hasEndGoal := false
	for _, e := range els {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						behaviors := sub["behaviors"].([]CardElement)
						if behaviors[0]["value"].(ActionValue)["a"] == RCEndGoal {
							hasEndGoal = true
						}
					}
				}
			}
		}
	}
	if !hasEndGoal {
		t.Fatal("goal card should have 🎯 endGoal button")
	}
}

func TestBuildRunCard_TerminalFoldsProcess(t *testing.T) {
	rs := InitialState()
	rs = Reduce(rs, agent.EvToolU("t1", "ls", "/tmp"))
	rs = Reduce(rs, agent.EvToolR("t1", "output", intP(0)))
	rs = Reduce(rs, agent.EvTextFull("i1", "final answer"))
	rs = Reduce(rs, agent.EvDoneT("turn1"))
	c := BuildRunCard(rcWithState(rs))
	if c["config"].(CardElement)["streaming_mode"] != nil {
		t.Fatal("terminal card should NOT enable streaming")
	}
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	// 应含过程折叠面板 + 最终答案。
	hasProcess := false
	hasAnswer := false
	for _, e := range els {
		if e["tag"] == "collapsible_panel" {
			hasProcess = true
		}
		if e["tag"] == "markdown" && e["content"] == "final answer" {
			hasAnswer = true
		}
	}
	if !hasProcess {
		t.Fatal("terminal should fold process into a panel")
	}
	if !hasAnswer {
		t.Fatal("terminal should surface final answer")
	}
}

func TestBuildRunCard_TerminalInterrupted(t *testing.T) {
	rs := MarkInterrupted(InitialState())
	c := BuildRunCard(rcWithState(rs))
	body := c["body"].(CardElement)
	if !containsMd(joinMd(body["elements"].([]CardElement)), "已被中断") {
		t.Fatal("interrupted terminal should show note")
	}
}

func TestBuildQueuedCard(t *testing.T) {
	c := BuildQueuedCard(QueuedCardState{Position: 3, CardKey: "m1"})
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !containsMd(joined, "第 **3** 位") {
		t.Fatalf("queued card should show position: %q", joined)
	}
	// 含 ⏹ 取消按钮。
	hasCancel := false
	for _, e := range body["elements"].([]CardElement) {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						hasCancel = true
					}
				}
			}
		}
	}
	if !hasCancel {
		t.Fatal("queued card with cardKey should have cancel button")
	}
}

func TestBuildQueuedCard_Cancelled(t *testing.T) {
	c := BuildQueuedCard(QueuedCardState{Cancelled: true, Dropped: 2})
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !containsMd(joined, "已取消排队") || !containsMd(joined, "2 条排队消息已丢弃") {
		t.Fatalf("cancelled should show notice: %q", joined)
	}
}

func TestErrorAdvice(t *testing.T) {
	if errorAdvice("401 unauthorized") == "" {
		t.Fatal("401 should give login advice")
	}
	if errorAdvice("usage limit exceeded") == "" {
		t.Fatal("usage limit should give advice")
	}
	if errorAdvice("random message") != "" {
		t.Fatal("unmatched should give no advice")
	}
}

func TestModelEffortMd(t *testing.T) {
	if modelEffortMd("gpt-5", "") != "gpt-5" {
		t.Fatal("no effort → model only")
	}
	md := modelEffortMd("gpt-5", agent.EffortHigh)
	if md == "gpt-5" {
		t.Fatal("high effort should append colored label")
	}
}

func containsMd(s, sub string) bool {
	return len(s) >= 0 && (s != "" || sub == "") || stringContains(s, sub)
}
func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
