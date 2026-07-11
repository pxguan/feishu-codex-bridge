package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// 用 sh 假 claude 脚本测 ClaudeCli 的 stream-json 读取 + thread 的事件归一流。

func writeFakeClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// 假 claude：吐一条完整 stream-json（system/init → 文本增量 → 工具 → 结果）。
const fakeStreamScript = `#!/bin/sh
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-123","model":"claude-opus-4-8"}
{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}}
{"type":"stream_event","event":{"type":"content_block_stop","index":0}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":[{"type":"text","text":"file1\nfile2"}]}]}}
{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}
EOF
`

func TestRunStreamed_FakeClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake claude is unix-only")
	}
	script := writeFakeClaude(t, fakeStreamScript)
	th := NewClaudeThread(script, t.TempDir(), "", agent.EffortMedium,
		[]string{"--permission-mode", "bypassPermissions"}, "")
	run := th.RunStreamed(context.Background(), agent.AgentInput{Text: "hi"}, nil)

	var sysThreadID string
	var sawTurnStart, sawToolUse, sawToolResult, sawUsage, sawDone bool
	for ev := range run.Events {
		switch ev.Type {
		case agent.EvTurnStarted:
			sawTurnStart = true
		case agent.EvSystem:
			sysThreadID = ev.ThreadID
		case agent.EvToolUse:
			sawToolUse = true
		case agent.EvToolResult:
			sawToolResult = true
		case agent.EvUsage:
			sawUsage = true
		case agent.EvDone:
			sawDone = true
		}
	}
	if !sawTurnStart {
		t.Fatal("missing turn_started")
	}
	if sysThreadID != "sess-123" {
		t.Fatalf("session id = %q", sysThreadID)
	}
	if !sawToolUse {
		t.Fatal("missing tool_use")
	}
	if !sawToolResult {
		t.Fatal("missing tool_result")
	}
	if !sawUsage {
		t.Fatal("missing usage")
	}
	if !sawDone {
		t.Fatal("missing done")
	}
	if th.SessionID() != "sess-123" {
		t.Fatalf("thread session id = %q", th.SessionID())
	}
}

// 假 claude（无 stream_event 增量，仅完整 assistant 消息）：验证 sawPartial 兜底补发文本。
const fakeNoPartialScript = `#!/bin/sh
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"s2","model":"claude-opus-4-8"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"plain answer"}]}}
{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":10,"output_tokens":5}}
EOF
`

func TestRunStreamed_NoPartialFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake claude is unix-only")
	}
	script := writeFakeClaude(t, fakeNoPartialScript)
	th := NewClaudeThread(script, t.TempDir(), "", agent.EffortMedium, nil, "")
	run := th.RunStreamed(context.Background(), agent.AgentInput{Text: "hi"}, nil)
	var gotText string
	var sawDone bool
	for ev := range run.Events {
		if ev.Type == agent.EvText {
			gotText = ev.Text
		}
		if ev.Type == agent.EvDone {
			sawDone = true
		}
	}
	if gotText != "plain answer" {
		t.Fatalf("no-partial fallback text = %q", gotText)
	}
	if !sawDone {
		t.Fatal("missing done")
	}
}

// 假 claude：init 后 sleep，测试 Abort 杀进程并以干净 done 收尾。
const fakeSleepScript = `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"s3","model":"claude-opus-4-8"}'
sleep 5
`

func TestRunStreamed_Abort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake claude is unix-only")
	}
	script := writeFakeClaude(t, fakeSleepScript)
	th := NewClaudeThread(script, t.TempDir(), "", agent.EffortMedium, nil, "")
	run := th.RunStreamed(context.Background(), agent.AgentInput{Text: "hi"}, nil)
	time.Sleep(300 * time.Millisecond) // 等 init 处理
	th.Abort(context.Background(), run.TurnID())
	var sawDone bool
	for ev := range run.Events {
		if ev.Type == agent.EvDone {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("abort should end with clean done")
	}
}

// 假 claude：init 报错（claude 找不到/崩）→ Start 失败，run 收 error 事件。
const fakeErrorScript = `#!/bin/sh
echo "boom" >&2
exit 3
`

func TestRunStreamed_StartError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake claude is unix-only")
	}
	script := writeFakeClaude(t, fakeErrorScript)
	th := NewClaudeThread(script, t.TempDir(), "", agent.EffortMedium, nil, "")
	run := th.RunStreamed(context.Background(), agent.AgentInput{Text: "hi"}, nil)
	var sawError bool
	for ev := range run.Events {
		if ev.Type == agent.EvError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("start failure should surface as error event")
	}
}

func TestSanitizeClaudeNodeOptions(t *testing.T) {
	find := func(env []string) string {
		for _, kv := range env {
			if len(kv) >= 13 && kv[:13] == "NODE_OPTIONS=" {
				return kv[13:]
			}
		}
		return "<none>"
	}
	// 1) 无 NODE_OPTIONS → 注入 ipv4first
	if got := find(sanitizeClaudeNodeOptions([]string{"A=1"})); got != "--dns-result-order=ipv4first" {
		t.Fatalf("empty case: got %q", got)
	}
	// 2) 含 --use-system-ca + shim → 剥掉 use-system-ca、保留 shim、补 ipv4first
	if got := find(sanitizeClaudeNodeOptions([]string{`NODE_OPTIONS=--require="/x/shim.cjs" --use-system-ca`})); got != `--require="/x/shim.cjs" --dns-result-order=ipv4first` {
		t.Fatalf("strip case: got %q", got)
	}
	// 3) 只有 --use-system-ca → 剥掉后只剩 ipv4first
	if got := find(sanitizeClaudeNodeOptions([]string{"NODE_OPTIONS=--use-system-ca"})); got != "--dns-result-order=ipv4first" {
		t.Fatalf("only-systemca case: got %q", got)
	}
	// 4) 用户已指定 dns 顺序 → 不覆盖，但仍剥 use-system-ca
	if got := find(sanitizeClaudeNodeOptions([]string{"NODE_OPTIONS=--use-system-ca --dns-result-order=verbatim"})); got != "--dns-result-order=verbatim" {
		t.Fatalf("respect-dns case: got %q", got)
	}
}
