package feishu

// onboarding.go —— 建群后的 onboarding 飞书操作（对齐 TS project/onboarding.ts）：
// 发欢迎卡由调用方用 SendCardFunc 走 CardKit 实体路径（本 app 不接受内联 JSON），
// 这里只补齐 Pin / 群 Tab / 群菜单三项原生 API。
//
// 所需权限（best-effort：缺权限时调用方仅告警、不阻断建群）：
//   - im:chat:pin             （Pin 消息）
//   - im:chat.tabs:write_only （加群 Tab）
//   - im:chat.menu_tree:write_only（加群菜单）

import (
	"context"
	"fmt"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// PinMessage 把一条消息 Pin 到群（对齐 TS im.v1.pin.create）。
func (c *Channel) PinMessage(ctx context.Context, messageID string) error {
	resp, err := c.LarkClient().Im.V1.Pin.Create(ctx,
		larkim.NewCreatePinReqBuilder().
			Body(larkim.NewCreatePinReqBodyBuilder().MessageId(messageID).Build()).
			Build(),
	)
	if err != nil {
		return fmt.Errorf("pin.create: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("pin.create: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// AddChatTab 给群加一个 url 类型的会话标签页（对齐 TS im.v1.chatTab.create）。
func (c *Channel) AddChatTab(ctx context.Context, chatID, name, url string) error {
	tab := larkim.NewChatTabBuilder().
		TabName(name).
		TabType("url").
		TabContent(larkim.NewChatTabContentBuilder().Url(url).Build()).
		Build()
	resp, err := c.LarkClient().Im.V1.ChatTab.Create(ctx,
		larkim.NewCreateChatTabReqBuilder().
			ChatId(chatID).
			Body(larkim.NewCreateChatTabReqBodyBuilder().ChatTabs([]*larkim.ChatTab{tab}).Build()).
			Build(),
	)
	if err != nil {
		return fmt.Errorf("chatTab.create: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("chatTab.create: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// AddChatMenu 给群加一个「跳转链接」类型的一级菜单（对齐 TS im.v1.chatMenuTree.create）。
// pcURL 为空时退化为仅 common_url（PC 端也会走原链接）。
func (c *Channel) AddChatMenu(ctx context.Context, chatID, name, url, pcURL string) error {
	linkBuilder := larkim.NewChatMenuItemRedirectLinkBuilder().CommonUrl(url)
	if pcURL != "" {
		linkBuilder = linkBuilder.PcUrl(pcURL)
	}
	item := larkim.NewChatMenuItemBuilder().
		ActionType("REDIRECT_LINK").
		Name(name).
		RedirectLink(linkBuilder.Build()).
		Build()
	top := larkim.NewChatMenuTopLevelBuilder().ChatMenuItem(item).Build()
	resp, err := c.LarkClient().Im.V1.ChatMenuTree.Create(ctx,
		larkim.NewCreateChatMenuTreeReqBuilder().
			ChatId(chatID).
			Body(larkim.NewCreateChatMenuTreeReqBodyBuilder().
				MenuTree(larkim.NewChatMenuTreeBuilder().ChatMenuTopLevels([]*larkim.ChatMenuTopLevel{top}).Build()).
				Build()).
			Build(),
	)
	if err != nil {
		return fmt.Errorf("chatMenuTree.create: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("chatMenuTree.create: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}
