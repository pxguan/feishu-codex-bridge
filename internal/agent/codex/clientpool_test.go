package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// 假 codex 脚本工具：对每个带 id 的 request 回响应；可注入对特定 method 回 error。

// fakeRespondingCodex：对所有 request 回 {result:{}}。
func fakeRespondingCodex(t *testing.T) string {
	t.Helper()
	body := `while IFS= read -r line; do
  if echo "$line" | grep -q '"id":'; then
    id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
    echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"ok\":true}}"
  fi
done
`
	return writeFakeCodex(t, body)
}

// fakeErrorOnMethodCodex：对 methodA 回 error、其余回 result。
func fakeErrorOnMethodCodex(t *testing.T, errMethod string) string {
	t.Helper()
	body := `while IFS= read -r line; do
  if echo "$line" | grep -q '"id":'; then
    id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
    if echo "$line" | grep -q '"method":"` + errMethod + `"'; then
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"error\":{\"message\":\"boom\"}}"
    else
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"ok\":true}}"
    fi
  fi
done
`
	return writeFakeCodex(t, body)
}

// newTestPool 构造一个用假 codex 脚本作 bin 的 Pool（隔离默认池）。
func newTestPool(t *testing.T, script string) *Pool {
	t.Helper()
	// 把脚本路径写到一个稳定位置（binFingerprint 要 stat 它）。
	bin := filepath.Join(t.TempDir(), "fake-codex")
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, src, 0o755); err != nil {
		t.Fatal(err)
	}
	p := newPool()
	p.bin = func() string { return bin }
	return p
}

func TestPool_UtilityRequest_ReusesAcrossCalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	p := newTestPool(t, fakeRespondingCodex(t))
	defer p.Shutdown()
	ctx := context.Background()

	// 第一次调用：创建 utility client。
	r1, err := p.UtilityRequest(ctx, "thread/list", map[string]any{"cwd": "/tmp"}, 5*time.Second)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if string(r1) == "" {
		t.Fatal("empty result")
	}
	p.mu.Lock()
	first := p.util
	p.mu.Unlock()
	if first == nil {
		t.Fatal("utility client should be created")
	}
	pid1 := first.client.Pid()

	// 第二、三次调用：应复用同一进程。
	for _, m := range []string{"model/list", "account/read"} {
		if _, err := p.UtilityRequest(ctx, m, nil, 5*time.Second); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	p.mu.Lock()
	cur := p.util
	p.mu.Unlock()
	if cur == nil || cur.client.Pid() != pid1 {
		t.Fatalf("utility client should be reused (pid %d vs %d)", pid1, pidOrZero(cur))
	}
}

func TestPool_UtilityRequest_JsonRpcErrorKeepsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake codex is unix-only")
	}
	p := newTestPool(t, fakeErrorOnMethodCodex(t, "thread/read"))
	defer p.Shutdown()
	ctx := context.Background()

	// 第一次正常请求建立 utility client。
	if _, err := p.UtilityRequest(ctx, "thread/list", nil, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	pid1 := 0
	if p.util != nil {
		pid1 = p.util.client.Pid()
	}
	p.mu.Unlock()

	// thread/read 返回 JsonRpcError → 进程必须保留（不重建）。
	_, err := p.UtilityRequest(ctx, "thread/read", nil, 5*time.Second)
	if err == nil || !AsJsonRpcError(err) {
		t.Fatalf("want JsonRpcError, got %v", err)
	}
	p.mu.Lock()
	pid2 := 0
	if p.util != nil {
		pid2 = p.util.client.Pid()
	}
	p.mu.Unlock()
	if pid1 != pid2 || pid1 == 0 {
		t.Fatalf("JsonRpcError must NOT discard process: pid %d vs %d", pid1, pid2)
	}
}

func TestPool_UtilityRequest_NotFoundBin(t *testing.T) {
	p := newPool()
	p.bin = func() string { return "" }
	_, err := p.UtilityRequest(context.Background(), "thread/list", nil, time.Second)
	if err == nil {
		t.Fatal("missing codex bin should error")
	}
}

func TestPool_TakeWarmClient_EmptyByDefault(t *testing.T) {
	p := newPool()
	if c := p.TakeWarmClient("/any"); c != nil {
		t.Fatal("empty pool should return nil")
	}
}

func pidOrZero(e *utilEntry) int {
	if e == nil {
		return 0
	}
	return e.client.Pid()
}
