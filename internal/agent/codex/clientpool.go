package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// clientpool.go —— 常驻 codex 进程池（utility + 预热池容量 1，对齐 TS client-pool）。
//
//   - utility client：thread/list、thread/read、model/list、account/read 等元数据 RPC
//     共享一个懒创建、出错即重建的常驻进程（避免每次 spawn+initialize ~500ms）。
//   - 预热池容量 1：预先 spawn+initialize+ephemeral thread/start（触发 MCP ~1.6s 大头），
//     新会话直接取走（thread/start 从 ~2.1s 降到 ~64ms）；取走后异步补位。
//
// 关键不变量：JsonRpcError 不杀进程（进程健康）；超时/传输层失败才 discard+SIGKILL。

const (
	connectTimeout        = 15 * time.Second
	prewarmTimeout        = 60 * time.Second
	defaultUtilityTimeout = 30 * time.Second
)

// Pool 常驻进程池。
type Pool struct {
	bin        func() string // 默认 ResolveCodexBin(false)
	neutralCWD string

	mu           sync.Mutex
	util         *utilEntry    // utility client
	utilCreating chan struct{} // 单飞（非 nil=在途创建）
	warm         *warmEntry    // 预热槽
	warming      bool          // 预热单飞

	residents map[*AppServerClient]struct{} // exit 兜底 SIGKILL 集合
	resMu     sync.Mutex
}

type utilEntry struct {
	client *AppServerClient
	bin    string
}

type warmEntry struct {
	client *AppServerClient
	bin    string
	fp     string
}

// defaultPool 进程级默认池（生产用）。
var defaultPool = newPool()

func newPool() *Pool {
	return &Pool{
		bin:        func() string { return ResolveCodexBin(false) },
		neutralCWD: os.TempDir(),
		residents:  map[*AppServerClient]struct{}{},
	}
}

// UtilityRequest 在共享 utility client 上发 RPC（默认池）。
func UtilityRequest(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	return defaultPool.UtilityRequest(ctx, method, params, timeout)
}

// TakeWarmClient 取走预热进程（默认池）。
func TakeWarmClient(bin string) *AppServerClient { return defaultPool.TakeWarmClient(bin) }

// RefillWarmPool 异步补位预热池（默认池）。
func RefillWarmPool() { defaultPool.RefillWarmPool() }

// ShutdownResidentClients 关闭全部常驻进程（默认池）。
func ShutdownResidentClients() { defaultPool.Shutdown() }

// UtilityRequest 在共享 utility client 上发 RPC。
// 连接失败/进程死亡/超时 → 丢弃进程重建；JsonRpcError → 原样上抛、进程保留。
func (p *Pool) UtilityRequest(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = defaultUtilityTimeout
	}
	bin := p.bin()
	if bin == "" {
		return nil, errors.New("codex CLI not found (set CODEX_BIN or install @openai/codex)")
	}
	client, err := p.acquireUtility(ctx, bin)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := client.Request(rctx, method, params)
	if err != nil && !AsJsonRpcError(err) {
		p.discardUtility(client)
	}
	return res, err
}

// acquireUtility 单飞获取 utility client：在途创建者把后来者阻塞在 chan 上。
func (p *Pool) acquireUtility(ctx context.Context, bin string) (*AppServerClient, error) {
	p.mu.Lock()
	for p.utilCreating != nil {
		ch := p.utilCreating
		p.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		p.mu.Lock()
	}
	// 复用：进程活 + 同一 bin
	if p.util != nil && !p.util.client.Exited() && p.util.bin == bin {
		c := p.util.client
		p.mu.Unlock()
		return c, nil
	}
	// 死了 / bin 换了 → 丢弃旧
	if p.util != nil {
		old := p.util.client
		p.util = nil
		p.mu.Unlock()
		p.untrack(old)
		old.Close(0)
		p.mu.Lock()
	}
	// 单飞创建
	creating := make(chan struct{})
	p.utilCreating = creating
	p.mu.Unlock()

	client, err := p.connectResident(ctx, bin, "feishu-codex-bridge-utility")

	p.mu.Lock()
	p.utilCreating = nil
	close(creating)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	p.util = &utilEntry{client: client, bin: bin}
	p.mu.Unlock()
	go drainStream(client) // utility 不跑 thread，永远排空通知流防无界增长
	return client, nil
}

func (p *Pool) discardUtility(c *AppServerClient) {
	p.mu.Lock()
	if p.util != nil && p.util.client == c {
		p.util = nil
	}
	p.mu.Unlock()
	p.untrack(c)
	c.Close(0)
}

func (p *Pool) connectResident(ctx context.Context, bin, name string) (*AppServerClient, error) {
	client := NewAppServerClient(AppServerClientOptions{Bin: bin, Cwd: p.neutralCWD, ClientName: name})
	p.track(client)
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := client.Connect(cctx); err != nil {
		p.untrack(client)
		client.Close(0)
		return nil, err
	}
	return client, nil
}

// TakeWarmClient 取走预热进程（探活：进程活 + 同一 bin 指纹）。失活/错位返回 nil。
func (p *Pool) TakeWarmClient(bin string) *AppServerClient {
	p.mu.Lock()
	entry := p.warm
	p.warm = nil
	p.mu.Unlock()
	if entry == nil {
		return nil
	}
	p.untrack(entry.client)
	if entry.client.Exited() {
		return nil
	}
	if entry.bin != bin || entry.fp != binFingerprint(bin) {
		entry.client.Close(0)
		return nil
	}
	// 清空预热期缓冲的通知（MCP 进度、ephemeral thread/started）——绝不漏进真实会话。
	entry.client.ClearNotifications()
	return entry.client
}

// RefillWarmPool 异步补位（容量恒 1）。失败只记日志——预热是纯优化。
func (p *Pool) RefillWarmPool() {
	p.mu.Lock()
	if p.warm != nil || p.warming {
		p.mu.Unlock()
		return
	}
	p.warming = true
	p.mu.Unlock()

	go func() {
		defer func() {
			p.mu.Lock()
			p.warming = false
			p.mu.Unlock()
		}()
		bin := p.bin()
		if bin == "" {
			return
		}
		fp := binFingerprint(bin)
		client, err := p.connectResident(context.Background(), bin, "")
		if err != nil {
			return
		}
		pctx, cancel := context.WithTimeout(context.Background(), prewarmTimeout)
		defer cancel()
		_, err = client.Request(pctx, "thread/start", map[string]any{
			"cwd":            p.neutralCWD,
			"ephemeral":      true,
			"approvalPolicy": "never",
			"sandbox":        "read-only",
		})
		if err != nil {
			p.untrack(client)
			client.Close(0)
			return
		}
		p.mu.Lock()
		p.warm = &warmEntry{client: client, bin: bin, fp: fp}
		p.mu.Unlock()
	}()
}

// Shutdown 关闭全部常驻进程。
func (p *Pool) Shutdown() {
	p.mu.Lock()
	var targets []*AppServerClient
	if p.util != nil {
		targets = append(targets, p.util.client)
		p.util = nil
	}
	if p.warm != nil {
		targets = append(targets, p.warm.client)
		p.warm = nil
	}
	p.mu.Unlock()
	for _, c := range targets {
		p.untrack(c)
		c.Close(time.Second)
	}
}

// ── 辅助 ────────────────────────────────────────────────────────

// binFingerprint bin 文件指纹（mtime+size），取用时复验防 codex 原地升级错位。
func binFingerprint(bin string) string {
	fi, err := os.Stat(bin)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
}

func (p *Pool) track(c *AppServerClient) {
	p.resMu.Lock()
	p.residents[c] = struct{}{}
	p.resMu.Unlock()
}

func (p *Pool) untrack(c *AppServerClient) {
	p.resMu.Lock()
	delete(p.residents, c)
	p.resMu.Unlock()
}

func drainStream(c *AppServerClient) {
	for range c.Stream() {
	}
}
