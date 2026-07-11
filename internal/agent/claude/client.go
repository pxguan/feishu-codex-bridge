package claude

// client.go —— 一个 `claude ... --output-format stream-json` 子进程。
//
// 与 codex 的 AppServerClient（持久 JSON-RPC 连接，一个进程 = 一个 thread）不同：
// `claude` CLI 在 `--print` 模式下是【一次性】的——一个进程 = 一个 turn（或一次 /compact、
// 或一次 goal 自主轮）。会话连续性靠 `--resume <sessionId>` 跨进程维持。
//
// 因此本客户端极简：拉起进程 → 逐行解析 stream-json 推入 messages chan → 进程退出关 chan。
// 调用方（ClaudeThread）负责把 messages 喂给 eventmap 并归一成 AgentEvent。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

// ClaudeCli 一个 claude 子进程。
type ClaudeCli struct {
	bin        string
	cwd        string
	env        map[string]string
	clientName string

	cmd   *exec.Cmd
	mu    sync.Mutex
	closed bool
	exited bool
	exitMu sync.Once

	messages chan json.RawMessage
	stdin   io.WriteCloser
	onExit   func()
}

// NewClaudeCli 构造（未连接）。
func NewClaudeCli(bin, cwd string, env map[string]string) *ClaudeCli {
	name := "feishu-codex-bridge"
	return &ClaudeCli{bin: bin, cwd: cwd, env: env, clientName: name}
}

// Pid 返回子进程 pid（未连接为 0）。
func (c *ClaudeCli) Pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Pid
	}
	return 0
}

// Exited 进程是否已退出。
func (c *ClaudeCli) Exited() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exited
}

// Messages 返回消息流 chan（进程退出时关闭）。
func (c *ClaudeCli) Messages() <-chan json.RawMessage { return c.messages }

// Start spawn + 开始读取。onExit 在进程退出后回调（关 chan 之后）。
func (c *ClaudeCli) Start(ctx context.Context, args []string, onExit func()) error {
	c.onExit = onExit
	c.messages = make(chan json.RawMessage, 256)
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.cwd
	cmd.Env = append(os.Environ(), "FEISHU_CODEX_BRIDGE=1")
	for k, v := range c.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// WorkBuddy 宿主给 shell 注入的 NODE_OPTIONS 里带 `--use-system-ca`，会让 claude
	// 子进程(Node)改用 macOS 系统 CA 库跟内网 GLM 网关(open.bigmodel.cn / 阿里云盾 WAF)
	// 做 TLS 握手并失败——表现为 `api_retry error:unknown, error_status:null` 死循环、
	// 整轮零回复(A/B 实测:带 --use-system-ca 必挂，去掉必通；curl 用系统 CA 反而正常，
	// 是 Node --use-system-ca 的链路处理问题)。这里剥掉它，让 Node 用内置 Mozilla CA。
	// 顺带强制 IPv4-first，规避 happy-eyeballs 下偶发 IPv6 连接问题。
	cmd.Env = sanitizeClaudeNodeOptions(cmd.Env)
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
		return fmt.Errorf("spawn claude: %w", err)
	}
	c.cmd = cmd
	c.stdin = stdin
	c.mu.Lock()
	c.closed = false
	c.exited = false
	c.mu.Unlock()
	core.Info(ctx, "agent", "spawn", fmt.Sprintf("claude pid=%d cwd=%s", cmd.Process.Pid, c.cwd))

	go c.readLoop(stdout)
	go func() {
		_ = cmd.Wait()
		c.handleExit()
	}()
	return nil
}

// WriteStdin 向 claude 子进程写入（仅 --input-format stream-json 模式需要）。
// 进程已退出时静默返回（best-effort）。
func (c *ClaudeCli) WriteStdin(p []byte) (int, error) {
	c.mu.Lock()
	w := c.stdin
	c.mu.Unlock()
	if w == nil {
		return 0, nil
	}
	return w.Write(p)
}

// CloseStdin 关闭 stdin（告知 claude 输入结束，等价于 EOF）。
func (c *ClaudeCli) CloseStdin() error {
	c.mu.Lock()
	w := c.stdin
	c.stdin = nil
	c.mu.Unlock()
	if w == nil {
		return nil
	}
	return w.Close()
}

// sanitizeClaudeNodeOptions 修正 claude 子进程的 NODE_OPTIONS：
//  1. 剥掉 `--use-system-ca`（会导致跟内网 GLM 网关 TLS 握手失败）；
//  2. 确保含 `--dns-result-order=ipv4first`（用户已显式指定 DNS 顺序时尊重不覆盖）。
//
// 保留其它已有选项（如 WorkBuddy 的 `--require=...shim.cjs`）。
func sanitizeClaudeNodeOptions(env []string) []string {
	idx, cur := -1, ""
	for i, kv := range env {
		if strings.HasPrefix(kv, "NODE_OPTIONS=") {
			idx = i
			cur = strings.TrimPrefix(kv, "NODE_OPTIONS=")
		}
	}

	fields := strings.Fields(cur)
	out := make([]string, 0, len(fields)+1)
	hasDNSOrder := false
	for _, f := range fields {
		if f == "--use-system-ca" {
			continue // 剔除
		}
		if strings.HasPrefix(f, "--dns-result-order") {
			hasDNSOrder = true
		}
		out = append(out, f)
	}
	if !hasDNSOrder {
		out = append(out, "--dns-result-order=ipv4first")
	}
	val := strings.Join(out, " ")

	if idx >= 0 {
		if val == "" {
			return append(env[:idx:idx], env[idx+1:]...) // 清空则移除该项
		}
		env[idx] = "NODE_OPTIONS=" + val
		return env
	}
	if val == "" {
		return env
	}
	return append(env, "NODE_OPTIONS="+val)
}

// Kill SIGTERM 整组 → grace → SIGKILL；幂等。
func (c *ClaudeCli) Kill(grace time.Duration) {
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	killTree(cmd.Process.Pid, grace)
}

func (c *ClaudeCli) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // claude 输出可能很大（长 diff/命令输出）
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		raw := append([]byte(nil), line...)
		// 非 JSON 行（如 --verbose 的告警）→ 丢弃，不进消息流。
		var probe json.RawMessage
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}
		select {
		case c.messages <- raw:
		default:
			// 消费者太慢：丢弃（一个 turn 的消息量有限）。
		}
	}
}

func (c *ClaudeCli) handleExit() {
	c.exitMu.Do(func() {
		c.mu.Lock()
		c.exited = true
		c.closed = true
		c.mu.Unlock()
		close(c.messages)
		if c.onExit != nil {
			c.onExit()
		}
	})
}

// stderrLogger 把 claude stderr 行 warn 到日志。
type stderrLogger struct{}

func (stderrLogger) Write(p []byte) (int, error) {
	line := truncStr(string(p), 300)
	core.Warn(context.Background(), "agent", "stderr", line)
	return len(p), nil
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
