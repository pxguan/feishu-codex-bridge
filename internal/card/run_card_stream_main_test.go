package card

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// mockCardKitClient 测试用 CardKitClient mock。
type mockCardKitClient struct {
	mu           sync.Mutex
	cardIDSeq    int
	createdCards []string
	updates      []string
	elemPushes   []string
	settings     []string
	messages     []string
	failCreate   error // 注入 create 错误
	failUpdate   error
	failElement  error
}

func (m *mockCardKitClient) CardCreate(cardJSON string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCreate != nil {
		err := m.failCreate
		m.failCreate = nil
		return "", err
	}
	m.cardIDSeq++
	id := "card_" + itoaTest(m.cardIDSeq)
	m.createdCards = append(m.createdCards, id)
	return id, nil
}
func (m *mockCardKitClient) CardUpdate(cardID, cardJSON string, seq int, uuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failUpdate != nil {
		err := m.failUpdate
		m.failUpdate = nil
		return err
	}
	m.updates = append(m.updates, cardJSON)
	return nil
}
func (m *mockCardKitClient) CardElementContent(cardID, elementID, content string, seq int, uuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failElement != nil {
		err := m.failElement
		m.failElement = nil
		return err
	}
	m.elemPushes = append(m.elemPushes, content)
	return nil
}
func (m *mockCardKitClient) CardSettings(cardID, settingsJSON string, seq int, uuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = append(m.settings, settingsJSON)
	return nil
}
func (m *mockCardKitClient) MessageCreateWithCard(chatID, cardID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, "om_"+cardID)
	return "om_" + cardID, nil
}
func (m *mockCardKitClient) MessageReplyWithCard(replyTo, cardID string, replyInThread bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return "om_reply_" + cardID, nil
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

// cardOne 单元素卡片 helper（Card 需 []CardElement）。
func cardOne(el CardElement) CardObject { return Card([]CardElement{el}, CardOpts{}) }

func TestRunCardStream_Create(t *testing.T) {
	mc := &mockCardKitClient{}
	s := NewRunCardStream(RunCardStreamOptions{Client: mc})
	msgID, err := s.Create(context.Background(), "chat_1", cardOne(Md("hi")), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if msgID == "" || s.CardID() == "" {
		t.Fatal("create should set cardID + messageID")
	}
	if len(mc.createdCards) != 1 {
		t.Fatalf("should create 1 card: %d", len(mc.createdCards))
	}
}

func TestRunCardStream_CreateRetriesCardIdNotReady(t *testing.T) {
	mc := &mockCardKitClient{failCreate: &CardkitError{Code: 230099}}
	s := NewRunCardStream(RunCardStreamOptions{Client: mc})
	_, err := s.Create(context.Background(), "chat_1", cardOne(Md("hi")), "", false)
	if err != nil {
		t.Fatal("230099 should retry and succeed")
	}
	if len(mc.createdCards) != 1 {
		t.Fatalf("230099 retry should land 1 successful create: %d", len(mc.createdCards))
	}
}

func TestRunCardStream_StreamCard(t *testing.T) {
	mc := &mockCardKitClient{}
	s := NewRunCardStream(RunCardStreamOptions{Client: mc})
	s.Create(context.Background(), "chat_1", cardOne(Md("v1")), "", false)
	mc.updates = nil
	ok := s.StreamCard(context.Background(), cardOne(Md("v2")), true)
	if !ok {
		t.Fatal("streamCard should succeed")
	}
	if len(mc.updates) != 1 {
		t.Fatalf("should push 1 update: %d", len(mc.updates))
	}
}

func TestRunCardStream_StreamCardDedup(t *testing.T) {
	mc := &mockCardKitClient{}
	s := NewRunCardStream(RunCardStreamOptions{Client: mc})
	card := cardOne(Md("same"))
	s.Create(context.Background(), "chat_1", card, "", false)
	mc.updates = nil
	// 相同内容 → 去重（返回 true 但不 push）。
	if !s.StreamCard(context.Background(), card, true) {
		t.Fatal("identical should return true (deduped)")
	}
	if len(mc.updates) != 0 {
		t.Fatal("identical content should be deduped (no update)")
	}
}

func TestRunCardStream_StreamElement(t *testing.T) {
	mc := &mockCardKitClient{}
	s := NewRunCardStream(RunCardStreamOptions{Client: mc})
	s.Create(context.Background(), "chat_1", cardOne(MdStream("hi", AnswerEID)), "", false)
	ok := s.streamElement(context.Background(), AnswerEID, "hello world")
	if !ok {
		t.Fatal("streamElement should succeed")
	}
	if len(mc.elemPushes) != 1 || mc.elemPushes[0] != "hello world" {
		t.Fatalf("element push wrong: %v", mc.elemPushes)
	}
}

func TestRunCardStream_StreamElementReopensStreaming(t *testing.T) {
	mc := &mockCardKitClient{failElement: &CardkitError{Code: ErrStreamingOff}}
	s := NewRunCardStream(RunCardStreamOptions{Client: mc})
	s.Create(context.Background(), "chat_1", cardOne(MdStream("hi", AnswerEID)), "", false)
	ok := s.streamElement(context.Background(), AnswerEID, "hello")
	if !ok {
		t.Fatal("300309 should reopen streaming + retry")
	}
	if len(mc.settings) != 1 || !strings.Contains(mc.settings[0], "streaming_mode") {
		t.Fatalf("should call CardSettings to reopen: %v", mc.settings)
	}
}

func TestIsCardIdNotReady(t *testing.T) {
	if !IsCardIdNotReady(&CardkitError{Code: 230099}) {
		t.Fatal("230099 → not ready")
	}
	if !IsCardIdNotReady(&CardkitError{Code: 11310}) {
		t.Fatal("11310 → not ready")
	}
	if IsCardIdNotReady(&CardkitError{Code: 99991400}) {
		t.Fatal("99991400 (rate limit) is NOT cardId-not-ready")
	}
}

func TestRunCardStream_Stats(t *testing.T) {
	mc := &mockCardKitClient{}
	s := NewRunCardStream(RunCardStreamOptions{Client: mc})
	s.Create(context.Background(), "chat_1", cardOne(Md("v1")), "", false)
	s.StreamCard(context.Background(), cardOne(Md("v2")), true)
	stats := s.Stats()
	if stats.PushCount == 0 || stats.CardPushes == 0 {
		t.Fatalf("stats should count pushes: %+v", stats)
	}
}
