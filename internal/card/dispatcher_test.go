package card

import (
	"context"
	"testing"
)

func eventWith(actionID string, opts ...func(*CardActionEvent)) *CardActionEvent {
	evt := &CardActionEvent{}
	if actionID != "" {
		evt.Action.Value = ActionValue{"a": actionID}
	}
	for _, o := range opts {
		o(evt)
	}
	return evt
}

func TestDispatcher_RoutesByActionID(t *testing.T) {
	d := NewCardDispatcher()
	called := ""
	d.On(RCStop, func(ctx CardActionContext) error {
		called = ctx.ActionID
		return nil
	})
	d.Handle(context.Background(), eventWith(RCStop))
	if called != RCStop {
		t.Fatalf("should route to handler: got %q", called)
	}
}

func TestDispatcher_LastRegistrationWins(t *testing.T) {
	d := NewCardDispatcher()
	first := false
	second := false
	d.On(RCStop, func(ctx CardActionContext) error { first = true; return nil })
	d.On(RCStop, func(ctx CardActionContext) error { second = true; return nil })
	d.Handle(context.Background(), eventWith(RCStop))
	if first {
		t.Fatal("first registration should be overridden")
	}
	if !second {
		t.Fatal("last registration should win")
	}
}

func TestDispatcher_UnkeyedNoCrash(t *testing.T) {
	d := NewCardDispatcher()
	d.On(RCStop, func(ctx CardActionContext) error {
		t.Fatal("unkeyed event should not call handler")
		return nil
	})
	// 无 value.a。
	d.Handle(context.Background(), eventWith(""))
}

func TestDispatcher_NoHandlerNoCrash(t *testing.T) {
	d := NewCardDispatcher()
	// 不注册 RCStop。
	d.Handle(context.Background(), eventWith(RCStop))
	// 不应 panic。
}

func TestDispatcher_OptionAndFormValue(t *testing.T) {
	d := NewCardDispatcher()
	var gotOption string
	var gotForm map[string]any
	d.On(MCModel, func(ctx CardActionContext) error {
		gotOption = ctx.Option
		gotForm = ctx.FormValue
		return nil
	})
	evt := eventWith(MCModel, func(e *CardActionEvent) {
		e.Action.Option = "gpt-5"
		e.Raw.Action.FormValue = map[string]any{"secs": []any{"stats"}}
	})
	d.Handle(context.Background(), evt)
	if gotOption != "gpt-5" {
		t.Fatalf("option wrong: %q", gotOption)
	}
	if gotForm["secs"] == nil {
		t.Fatal("formValue should pass through")
	}
}

func TestDispatcher_HandlerErrorLogged(t *testing.T) {
	// handler 抛错不应 panic（dispatcher 吞错 + log）。
	d := NewCardDispatcher()
	d.On(RCStop, func(ctx CardActionContext) error {
		return context.DeadlineExceeded
	})
	d.Handle(context.Background(), eventWith(RCStop))
}
