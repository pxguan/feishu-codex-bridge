package feishu

// cardkit.go —— 飞书 CardKit 实体操作 wrapper（对齐 TS channel.rawClient.cardkit.v1.card.*）。
// CreateCardKitEntity（实体创建）+ UpdateCardKitEntity（整卡更新）+ CardSettings（流式重开）+ CardElementContent（元素级 typewriter）。
// 路径：client.Cardkit.V1.Card.Create/Update/Settings + client.Cardkit.V1.CardElement.Content。

import (
	"context"
	"encoding/json"
	"fmt"

	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
)

// CreateCardKitEntity 创建 CardKit 实体。返回 card_id。
func (c *Channel) CreateCardKitEntity(ctx context.Context, cardJSON string) (string, error) {
	req := larkcardkit.NewCreateCardReqBuilder().
		Body(larkcardkit.NewCreateCardReqBodyBuilder().
			Type("card_json").
			Data(cardJSON).
			Build()).
		Build()
	resp, err := c.LarkClient().Cardkit.V1.Card.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("cardkit.card.create: %w", err)
	}
	if !resp.Success() {
		return "", &cardKitError{Code: resp.Code, Msg: resp.Msg}
	}
	if resp.Data == nil || resp.Data.CardId == nil {
		return "", fmt.Errorf("cardkit.card.create: no card_id")
	}
	return *resp.Data.CardId, nil
}

// UpdateCardKitEntity 整卡更新（单调 seq）。
func (c *Channel) UpdateCardKitEntity(ctx context.Context, cardID, cardJSON string, seq int, uuid string) error {
	req := larkcardkit.NewUpdateCardReqBuilder().
		CardId(cardID).
		Body(larkcardkit.NewUpdateCardReqBodyBuilder().
			Card(larkcardkit.NewCardBuilder().
				Type("card_json").
				Data(cardJSON).
				Build()).
			Sequence(seq).
			Uuid(uuid).
			Build()).
		Build()
	resp, err := c.LarkClient().Cardkit.V1.Card.Update(ctx, req)
	if err != nil {
		return fmt.Errorf("cardkit.card.update: %w", err)
	}
	if !resp.Success() {
		return &cardKitError{Code: resp.Code, Msg: resp.Msg}
	}
	return nil
}

// CardSettings PATCH 流式配置（重开 streaming_mode）。
func (c *Channel) CardSettings(ctx context.Context, cardID, settingsJSON string, seq int, uuid string) error {
	req := larkcardkit.NewSettingsCardReqBuilder().
		CardId(cardID).
		Body(larkcardkit.NewSettingsCardReqBodyBuilder().
			Settings(settingsJSON).
			Sequence(seq).
			Uuid(uuid).
			Build()).
		Build()
	resp, err := c.LarkClient().Cardkit.V1.Card.Settings(ctx, req)
	if err != nil {
		return fmt.Errorf("cardkit.card.settings: %w", err)
	}
	if !resp.Success() {
		return &cardKitError{Code: resp.Code, Msg: resp.Msg}
	}
	return nil
}

// CardElementContent 元素级 typewriter（cardkit.v1.cardElement.content）。
func (c *Channel) CardElementContent(ctx context.Context, cardID, elementID, content string, seq int, uuid string) error {
	req := larkcardkit.NewContentCardElementReqBuilder().
		CardId(cardID).
		ElementId(elementID).
		Body(larkcardkit.NewContentCardElementReqBodyBuilder().
			Content(content).
			Sequence(seq).
			Uuid(uuid).
			Build()).
		Build()
	resp, err := c.LarkClient().Cardkit.V1.CardElement.Content(ctx, req)
	if err != nil {
		return fmt.Errorf("cardkit.cardElement.content: %w", err)
	}
	if !resp.Success() {
		return &cardKitError{Code: resp.Code, Msg: resp.Msg}
	}
	return nil
}

// cardKitError 飞书 CardKit 业务错误（含 code，对齐 card.CardkitError）。
type cardKitError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (e *cardKitError) Error() string { return e.Msg }

func isValidJSON(s string) bool {
	var v any
	return json.Unmarshal([]byte(s), &v) == nil
}
