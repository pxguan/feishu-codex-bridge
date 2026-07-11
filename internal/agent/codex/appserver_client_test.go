package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// 用 sh 假 codex 脚本测 AppServerClient 的 JSON-RPC over NDJSON。

func writeFakeCodex(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-codex.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAppServerClient_ConnectAndStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	script := writeFakeCodex(t, `
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"capabilities":{}}}'
echo '{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"t1"}}}'
sleep 5
`)
	c := NewAppServerClient(AppServerClientOptions{Bin: script, Cwd: t.TempDir()})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close(time.Second)
	select {
	case n, ok := <-c.Stream():
		if !ok || n.Method != "thread/started" {
			t.Fatalf("stream got method=%q ok=%v", n.Method, ok)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestAppServerClient_ConnectJsonRpcError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	script := writeFakeCodex(t, `
read line
echo '{"jsonrpc":"2.0","id":1,"error":{"message":"init failed"}}'
sleep 1
`)
	c := NewAppServerClient(AppServerClientOptions{Bin: script, Cwd: t.TempDir()})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("connect should fail when initialize returns error")
	}
	if !AsJsonRpcError(err) {
		t.Fatalf("want JsonRpcError, got %T: %v", err, err)
	}
}

func TestAppServerClient_ExitedAndRequestFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	script := writeFakeCodex(t, `
read line
echo '{"jsonrpc":"2.0","id":1,"result":{}}'
exit 0
`)
	c := NewAppServerClient(AppServerClientOptions{Bin: script, Cwd: t.TempDir()})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close(time.Second)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !c.Exited() {
		time.Sleep(20 * time.Millisecond)
	}
	if !c.Exited() {
		t.Fatal("client should report exited after process exit")
	}
	// 进程死后，Request 必须立即失败（而非挂起）。
	_, err := c.Request(context.Background(), "thread/list", nil)
	if err == nil {
		t.Fatal("request after exit must fail")
	}
}

func TestAppServerClient_PendingRequestRejectedOnExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	// 假脚本：回 init 后，立即退出（不回后续请求）→ 在飞的 Request 被拒绝。
	script := writeFakeCodex(t, `
read line
echo '{"jsonrpc":"2.0","id":1,"result":{}}'
read line
exit 0
`)
	c := NewAppServerClient(AppServerClientOptions{Bin: script, Cwd: t.TempDir()})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close(time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// 第二个 Request（id=2）—— 脚本读到后 exit，不回响应 → handleExit failAllPending。
	_, err := c.Request(ctx, "thread/list", nil)
	if err == nil {
		t.Fatal("pending request should be rejected when process exits")
	}
}
