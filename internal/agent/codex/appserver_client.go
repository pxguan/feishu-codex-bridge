package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

// appserver_client.go —— 一个 `codex app-server --listen stdio://` 子进程，
// 走 JSON-RPC 2.0 over newline-delimited JSON（对齐 TS app-server-client）。
//
// 一个 client = 一个 thread/session（进程级崩溃隔离）。Request 按 id 匹配 response；
// 通知推 chan；进程退出 → 标记 exited + failAllPending + 关闭通知 chan。
//
// JsonRpcError 区分「应用层 RPC error（进程健康）」与「传输层失败/超时（进程坏了）」，
// client-pool 据此决定是否杀共享进程。

// JsonRpcError 应用层 JSON-RPC error（进程健康，只是这次 RPC 失败）。
type JsonRpcError struct{ Msg string }

func (e *JsonRpcError) Error() string { return e.Msg }

// AsJsonRpcError 判定是否 JsonRpcError。
func AsJsonRpcError(err error) bool {
	var j *JsonRpcError
	return errors.As(err, &j)
}

// AppServerClientOptions 构造参数。
type AppServerClientOptions struct {
	Bin        string
	Cwd        string
	Env        map[string]string
	ClientName string
}

// AppServerClient 一个 codex app-server 子进程客户端。
type AppServerClient struct {
	bin        string
	cwd        string
	env        map[string]string
	clientName string

	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu       sync.Mutex
	nextID   int
	pending  map[int]chan rpcResult
	notifyCh chan ServerNotification
	closed   bool
	exited   bool

	exitMu sync.Once
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

// NewAppServerClient 构造（未连接）。
func NewAppServerClient(opts AppServerClientOptions) *AppServerClient {
	name := opts.ClientName
	if name == "" {
		name = "feishu-codex-bridge"
	}
	return &AppServerClient{
		bin:        opts.Bin,
		cwd:        opts.Cwd,
		env:        opts.Env,
		clientName: name,
		pending:    map[int]chan rpcResult{},
		notifyCh:   make(chan ServerNotification, 1024),
	}
}

// Pid 返回子进程 pid（未连接为 0）。
func (c *AppServerClient) Pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Pid
	}
	return 0
}

// Exited 进程是否已退出（崩了/close）。调用方据此驱逐 thread，让 resume 兜底接管。
func (c *AppServerClient) Exited() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exited
}

// Connect spawn + initialize 握手。
func (c *AppServerClient) Connect(ctx context.Context) error {
	cmd := exec.Command(c.bin, "app-server", "--listen", "stdio://")
	cmd.Dir = c.cwd
	cmd.Env = append(os.Environ(), "FEISHU_CODEX_BRIDGE=1")
	for k, v := range c.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	setChildSysProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = stderrLogger{}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn codex app-server: %w", err)
	}
	c.cmd = cmd
	c.stdin = stdin
	core.Info(ctx, "agent", "spawn", fmt.Sprintf("codex app-server pid=%d cwd=%s", cmd.Process.Pid, c.cwd))

	go c.readLoop(stdout)
	go func() {
		_ = cmd.Wait()
		c.handleExit()
	}()

	// initialize 握手：experimentalApi 必须开（否则 goal RPC 被拒）。
	_, err = c.Request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": c.clientName, "version": "0.0.1"},
		"capabilities": map[string]any{"experimentalApi": true, "requestAttestation": false},
	})
	if err != nil {
		_ = c.Close(0)
		return err
	}
	c.Notify("initialized", nil)
	return nil
}

// Request 发 JSON-RPC 请求，等响应（受 ctx 超时控制）。
func (c *AppServerClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("app-server client closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	payload = append(payload, '\n')
	_, werr := c.stdin.Write(payload)
	c.mu.Unlock()
	if werr != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, werr
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Notify 发无 id 通知。
func (c *AppServerClient) Notify(method string, params any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	payload = append(payload, '\n')
	_, _ = c.stdin.Write(payload)
}

// Stream 返回通知 chan（进程退出时关闭）。
func (c *AppServerClient) Stream() <-chan ServerNotification {
	return c.notifyCh
}

// ClearNotifications 丢弃缓冲未消费的通知（预热池取用前清空 warmup 通知）。
func (c *AppServerClient) ClearNotifications() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		select {
		case <-c.notifyCh:
		default:
			return
		}
	}
}

// Close SIGTERM 整组 → grace → SIGKILL；幂等。
func (c *AppServerClient) Close(grace time.Duration) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	cmd := c.cmd
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	killTree(cmd.Process.Pid, grace)
	return nil
}

func (c *AppServerClient) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // codex 通知可能很大（diff/命令输出）
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		c.handleLine(line)
	}
}

func (c *AppServerClient) handleLine(line []byte) {
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		core.Warn(context.Background(), "agent", "nonjson", fmt.Sprintf("non-json line: %q", truncStr(string(line), 120)))
		return
	}
	idRaw, hasID := msg["id"]
	hasResult := msg["result"] != nil
	hasError := msg["error"] != nil
	_, hasMethod := msg["method"]

	// response（id + result/error，无 method）
	if hasID && (hasResult || hasError) && !hasMethod {
		var id float64
		_ = json.Unmarshal(idRaw, &id)
		iid := int(id)
		c.mu.Lock()
		ch, ok := c.pending[iid]
		if ok {
			delete(c.pending, iid)
		}
		c.mu.Unlock()
		if !ok {
			return
		}
		if hasError {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(msg["error"], &e)
			msg := e.Message
			if msg == "" {
				msg = "JSON-RPC error"
			}
			ch <- rpcResult{err: &JsonRpcError{Msg: msg}}
		} else {
			ch <- rpcResult{result: msg["result"]}
		}
		return
	}
	// server-initiated request（id + method）→ 回 method not found（approvalPolicy:never 不应有）
	if hasID && hasMethod {
		var id float64
		_ = json.Unmarshal(idRaw, &id)
		resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": int(id), "error": map[string]any{"code": -32601, "message": "not handled"}})
		resp = append(resp, '\n')
		c.mu.Lock()
		_, _ = c.stdin.Write(resp)
		c.mu.Unlock()
		return
	}
	// notification（method，无 id）
	if hasMethod {
		var n ServerNotification
		if err := json.Unmarshal(line, &n); err == nil {
			select {
			case c.notifyCh <- n:
			default:
				// 缓冲满：丢弃（消费者太慢；Phase 1 通知量不会到 1024）。
			}
		}
	}
}

func (c *AppServerClient) handleExit() {
	c.exitMu.Do(func() {
		c.mu.Lock()
		c.exited = true
		c.closed = true
		pending := c.pending
		c.pending = map[int]chan rpcResult{}
		c.mu.Unlock()
		for _, ch := range pending {
			ch <- rpcResult{err: errors.New("app-server exited")}
		}
		close(c.notifyCh)
	})
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// stderrLogger 把 codex stderr 行 warn 到日志。
type stderrLogger struct{}

func (stderrLogger) Write(p []byte) (int, error) {
	line := truncStr(string(p), 200)
	core.Warn(context.Background(), "agent", "stderr", line)
	return len(p), nil
}
