package card

// run_card_stream.go —— 运行卡流式 patch 的纯函数 + ChatPacer（对齐 TS card/run-card-stream）。
// RunCardStream 主体（create/streamCard/streamElement/updateCard，依赖 CardKitClient interface）
// 是下一步（飞书 SDK cardkit/im 调用通过 interface 注入）。

import (
	"encoding/json"
	"sync"
)

// 限流常量。
const (
	StreamThrottleMS    = 150
	ChatMinGapMS        = 250
	RateLimitPenaltyMS  = 1000
	TerminalRLRetries   = 3
	RLBackoffBaseMS     = 1000
	ErrStreamingOff     = 300309
	ErrSeqOutOfOrder    = 300317
	ChatPacerMaxEntries = 512
)

// ChatPacer per-chat 限流（所有 RunCardStream 共享一个 chat 的 pacer）。
type ChatPacer struct {
	mu     sync.Mutex
	nextAt int64 // ms
}

// Wait 预订下一槽并等（now/sleep 注入便于测试）。
func (p *ChatPacer) Wait(nowMs int64, sleep func(ms int64)) {
	p.mu.Lock()
	at := p.nextAt
	if nowMs > at {
		at = nowMs
	}
	p.nextAt = at + ChatMinGapMS
	p.mu.Unlock()
	if at > nowMs {
		sleep(at - nowMs)
	}
}

// Penalize 429 → 整个 chat 下一槽后推。
func (p *ChatPacer) Penalize(nowMs int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	penalty := nowMs + RateLimitPenaltyMS
	if penalty > p.nextAt {
		p.nextAt = penalty
	}
}

// Idle 是否空闲（>60s 无活动），供 pacer map 清理。
func (p *ChatPacer) Idle(nowMs int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextAt < nowMs-60000
}

// pacerMap per-chat ChatPacer 池（上限 512，溢出清空闲）。
type pacerMap struct {
	mu     sync.Mutex
	pacers map[string]*ChatPacer
	nowMs  func() int64
}

func newPacerMap(nowMs func() int64) *pacerMap {
	return &pacerMap{pacers: map[string]*ChatPacer{}, nowMs: nowMs}
}

func (m *pacerMap) pacerFor(chatID string) *ChatPacer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pacers[chatID]; ok {
		return p
	}
	if len(m.pacers) >= ChatPacerMaxEntries {
		now := m.nowMs()
		for k, v := range m.pacers {
			if v.Idle(now) {
				delete(m.pacers, k)
			}
		}
	}
	p := &ChatPacer{}
	m.pacers[chatID] = p
	return p
}

// IsRateLimited 飞书限流：HTTP 429 或业务码 99991400。
// CardkitError 是飞书 SDK 错误的抽象（Code 在 response.data.code 或顶层）。
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	if ce, ok := err.(*CardkitError); ok {
		return ce.Code == 99991400
	}
	return false
}

// CardkitError 飞书 cardkit 业务错误（含 code）。
type CardkitError struct {
	Code    int
	Message string
}

func (e *CardkitError) Error() string { return e.Message }

// CardkitErrCode 从错误提取业务码（非 CardkitError 返回 0）。
func CardkitErrCode(err error) int {
	if ce, ok := err.(*CardkitError); ok {
		return ce.Code
	}
	return 0
}

// AnswerContent 卡片中指定 element_id 的内容（answer 元素）；不存在返回 nil。
func AnswerContent(card CardObject, eid string) *string {
	body, _ := card["body"].(CardObject)
	els, _ := body["elements"].([]CardElement)
	for _, el := range els {
		if id, _ := el["element_id"].(string); id == eid {
			s, _ := el["content"].(string)
			return &s
		}
	}
	return nil
}

// StructureSig 卡片的「结构签名」（answer 元素 content 置空后序列化）。
// sig 不变 + answer 是 append → 元素级 typewriter；否则整卡 update。
func StructureSig(card CardObject, eid string) string {
	body, _ := card["body"].(CardObject)
	els, _ := body["elements"].([]CardElement)
	if eid == "" || len(els) == 0 {
		b, _ := json.Marshal(card)
		return string(b)
	}
	blanked := make([]CardElement, len(els))
	for i, el := range els {
		if id, _ := el["element_id"].(string); id == eid {
			dup := CardElement{}
			for k, v := range el {
				dup[k] = v
			}
			dup["content"] = ""
			blanked[i] = dup
		} else {
			blanked[i] = el
		}
	}
	// 重建 body（elements=blanked）+ 序列化整卡。
	out := CardObject{}
	for k, v := range card {
		out[k] = v
	}
	newBody := CardObject{}
	for k, v := range body {
		newBody[k] = v
	}
	newBody["elements"] = blanked
	out["body"] = newBody
	b, _ := json.Marshal(out)
	return string(b)
}
