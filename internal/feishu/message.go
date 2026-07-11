package feishu

// message.go —— 飞书 OpenAPI 消息操作 wrapper（对齐 TS channel.rawClient.im.v1.message.*）。
// SendText/SendCard/ReplyCard/ReplyCardInThread。
// 基于 lark.Client + larkim（service/im/v1）Builder 模式。

import (
	"context"
	"encoding/json"
	"fmt"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// SendText 发文本消息到 chat。返回 message_id。
func (c *Channel) SendText(ctx context.Context, chatID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	return c.createMessage(ctx, "chat_id", chatID, "text", string(content))
}

// SendCardJSON 发交互卡片（plain JSON schema，非 CardKit 实体）到 chat。
func (c *Channel) SendCardJSON(ctx context.Context, chatID, cardJSON string) (string, error) {
	content, _ := json.Marshal(map[string]any{"type": "card", "data": cardJSON})
	return c.createMessage(ctx, "chat_id", chatID, "interactive", string(content))
}

// ReplyText 回复文本消息。
func (c *Channel) ReplyText(ctx context.Context, messageID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	return c.replyMessage(ctx, messageID, "text", string(content), false)
}

// ReplyCardJSON 回复交互卡片。
func (c *Channel) ReplyCardJSON(ctx context.Context, messageID, cardJSON string, replyInThread bool) (string, error) {
	content, _ := json.Marshal(map[string]any{"type": "card", "data": cardJSON})
	return c.replyMessage(ctx, messageID, "interactive", string(content), replyInThread)
}

// SendDM 发私聊文本消息（open_id）。
func (c *Channel) SendDM(ctx context.Context, openID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	return c.createMessage(ctx, "open_id", openID, "text", string(content))
}

// SendCardByEntity 创建 CardKit 实体 → 发引用 card_id 的消息（schema 2.0 必须）。
func (c *Channel) SendCardByEntity(ctx context.Context, chatID, cardJSON string) (string, error) {
	cardID, err := c.CreateCardKitEntity(ctx, cardJSON)
	if err != nil {
		return "", fmt.Errorf("create cardkit entity: %w", err)
	}
	content, _ := json.Marshal(map[string]any{"type": "card", "data": map[string]string{"card_id": cardID}})
	return c.createMessage(ctx, "chat_id", chatID, "interactive", string(content))
}

// SendCardByOpenID 创建 CardKit 实体 → 发到指定 open_id 的私聊（cli-bridge owner 卡用）。
func (c *Channel) SendCardByOpenID(ctx context.Context, openID, cardJSON string) (string, error) {
	cardID, err := c.CreateCardKitEntity(ctx, cardJSON)
	if err != nil {
		return "", fmt.Errorf("create cardkit entity: %w", err)
	}
	content, _ := json.Marshal(map[string]any{"type": "card", "data": map[string]string{"card_id": cardID}})
	return c.createMessage(ctx, "open_id", openID, "interactive", string(content))
}

// SendMarkdown 发 markdown 消息到群（cli-bridge 完成同步通知用）。
// 飞书 im.message.create 不支持 msg_type=markdown，故改为发一张带 markdown 元素的交互卡片，
// 这样 **加粗** / `代码` / 列表 / 标题 / 代码块等 markdown 都能原生渲染
// （对齐 TS channel.send(chatId, { markdown }) 的原有行为）。
// 注意：interactive 消息的 content 必须为 {"type":"card","data":"<卡片JSON字符串>"}，
// data 是字符串而非对象（否则报 ErrCode 11310 content's type illegal）。
func (c *Channel) SendMarkdown(ctx context.Context, chatID, markdown string) (string, error) {
	cardJSON, err := markdownCardJSON(markdown)
	if err != nil {
		return "", err
	}
	// 走 CardKit 实体路径（与权限卡/owner 卡一致）：先建实体再用 card_id 引用发送。
	// 内联 JSON 卡片（SendCardJSON 的 data 字符串形式）在本 app 下会被飞书拒绝
	// （ErrCode 200621 parse card json err / 11310 content's type illegal）。
	return c.SendCardByEntity(ctx, chatID, cardJSON)
}

// SendMarkdownInThread 群话题以「话题(thread)」形式归并到同一段对话。
// 飞书 create API 在本 SDK 版本不暴露 reply_in_thread，故采用：首次群话题当 thread 根
// （普通 create 拿 message_id），之后都用 reply API（reply_in_thread=true）挂回该根，
// 使同一群的所有完成总结归并到一段 thread，不打散主时间线。
func (c *Channel) SendMarkdownInThread(ctx context.Context, chatID, markdown string) (string, error) {
	c.threadMu.Lock()
	root, ok := c.threadRoots[chatID]
	c.threadMu.Unlock()
	if !ok {
		// 首次：作为 thread 根（普通 create）。
		id, err := c.SendMarkdown(ctx, chatID, markdown)
		if err != nil {
			return "", err
		}
		c.threadMu.Lock()
		if c.threadRoots == nil {
			c.threadRoots = map[string]string{}
		}
		c.threadRoots[chatID] = id
		c.threadMu.Unlock()
		return id, nil
	}
	// 后续：回复根消息，话题模式。
	cardJSON, err := markdownCardJSON(markdown)
	if err != nil {
		return "", err
	}
	return c.ReplyCardByEntity(ctx, root, cardJSON, true)
}

// ReplyMarkdown 以「话题(reply_in_thread)」形式回复指定消息，正文为 markdown 卡片。
// 用于普通群任务结束提醒（@ 发起人，走真实 @ 通知路径）。对齐 TS im.v1.message.reply + post at 节点，
// 但 Go 侧改用 markdown 卡片（飞书 im.message.create 不支持 msg_type=markdown，交互卡片的
// tag=markdown 元素可原生渲染，且经 CardKit 实体路径发送，规避 post 格式坑）。
func (c *Channel) ReplyMarkdown(ctx context.Context, messageID, markdown string, replyInThread bool) (string, error) {
	cardJSON, err := markdownCardJSON(markdown)
	if err != nil {
		return "", err
	}
	return c.ReplyCardByEntity(ctx, messageID, cardJSON, replyInThread)
}

// ReplyCardByEntity 创建 CardKit 实体 → 以话题(reply_in_thread)形式回复指定消息（cli-bridge 群话题 thread 用）。
func (c *Channel) ReplyCardByEntity(ctx context.Context, replyTo, cardJSON string, replyInThread bool) (string, error) {
	cardID, err := c.CreateCardKitEntity(ctx, cardJSON)
	if err != nil {
		return "", fmt.Errorf("create cardkit entity: %w", err)
	}
	content, _ := json.Marshal(map[string]any{"type": "card", "data": map[string]string{"card_id": cardID}})
	return c.replyMessage(ctx, replyTo, "interactive", string(content), replyInThread)
}

// markdownCardJSON 构造一张 schema 2.0 交互卡片 JSON（单 markdown 元素）。
// interactive 消息的 content 必须是 {"type":"card","data":"<本函数输出>"}。
func markdownCardJSON(markdown string) (string, error) {
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "content": markdown},
			},
		},
	}
	b, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal markdown card: %w", err)
	}
	return string(b), nil
}

// CreateMessageRaw 底层 im.v1.message.create（导出包装，供 cli-bridge 等直接用 receive_id_type 发消息）。
func (c *Channel) CreateMessageRaw(ctx context.Context, receiveIDType, receiveID, msgType, content string) (string, error) {
	return c.createMessage(ctx, receiveIDType, receiveID, msgType, content)
}

// createMessage 底层 im.v1.message.create。
func (c *Channel) createMessage(ctx context.Context, receiveIDType, receiveID, msgType, content string) (string, error) {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(receiveID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build()
	resp, err := c.LarkClient().Im.Message.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("im.message.create: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("im.message.create: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", fmt.Errorf("im.message.create: no message_id")
	}
	return *resp.Data.MessageId, nil
}

// replyMessage 底层 im.v1.message.reply。
func (c *Channel) replyMessage(ctx context.Context, messageID, msgType, content string, replyInThread bool) (string, error) {
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType(msgType).
			Content(content).
			ReplyInThread(replyInThread).
			Build()).
		Build()
	resp, err := c.LarkClient().Im.Message.Reply(ctx, req)
	if err != nil {
		return "", fmt.Errorf("im.message.reply: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("im.message.reply: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", fmt.Errorf("im.message.reply: no message_id")
	}
	return *resp.Data.MessageId, nil
}
