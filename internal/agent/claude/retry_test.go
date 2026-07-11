package claude

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// TestMain：把重试退避调到极小，避免单测拖慢。具体重试次数/退避在各用例用 t.Setenv 覆盖。
// 真实验收（RUN_CLAUDE_ACCEPTANCE=1）也可自行设置 FCB_CLAUDE_* 覆盖。
func TestMain(m *testing.M) {
	os.Setenv("FCB_CLAUDE_MAX_RETRIES", "3")
	os.Setenv("FCB_CLAUDE_RETRY_BASE_DELAY", "20ms")
	os.Exit(m.Run())
}

// fakeFlakyClaude：前 failTimes 次直接以 api_retry 报错退出（模拟网关瞬断），之后吐完整成功流。
// 用工作目录下的 .attempt 计数器记录真实调用次数，跨重试持久。
func fakeFlakyClaude(t *testing.T, failTimes int) string {
	t.Helper()
	body := fmt.Sprintf(`#!/bin/sh
STATE="$PWD/.attempt"
n=$(cat "$STATE" 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > "$STATE"
if [ "$n" -le %d ]; then
  echo "api_retry error: unknown" >&2
  exit 1
fi
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-r","model":"claude-opus-4-8"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"recovered"}]}}
{"type":"result","subtype":"success","result":"recovered","usage":{"input_tokens":1,"output_tokens":1}}
EOF
`, failTimes)
	return writeFakeClaude(t, body)
}

// 网关瞬断后恢复：应在重试后成功，且只发「将重试」(WillRetry=true) 临时错误，无终态错误。
func TestRunStreamed_RetryRecovers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake claude is unix-only")
	}
	t.Setenv("FCB_CLAUDE_MAX_RETRIES", "3")
	t.Setenv("FCB_CLAUDE_RETRY_BASE_DELAY", "10ms")
	script := fakeFlakyClaude(t, 2) // 前 2 次失败，第 3 次成功
	th := NewClaudeThread(script, t.TempDir(), "", agent.EffortMedium, nil, "")
	run := th.RunStreamed(context.Background(), agent.AgentInput{Text: "hi"}, nil)

	var gotText string
	var sawDone, sawTerminalErr bool
	var retryNotices int
	for ev := range run.Events {
		switch ev.Type {
		case agent.EvText:
			gotText = ev.Text
		case agent.EvDone:
			sawDone = true
		case agent.EvError:
			if ev.WillRetry {
				retryNotices++
			} else {
				sawTerminalErr = true
			}
		}
	}
	if !sawDone {
		t.Fatal("expected done after retry recovers")
	}
	if sawTerminalErr {
		t.Fatal("should not emit terminal error when retry recovers")
	}
	if retryNotices == 0 {
		t.Fatal("expected at least one willRetry notice")
	}
	if gotText != "recovered" {
		t.Fatalf("text = %q, want %q", gotText, "recovered")
	}
}

// 网关持续故障：重试耗尽后应发一条终态错误(WillRetry=false)，不再重试。
func TestRunStreamed_RetryExhausted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake claude is unix-only")
	}
	t.Setenv("FCB_CLAUDE_MAX_RETRIES", "2")
	t.Setenv("FCB_CLAUDE_RETRY_BASE_DELAY", "10ms")
	script := fakeFlakyClaude(t, 99) // 永远失败
	th := NewClaudeThread(script, t.TempDir(), "", agent.EffortMedium, nil, "")
	run := th.RunStreamed(context.Background(), agent.AgentInput{Text: "hi"}, nil)

	var sawTerminalErr, sawDone bool
	var retryNotices int
	for ev := range run.Events {
		switch ev.Type {
		case agent.EvDone:
			sawDone = true
		case agent.EvError:
			if ev.WillRetry {
				retryNotices++
			} else {
				sawTerminalErr = true
			}
		}
	}
	if !sawTerminalErr {
		t.Fatal("expected terminal error after retries exhausted")
	}
	if sawDone {
		t.Fatal("should not emit done when all retries fail")
	}
	// maxRetries=2 → 第 1、2 次重试各一条 willRetry 通知，共 2 条。
	if retryNotices != 2 {
		t.Fatalf("retryNotices = %d, want 2", retryNotices)
	}
}
