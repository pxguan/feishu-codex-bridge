package card

// run_card_stream_main.go —— RunCardStream 主体（对齐 TS card/run-card-stream 的类方法）。
// 通过 CardKitClient interface 注入飞书 SDK cardkit/im 调用（可 mock 测）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// CardKitClient 飞书 cardkit/im 操作抽象（飞书 SDK 实现 + 测试 mock）。
type CardKitClient interface {
	CardCreate(cardJSON string) (cardID string, err error)
	CardUpdate(cardID, cardJSON string, seq int, uuid string) error
	CardElementContent(cardID, elementID, content string, seq int, uuid string) error
	CardSettings(cardID, settingsJSON string, seq int, uuid string) error
	MessageCreateWithCard(chatID, cardID string) (messageID string, err error)
	MessageReplyWithCard(replyTo, cardID string, replyInThread bool) (messageID string, err error)
}

// IsCardIdNotReady cardId 未就绪判据（230099 / 11310 "cardid is invalid"）。
// cardkit 实体刚 create 偶尔未传播 → message 引用 400。重试 create+send。
func IsCardIdNotReady(err error) bool {
	code := CardkitErrCode(err)
	return code == 230099 || code == 11310
}

// RunCardStreamOptions 构造参数。
type RunCardStreamOptions struct {
	Client CardKitClient
	Now    func() int64   // 默认 time.Now().UnixMilli()
	Sleep  func(ms int64) // 默认 time.Sleep
}

// RunCardStream 一个 CardKit 2.0 实体支撑的流式运行卡。
type RunCardStream struct {
	client CardKitClient
	pacer  *ChatPacer
	now    func() int64
	sleep  func(ms int64)

	mu          sync.Mutex
	cardID      string
	messageID   string
	seq         int
	lastPush    int64
	lastContent string

	// coalesced pump
	pending  chan pumpFrame
	pumpOnce sync.Once
	pumpDone chan struct{}
	stopPump chan struct{}

	// baselines（pump 路由判据）
	lastStructureSig string
	lastAnswerText   string

	// 统计
	pushCount, cardPushes, elPushes int
	totalRttMs, maxRttMs            int64
}

type pumpFrame struct {
	card      CardObject
	answerEID string
}

// NewRunCardStream 构造。
func NewRunCardStream(opts RunCardStreamOptions) *RunCardStream {
	if opts.Now == nil {
		opts.Now = func() int64 { return time.Now().UnixMilli() }
	}
	if opts.Sleep == nil {
		opts.Sleep = func(ms int64) { time.Sleep(time.Duration(ms) * time.Millisecond) }
	}
	return &RunCardStream{
		client:   opts.Client,
		now:      opts.Now,
		sleep:    opts.Sleep,
		pending:  make(chan pumpFrame, 64),
		pumpDone: make(chan struct{}),
		stopPump: make(chan struct{}),
	}
}

// MessageID 载体消息 id。
func (s *RunCardStream) MessageID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageID
}

// CardID CardKit 实体 id。
func (s *RunCardStream) CardID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cardID
}

// Stats 推送计数 + RTT 统计。
type StreamStats struct {
	PushCount, CardPushes, ElPushes int
	TotalRttMs, MaxRttMs            int64
}

func (s *RunCardStream) Stats() StreamStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StreamStats{PushCount: s.pushCount, CardPushes: s.cardPushes, ElPushes: s.elPushes, TotalRttMs: s.totalRttMs, MaxRttMs: s.maxRttMs}
}

// Create 创建 CardKit 实体 + 发引用消息；cardId 未就绪重试（最多 2 次）。
func (s *RunCardStream) Create(ctx context.Context, chatID string, initialCard CardObject, replyTo string, replyInThread bool) (string, error) {
	for attempt := 0; ; attempt++ {
		cardJSON, _ := json.Marshal(initialCard)
		cardID, err := s.client.CardCreate(string(cardJSON))
		if err != nil {
			if attempt >= 2 || !IsCardIdNotReady(err) {
				return "", fmt.Errorf("card.create: %w", err)
			}
			s.sleep(int64(400 * (attempt + 1)))
			continue
		}
		s.mu.Lock()
		s.cardID = cardID
		s.lastContent = string(cardJSON)
		s.mu.Unlock()

		var msgID string
		if replyTo != "" {
			msgID, err = s.client.MessageReplyWithCard(replyTo, cardID, replyInThread)
		} else {
			msgID, err = s.client.MessageCreateWithCard(chatID, cardID)
		}
		if err != nil {
			if attempt >= 2 || !IsCardIdNotReady(err) {
				return "", fmt.Errorf("card.send: %w", err)
			}
			s.sleep(int64(400 * (attempt + 1)))
			continue
		}
		s.mu.Lock()
		s.messageID = msgID
		s.mu.Unlock()
		return msgID, nil
	}
}

// StreamCard 节流的整卡更新（去重 + 限流）；force 跳过节流。返回是否投递成功。
func (s *RunCardStream) StreamCard(ctx context.Context, fullCard CardObject, force bool) bool {
	s.mu.Lock()
	if s.cardID == "" {
		s.mu.Unlock()
		return false
	}
	data, _ := json.Marshal(fullCard)
	if string(data) == s.lastContent {
		s.mu.Unlock()
		return true
	}
	now := s.now()
	if !force && now-s.lastPush < StreamThrottleMS {
		s.mu.Unlock()
		return false
	}
	s.lastPush = now
	cardID := s.cardID
	seq := s.nextSeqLocked()
	s.mu.Unlock()

	if s.pacer != nil {
		s.pacer.Wait(s.now(), s.sleep)
	}
	t0 := s.now()
	uuid := fmt.Sprintf("s_%s_%d", cardID, seq)
	if err := s.client.CardUpdate(cardID, string(data), seq, uuid); err != nil {
		if IsRateLimited(err) && s.pacer != nil {
			s.pacer.Penalize(s.now())
		}
		return false
	}
	s.mu.Lock()
	s.lastContent = string(data)
	s.recordRttLocked(s.now() - t0)
	s.cardPushes++
	s.pushCount++
	s.mu.Unlock()
	return true
}

// UpdateCard 终态整卡更新（429 指数退避 + 200810 等 3.2s 重试）。
func (s *RunCardStream) UpdateCard(ctx context.Context, fullCard CardObject) {
	s.mu.Lock()
	if s.cardID == "" {
		s.mu.Unlock()
		return
	}
	data, _ := json.Marshal(fullCard)
	cardID := s.cardID
	s.mu.Unlock()

	for i := 0; ; i++ {
		if s.pacer != nil {
			s.pacer.Wait(s.now(), s.sleep)
		}
		s.mu.Lock()
		seq := s.nextSeqLocked()
		s.mu.Unlock()
		uuid := fmt.Sprintf("u_%s_%d", cardID, seq)
		err := s.client.CardUpdate(cardID, string(data), seq, uuid)
		if err == nil {
			s.mu.Lock()
			s.lastContent = string(data)
			s.mu.Unlock()
			return
		}
		rl := IsRateLimited(err)
		if rl && s.pacer != nil {
			s.pacer.Penalize(s.now())
		}
		maxRetry := 1
		if rl {
			maxRetry = TerminalRLRetries
		}
		if i >= maxRetry {
			return
		}
		if rl {
			s.sleep(int64(RLBackoffBaseMS * (1 << i)))
		} else {
			s.sleep(3200) // 200810 点击锁窗口
		}
	}
}

// StreamCoalesced 记录最新帧 + 确保 pump 运行（非阻塞）。
func (s *RunCardStream) StreamCoalesced(ctx context.Context, fullCard CardObject, answerEID string) {
	select {
	case s.pending <- pumpFrame{card: fullCard, answerEID: answerEID}:
	default:
		// 缓冲满：丢最旧（非阻塞保消费循环不卡）。
		select {
		case <-s.pending:
		default:
		}
		s.pending <- pumpFrame{card: fullCard, answerEID: answerEID}
	}
	s.pumpOnce.Do(func() { go s.runPump(ctx) })
}

// Drain 等在途 coalesced push 落地（终态前调）。
func (s *RunCardStream) Drain(ctx context.Context) {
	// 发一个 nil 帧让 pump drain 完。
	select {
	case s.pending <- pumpFrame{}:
	default:
	}
	// 等 pump 处理完当前批次（简化：短暂等 pending 空）。
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if len(s.pending) == 0 {
			return
		}
		s.sleep(10)
	}
}

func (s *RunCardStream) runPump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-s.pending:
			if !ok || f.card == nil {
				continue
			}
			s.pumpFrame(ctx, f)
		}
	}
}

func (s *RunCardStream) pumpFrame(ctx context.Context, f pumpFrame) {
	t0 := s.now()
	answer := (*string)(nil)
	if f.answerEID != "" {
		answer = AnswerContent(f.card, f.answerEID)
	}
	sig := StructureSig(f.card, f.answerEID)
	s.mu.Lock()
	lastSig := s.lastStructureSig
	lastAnswer := s.lastAnswerText
	s.mu.Unlock()

	if f.answerEID != "" && answer != nil && sig == lastSig && *answer != lastAnswer && hasPrefix(*answer, lastAnswer) {
		// 结构不变 + answer append → 元素级 typewriter。
		if s.streamElement(ctx, f.answerEID, *answer) {
			s.mu.Lock()
			s.lastAnswerText = *answer
			s.mu.Unlock()
		}
	} else {
		if s.StreamCard(ctx, f.card, true) {
			s.mu.Lock()
			s.lastStructureSig = sig
			if answer != nil {
				s.lastAnswerText = *answer
			}
			s.mu.Unlock()
		}
	}
	gap := StreamThrottleMS - (s.now() - t0)
	if gap > 0 {
		s.sleep(gap)
	}
}

func (s *RunCardStream) streamElement(ctx context.Context, elementID, content string) bool {
	s.mu.Lock()
	if s.cardID == "" {
		s.mu.Unlock()
		return false
	}
	cardID := s.cardID
	seq := s.nextSeqLocked()
	s.mu.Unlock()

	if s.pacer != nil {
		s.pacer.Wait(s.now(), s.sleep)
	}
	t0 := s.now()
	push := func(seq int) error {
		uuid := fmt.Sprintf("e_%s_%d", cardID, seq)
		return s.client.CardElementContent(cardID, elementID, content, seq, uuid)
	}
	err := push(seq)
	if err != nil {
		code := CardkitErrCode(err)
		if code == ErrStreamingOff {
			// 重开流式 + 重发。
			s.mu.Lock()
			settingsSeq := s.nextSeqLocked()
			s.mu.Unlock()
			_ = s.client.CardSettings(cardID, `{"config":{"streaming_mode":true}}`, settingsSeq, fmt.Sprintf("o_%s_%d", cardID, settingsSeq))
			s.mu.Lock()
			seq = s.nextSeqLocked()
			s.mu.Unlock()
			err = push(seq)
		} else if code == ErrSeqOutOfOrder {
			s.mu.Lock()
			seq = s.nextSeqLocked()
			s.mu.Unlock()
			err = push(seq)
		}
	}
	if err != nil {
		if IsRateLimited(err) && s.pacer != nil {
			s.pacer.Penalize(s.now())
		}
		return false
	}
	rtt := s.now() - t0
	s.mu.Lock()
	s.recordRttLocked(rtt)
	s.elPushes++
	s.pushCount++
	s.mu.Unlock()
	return true
}

func (s *RunCardStream) nextSeqLocked() int {
	s.seq++
	return s.seq
}

func (s *RunCardStream) recordRttLocked(rtt int64) {
	s.totalRttMs += rtt
	if rtt > s.maxRttMs {
		s.maxRttMs = rtt
	}
}

// SetPacer 绑定 per-chat 限流器（create 时调用）。
func (s *RunCardStream) SetPacer(p *ChatPacer) { s.pacer = p }

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
