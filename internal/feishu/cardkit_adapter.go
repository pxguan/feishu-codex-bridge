package feishu

// cardkit_adapter.go —— Channel 实现 card.CardKitClient interface（桥接 feishu SDK → card.RunCardStream/ManagedRegistry）。

import (
	"context"
	"fmt"
)

// CardKitClientAdapter 包装 Channel 为 card.CardKitClient。
type CardKitClientAdapter struct {
	ch *Channel
}

// NewCardKitClientAdapter 构造 adapter。
func NewCardKitClientAdapter(ch *Channel) *CardKitClientAdapter {
	return &CardKitClientAdapter{ch: ch}
}

// CardCreate 创建 CardKit 实体。
func (a *CardKitClientAdapter) CardCreate(cardJSON string) (string, error) {
	return a.ch.CreateCardKitEntity(context.Background(), cardJSON)
}

// CardUpdate 整卡更新。
func (a *CardKitClientAdapter) CardUpdate(cardID, cardJSON string, seq int, uuid string) error {
	return a.ch.UpdateCardKitEntity(context.Background(), cardID, cardJSON, seq, uuid)
}

// CardElementContent 元素级 typewriter。
func (a *CardKitClientAdapter) CardElementContent(cardID, elementID, content string, seq int, uuid string) error {
	return a.ch.CardElementContent(context.Background(), cardID, elementID, content, seq, uuid)
}

// CardSettings PATCH 流式配置。
func (a *CardKitClientAdapter) CardSettings(cardID, settingsJSON string, seq int, uuid string) error {
	return a.ch.CardSettings(context.Background(), cardID, settingsJSON, seq, uuid)
}

// MessageCreateWithCard 发引用 CardKit 实体的消息。
func (a *CardKitClientAdapter) MessageCreateWithCard(chatID, cardID string) (string, error) {
	content := fmt.Sprintf(`{"type":"card","data":{"card_id":"%s"}}`, cardID)
	return a.ch.createMessage(context.Background(), "chat_id", chatID, "interactive", content)
}

// MessageReplyWithCard 回复引用 CardKit 实体的消息。
func (a *CardKitClientAdapter) MessageReplyWithCard(replyTo, cardID string, replyInThread bool) (string, error) {
	content := fmt.Sprintf(`{"type":"card","data":{"card_id":"%s"}}`, cardID)
	return a.ch.replyMessage(context.Background(), replyTo, "interactive", content, replyInThread)
}
