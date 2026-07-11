package agent

import "testing"

func intPtrA(i int) *int { return &i }

func TestIsGoalTerminal(t *testing.T) {
	for _, s := range []string{"complete", "budgetLimited", "usageLimited", "blocked"} {
		if !IsGoalTerminal(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{"active", "paused", ""} {
		if IsGoalTerminal(s) {
			t.Errorf("%q should NOT be terminal", s)
		}
	}
}

func TestIsGoalSuccess(t *testing.T) {
	if !IsGoalSuccess("complete") {
		t.Fatal("complete is success")
	}
	if IsGoalSuccess("blocked") {
		t.Fatal("blocked is not success")
	}
}

func TestEventConstructors(t *testing.T) {
	if e := EvSys("t1"); e.Type != EvSystem || e.ThreadID != "t1" {
		t.Fatalf("EvSys wrong: %+v", e)
	}
	if e := EvTextD("i1", "hi"); e.Type != EvTextDelta || e.ItemID != "i1" || e.Delta != "hi" {
		t.Fatalf("EvTextD wrong: %+v", e)
	}
	if e := EvToolR("i1", "out", intPtrA(0)); e.Type != EvToolResult || e.ExitCode == nil || *e.ExitCode != 0 {
		t.Fatalf("EvToolR wrong: %+v", e)
	}
	if e := EvContext(100, nil); e.Type != EvContextUsage || e.UsedTokens != 100 || e.ContextWindow != nil {
		t.Fatalf("EvContext wrong: %+v", e)
	}
	w := 200000
	if e := EvContext(100, &w); e.ContextWindow == nil || *e.ContextWindow != 200000 {
		t.Fatalf("EvContext window wrong: %+v", e)
	}
	if e := EvErrorT("boom", true); e.Type != EvError || e.Message != "boom" || !e.WillRetry {
		t.Fatalf("EvErrorT wrong: %+v", e)
	}
	if e := EvDoneT("turn1"); e.Type != EvDone || e.TurnID != "turn1" {
		t.Fatalf("EvDone wrong: %+v", e)
	}
}

func TestUsageError(t *testing.T) {
	e := NewUsageError(UsageErrNoAuth, "no auth.json")
	if e.Kind != UsageErrNoAuth || e.Error() != "no auth.json" {
		t.Fatalf("UsageError wrong: %+v", e)
	}
}

func TestAllCapabilitiesDefaultsTrue(t *testing.T) {
	c := AllCapabilities()
	if !c.Goal || !c.Steer || !c.Compact || !c.Resume || !c.Approvals {
		t.Fatal("default capabilities should all be true")
	}
}
