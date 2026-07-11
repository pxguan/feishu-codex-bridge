package feishu

// chat.go —— 飞书群操作 wrapper（对齐 TS channel.rawClient.im.v1.chat.*）。
// CreateChat（建群）+ GetChat（查群名）+ GetChatMembers（拉成员）+ AddManagers
// （提管理员）+ TransferOwner（转让群主）+ LeaveChat（机器人退群）。

import (
	"context"
	"fmt"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ChatMemberInfo 群成员简档（open_id + 名字）。
type ChatMemberInfo struct {
	MemberID string
	Name     string
}

// CreateChat 建群（bot 作 owner，ownerOpenID 作为初始成员）。返回 chat_id。
func (c *Channel) CreateChat(ctx context.Context, name, ownerOpenID string) (string, error) {
	userList := []string{}
	if ownerOpenID != "" {
		userList = append(userList, ownerOpenID)
	}
	req := larkim.NewCreateChatReqBuilder().
		UserIdType("open_id").
		Body(larkim.NewCreateChatReqBodyBuilder().
			Name(name).
			UserIdList(userList).
			ChatType("private").
			Build()).
		Build()
	resp, err := c.LarkClient().Im.Chat.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("im.chat.create: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("im.chat.create: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.ChatId == nil {
		return "", fmt.Errorf("im.chat.create: no chat_id")
	}
	return *resp.Data.ChatId, nil
}

// GetChat 查群信息（群名）。
func (c *Channel) GetChat(ctx context.Context, chatID string) (name string, err error) {
	req := larkim.NewGetChatReqBuilder().
		ChatId(chatID).
		Build()
	resp, err := c.LarkClient().Im.Chat.Get(ctx, req)
	if err != nil {
		return "", fmt.Errorf("im.chat.get: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("im.chat.get: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data != nil && resp.Data.Name != nil {
		return *resp.Data.Name, nil
	}
	return "", nil
}

// GetChatMembers 拉取群成员列表（open_id + 名字），自动翻页取全。
func (c *Channel) GetChatMembers(ctx context.Context, chatID string) ([]ChatMemberInfo, error) {
	var out []ChatMemberInfo
	pageToken := ""
	for {
		req := larkim.NewGetChatMembersReqBuilder().
			ChatId(chatID).
			MemberIdType("open_id").
			PageSize(100).
			PageToken(pageToken).
			Build()
		resp, err := c.LarkClient().Im.ChatMembers.Get(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("im.chat.members.get: %w", err)
		}
		if !resp.Success() {
			return nil, fmt.Errorf("im.chat.members.get: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data != nil {
			for _, m := range resp.Data.Items {
				if m == nil {
					continue
				}
				info := ChatMemberInfo{}
				if m.MemberId != nil {
					info.MemberID = *m.MemberId
				}
				if m.Name != nil {
					info.Name = *m.Name
				}
				out = append(out, info)
			}
			if resp.Data.HasMore != nil && *resp.Data.HasMore && resp.Data.PageToken != nil {
				pageToken = *resp.Data.PageToken
				continue
			}
		}
		break
	}
	return out, nil
}

// AddManagers 提升成员为群管理员（真实调用 chatManagers.AddManagers）。
func (c *Channel) AddManagers(ctx context.Context, chatID string, managerIDs []string) error {
	if len(managerIDs) == 0 {
		return nil
	}
	req := larkim.NewAddManagersChatManagersReqBuilder().
		ChatId(chatID).
		MemberIdType("open_id").
		Body(larkim.NewAddManagersChatManagersReqBodyBuilder().ManagerIds(managerIDs).Build()).
		Build()
	resp, err := c.LarkClient().Im.ChatManagers.AddManagers(ctx, req)
	if err != nil {
		return fmt.Errorf("im.chat.managers.add: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("im.chat.managers.add: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// TransferOwner 转让群主（chat.Update owner_id）。
func (c *Channel) TransferOwner(ctx context.Context, chatID, openID string) error {
	req := larkim.NewUpdateChatReqBuilder().
		ChatId(chatID).
		UserIdType("open_id").
		Body(larkim.NewUpdateChatReqBodyBuilder().OwnerId(openID).Build()).
		Build()
	resp, err := c.LarkClient().Im.Chat.Update(ctx, req)
	if err != nil {
		return fmt.Errorf("im.chat.update(transfer): %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("im.chat.update(transfer): code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// LeaveChat 机器人退出群（best-effort：以 app_id 把自己从群成员移除）。
// 若机器人仍是群主，飞书会拒绝退群（需先 TransferOwner），此时返回 error 由调用方决定降级。
func (c *Channel) LeaveChat(ctx context.Context, chatID string) error {
	req := larkim.NewDeleteChatMembersReqBuilder().
		ChatId(chatID).
		MemberIdType("app_id").
		Body(larkim.NewDeleteChatMembersReqBodyBuilder().IdList([]string{c.AppID}).Build()).
		Build()
	resp, err := c.LarkClient().Im.ChatMembers.Delete(ctx, req)
	if err != nil {
		return fmt.Errorf("im.chat.members.delete(leave): %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("im.chat.members.delete(leave): code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}
