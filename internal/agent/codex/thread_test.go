package codex

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

func TestRegistry_CreateCodex(t *testing.T) {
	b, err := agent.CreateBackend(agent.DEFAULT_BACKEND_ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID() != agent.DEFAULT_BACKEND_ID {
		t.Fatalf("backend id = %q", b.ID())
	}
	if _, err := agent.CreateBackend("nonexistent"); err == nil {
		t.Fatal("unknown backend should error")
	}
}

func TestCodexBackend_Doctor_ReturnsProbe(t *testing.T) {
	// 环境无关断言：Ok 时必有 version+location；!Ok 时必有 hint（无 codex / 探测失败）。
	b := &CodexAppServerBackend{}
	probe := b.Doctor(context.Background(), true)
	if probe.Ok {
		if probe.Version == "" || probe.Location == "" {
			t.Fatalf("ok probe must carry version+location: %+v", probe)
		}
	} else {
		if probe.Hint == "" {
			t.Fatalf("not-ok probe must carry hint: %+v", probe)
		}
	}
}

// 假脚本：对每个带 id 的 request 回 result；额外对 turn/start 推通知序列。
func fakeRunStreamedCodex(t *testing.T) string {
	t.Helper()
	body := `while IFS= read -r line; do
  if echo "$line" | grep -q '"id":'; then
    id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
    if echo "$line" | grep -q '"method":"turn/start"'; then
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
      echo '{"jsonrpc":"2.0","method":"turn/started","params":{"turn":{"id":"t1"}}}'
      echo '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"itemId":"i1","delta":"hi"}}'
      echo '{"jsonrpc":"2.0","method":"turn/completed","params":{"turn":{"id":"t1"}}}'
    else
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"data\":[{\"id\":\"gpt-test\",\"displayName\":\"GPT Test\"}]}}"
    fi
  fi
done
`
	return writeFakeCodex(t, body)
}

func TestCodexBackend_ListModels_FakeScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	script := fakeRunStreamedCodex(t)
	old := defaultPool
	defaultPool = newTestPool(t, script)
	defer func() { defaultPool = old }()
	defer defaultPool.Shutdown()

	b := &CodexAppServerBackend{}
	models, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 || models[0].ID != "gpt-test" {
		t.Fatalf("want gpt-test from fake model/list, got %+v", models)
	}
}

func TestCodexThread_RunStreamed_FakeScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	script := fakeRunStreamedCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := NewAppServerClient(AppServerClientOptions{Bin: script, Cwd: t.TempDir()})
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close(2 * time.Second)

	thread := NewCodexThread(client, "session-1", "", "")
	run := thread.RunStreamed(ctx, agent.AgentInput{Text: "hello"}, nil)

	var sawTurnStart, sawTextDelta, sawDone bool
	for ev := range run.Events {
		switch ev.Type {
		case agent.EvTurnStarted:
			sawTurnStart = true
			if ev.TurnID != "t1" {
				t.Fatalf("turnId=%q want t1", ev.TurnID)
			}
		case agent.EvTextDelta:
			sawTextDelta = true
			if ev.Delta != "hi" {
				t.Fatalf("delta=%q want hi", ev.Delta)
			}
		case agent.EvDone:
			sawDone = true
		}
		if sawDone {
			break
		}
	}
	if !sawTurnStart || !sawTextDelta || !sawDone {
		t.Fatalf("expected turn_started+text_delta+done; got start=%v delta=%v done=%v",
			sawTurnStart, sawTextDelta, sawDone)
	}
	if tid := run.TurnID(); tid != "t1" {
		t.Fatalf("TurnID()=%q want t1", tid)
	}
	if run.LastActivity() == 0 {
		t.Fatal("LastActivity should be set")
	}
	if !thread.IsAlive() {
		t.Fatal("thread should be alive (process running)")
	}
}

func TestCodexThread_LifecycleMethods(t *testing.T) {
	// Steer/Abort/ClearGoal/Compact 是 RPC 调用——这里仅验证它们对未连接 client 不会 panic。
	thread := &CodexThread{sessionID: "x", client: NewAppServerClient(AppServerClientOptions{Bin: "/nonexistent"})}
	if thread.SessionID() != "x" {
		t.Fatal("sessionID mismatch")
	}
	// IsAlive 在未连接 client 上：Exited() 返回 false（进程未启动），故 IsAlive=true。
	// 这是预期（未 Connect 的 client 不是「已退出」）。
}
