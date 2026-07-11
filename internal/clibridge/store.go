package clibridge

// store.go —— 进程内 pending interaction 仓库（对齐 TS cli-bridge/store.ts）。
// 一个 hook 卡 = 一条 pending；飞书点击 / 本机回归 / 超时都会 resolve 它，
// 唤醒阻塞在对应 waitFor 上的 IPC 连接（即等审批的 agent hook）。
//
// 关键不变量：
//   - settled 缓存「先到的决策」：飞书点击快过 waitFor 注册时，决策先入 settled，
//     waitFor 起来直接取走，不丢点击。
//   - STALE_PENDING_MS 兜底清扫，map（及 findByReply 的 O(n) 扫描）不会无限增长。

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// PendingCliKind pending 类型。
type PendingCliKind string

const (
	PendingPermission      PendingCliKind = "permission"
	PendingQuestion        PendingCliKind = "question"
	PendingTaskCompletion  PendingCliKind = "task_completion"
)

// PendingCliInteraction 一条 pending（审批 / 问答 / 完成）。
type PendingCliInteraction struct {
	ID           string            `json:"id"`
	Kind         PendingCliKind    `json:"kind"`
	Source       CliBridgeAgent    `json:"source"`
	SessionID    string            `json:"sessionId"`
	Cwd          string            `json:"cwd"`
	ToolName     string            `json:"toolName,omitempty"`
	Question     string            `json:"question,omitempty"`
	Command      string            `json:"command,omitempty"`
	HookEventName string           `json:"hookEventName,omitempty"`
	Options      []CliQuestionOption `json:"options,omitempty"`
	Header       string            `json:"header,omitempty"`
	// AskUserQuestion：整组 1-4 问题（一张多问题表单）。
	Questions    []CliQuestionItem `json:"questions,omitempty"`
	TaskStatus   string            `json:"taskStatus,omitempty"` // completed|failed
	Summary      string            `json:"summary,omitempty"`
	ReplyExpiresAt int64           `json:"replyExpiresAt,omitempty"`
	ToolInput    map[string]any    `json:"toolInput,omitempty"`
	MessageID    string            `json:"messageId,omitempty"`
	CreatedAt    int64             `json:"createdAt"`
}

// 超过 24h 上界（IPC/审批等待上限）仍滞留的 pending 即泄漏（send 抛错未建、或
// 完成卡 reply 关掉），此处统一清扫。懒清除，无定时生命周期。
const stalePendingMs = 25 * 60 * 60 * 1000

type pendingStore struct {
	mu       sync.Mutex
	pending  map[string]*PendingCliInteraction
	waiters  map[string]chan CliHookResponse
	settled  map[string]CliHookResponse
}

var store = &pendingStore{
	pending: map[string]*PendingCliInteraction{},
	waiters: map[string]chan CliHookResponse{},
	settled: map[string]CliHookResponse{},
}

func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极不可能；退化为时间+随机。
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}

// prunePending 清除过期 pending（含其 waiter/settled）。
func (s *pendingStore) prunePending(now int64) {
	for id, item := range s.pending {
		if now-item.CreatedAt <= stalePendingMs {
			continue
		}
		delete(s.pending, id)
		delete(s.waiters, id)
		delete(s.settled, id)
	}
}

// CreatePendingCliInteraction 新建一条 pending 并写入仓库。
func CreatePendingCliInteraction(input PendingCliInteraction) *PendingCliInteraction {
	now := time.Now().UnixMilli()
	store.mu.Lock()
	store.prunePending(now)
	item := input
	item.ID = uuid()
	item.CreatedAt = now
	cp := item
	store.pending[item.ID] = &cp
	store.mu.Unlock()
	return &cp
}

// SetPendingCliMessageId 回填飞书卡片 message_id（resolve 时按回复匹配用）。
func SetPendingCliMessageId(id, messageID string) {
	store.mu.Lock()
	if item, ok := store.pending[id]; ok {
		cp := *item
		cp.MessageID = messageID
		store.pending[id] = &cp
	}
	store.mu.Unlock()
}

// GetPendingCliInteraction 取一条 pending（只读）。
func GetPendingCliInteraction(id string) *PendingCliInteraction {
	store.mu.Lock()
	defer store.mu.Unlock()
	if item, ok := store.pending[id]; ok {
		cp := *item
		return &cp
	}
	return nil
}

// FindPendingCliInteractionByMessageReply 按回复的 parent/root 匹配 pending。
func FindPendingCliInteractionByMessageReply(input struct {
	ParentID string
	RootID   string
}) *PendingCliInteraction {
	targets := make([]string, 0, 2)
	if input.ParentID != "" {
		targets = append(targets, input.ParentID)
	}
	if input.RootID != "" {
		targets = append(targets, input.RootID)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, item := range store.pending {
		if item.MessageID != "" {
			for _, t := range targets {
				if item.MessageID == t {
					cp := *item
					return &cp
				}
			}
		}
	}
	return nil
}

// ResolvePendingCliInteraction 决策到达：唤醒 waiter 或缓冲到 settled。
func ResolvePendingCliInteraction(id string, response CliHookResponse) bool {
	store.mu.Lock()
	if _, ok := store.pending[id]; !ok {
		store.mu.Unlock()
		return false
	}
	delete(store.pending, id)
	waiter, hasWaiter := store.waiters[id]
	if hasWaiter {
		delete(store.waiters, id)
		store.mu.Unlock()
		waiter <- response
		return true
	}
	store.settled[id] = response
	store.mu.Unlock()
	return true
}

// WaitForPendingCliInteraction 阻塞等决策（≤ timeoutMs）。先查 settled 缓冲。
// 出错路径：settled 有值直接取；pending 已无则 missing_pending；超时 timeout。
func WaitForPendingCliInteraction(id string, timeoutMs int) CliHookResponse {
	store.mu.Lock()
	if buffered, ok := store.settled[id]; ok {
		delete(store.settled, id)
		store.mu.Unlock()
		return buffered
	}
	if _, ok := store.pending[id]; !ok {
		store.mu.Unlock()
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "missing_pending"}
	}
	ch := make(chan CliHookResponse, 1)
	store.waiters[id] = ch
	store.mu.Unlock()

	timer := time.NewTimer(time.Duration(max(timeoutMs, 0)) * time.Millisecond)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return resp
	case <-timer.C:
		store.mu.Lock()
		delete(store.pending, id)
		delete(store.waiters, id)
		delete(store.settled, id)
		store.mu.Unlock()
		return CliHookResponse{Decision: DecisionFallbackLocal, Reason: "timeout"}
	}
}
