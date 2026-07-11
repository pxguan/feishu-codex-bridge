package card

// managed.go —— CardKit 实体托管（对齐 TS card/managed）。
// 按钮卡必须走 cardkit 实体（plain JSON 的 im.message.patch 对按钮静默失效）。
// sendManagedCard: create 实体 + 发引用消息 + stampRenderToken 反 12h cardActionId 去重。
// updateManagedCard: byMessageID→cardId 映射 + 单调 seq + 200810 点击窗口重试。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// ManagedRegistry CardKit 实体托管（进程内 messageId→cardId 映射）。
type ManagedRegistry struct {
	client      CardKitClient
	sleep       func(ms int64)
	mu          sync.Mutex
	byMessageID map[string]*managedEntry
	renderToken atomic.Int64
}

type managedEntry struct {
	cardID   string
	sequence int
}

// NewManagedRegistry 构造。
func NewManagedRegistry(client CardKitClient, sleep func(int64)) *ManagedRegistry {
	if sleep == nil {
		sleep = func(int64) {}
	}
	return &ManagedRegistry{client: client, sleep: sleep, byMessageID: map[string]*managedEntry{}}
}

// ManagedCardSendResult 发送结果。
type ManagedCardSendResult struct {
	MessageID string
	CardID    string
}

// SendManagedCard create 实体 + 发引用消息。230099 重试（最多 2 次）。
// replyTo 非空 → 回复（replyInThread 控话题内）；否则顶层发到 to（chat_id）。
func (r *ManagedRegistry) SendManagedCard(ctx context.Context, to string, card CardObject, replyTo string, replyInThread bool) (*ManagedCardSendResult, error) {
	r.stampRenderToken(card)
	data, _ := json.Marshal(card)
	for attempt := 0; ; attempt++ {
		cardID, err := r.client.CardCreate(string(data))
		if err != nil {
			if attempt >= 2 || !IsCardIdNotReady(err) {
				return nil, fmt.Errorf("managed.card.create: %w", err)
			}
			r.sleep(int64(400 * (attempt + 1)))
			continue
		}
		var msgID string
		if replyTo != "" {
			msgID, err = r.client.MessageReplyWithCard(replyTo, cardID, replyInThread)
		} else {
			msgID, err = r.client.MessageCreateWithCard(to, cardID)
		}
		if err != nil {
			if attempt >= 2 || !IsCardIdNotReady(err) {
				return nil, fmt.Errorf("managed.card.send: %w", err)
			}
			r.sleep(int64(400 * (attempt + 1)))
			continue
		}
		r.mu.Lock()
		r.byMessageID[msgID] = &managedEntry{cardID: cardID, sequence: 0}
		r.mu.Unlock()
		return &ManagedCardSendResult{MessageID: msgID, CardID: cardID}, nil
	}
}

// UpdateManagedCard 按 messageId 更新实体（单调 seq + 200810 重试）。无映射返回 false。
func (r *ManagedRegistry) UpdateManagedCard(ctx context.Context, messageID string, card CardObject) bool {
	r.mu.Lock()
	entry, ok := r.byMessageID[messageID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	r.stampRenderToken(card)
	data, _ := json.Marshal(card)

	push := func() error {
		entry.sequence++
		uuid := fmt.Sprintf("u_%s_%d", entry.cardID, entry.sequence)
		return r.client.CardUpdate(entry.cardID, string(data), entry.sequence, uuid)
	}
	if err := push(); err == nil {
		return true
	}
	// 200810 点击窗口：等 3.2s 重试一次（下一个 seq）。
	r.sleep(3200)
	if err := push(); err == nil {
		return true
	}
	return false
}

// IsManaged 是否持有该 messageId 的 cardId 映射。
func (r *ManagedRegistry) IsManaged(messageID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.byMessageID[messageID]
	return ok
}

// Forget 丢弃映射（卡片撤回/流程结束）。
func (r *ManagedRegistry) Forget(messageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byMessageID, messageID)
}

// stampRenderToken 递归给每个 callback behavior 的 value 盖自增 __r token。
// 反飞书 SDK 12h cardActionId 去重（同卡重渲后再次点击是新 key）。
func (r *ManagedRegistry) stampRenderToken(card CardObject) {
	token := r.renderToken.Add(1)
	tokenStr := encodeBase36(token)
	visitManaged(card, tokenStr)
}

func visitManaged(node any, token string) {
	switch v := node.(type) {
	case map[string]any:
		stampCallbackBehaviors(v, token)
		for _, child := range v {
			visitManaged(child, token)
		}
	case []any:
		for _, child := range v {
			visitManaged(child, token)
		}
	case []map[string]any: // []CardElement（代码构造的 behaviors/elements）
		for _, child := range v {
			visitManaged(child, token)
		}
	}
}

// stampCallbackBehaviors 给 map 里 callback behaviors 的 value 盖 __r。
// behaviors 可能是 []any（JSON 解码）或 []map[string]any（CardElement 构造）。
func stampCallbackBehaviors(m map[string]any, token string) {
	var behaviors []map[string]any
	switch bv := m["behaviors"].(type) {
	case []any:
		for _, b := range bv {
			if bm, ok := b.(map[string]any); ok {
				behaviors = append(behaviors, bm)
			}
		}
	case []map[string]any:
		behaviors = bv
	}
	for _, bm := range behaviors {
		if bm["type"] == "callback" {
			if val, ok := bm["value"].(map[string]any); ok {
				val["__r"] = token
			}
		}
	}
}

func encodeBase36(n int64) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var buf [16]byte
	p := len(buf)
	for n > 0 {
		p--
		buf[p] = digits[n%36]
		n /= 36
	}
	return string(buf[p:])
}
