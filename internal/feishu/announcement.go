package feishu

// announcement.go —— 群公告写入（对齐 TS project/announcement.ts）。
// 群公告是 docx 公告块（chat.announcement.block），写入一行文本并置顶到群顶部横幅。
// 需要飞书权限：
//   - im:chat.announcement:read        （list 公告块）
//   - im:chat.announcement:write_only  （清空 + 写入公告块）
//   - im:chat.top_notice:write_only     （置顶公告，best-effort）

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/core"

	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	announcementPageBlockType  = 1
	announcementTextBlockType  = 2
	announcementLatestRevision = -1
	announcementMaxRetries     = 5
)

func ptrInt(i int) *int { return &i }

// SetGroupAnnouncement 写入群公告并置顶到群顶部横幅，对齐 TS setAnnouncement。
// text 是一行公告文本（通常形如 "📁 name · 📂 cwd · 🌿 branch"）。
func (c *Channel) SetGroupAnnouncement(ctx context.Context, chatID, text string) error {
	var lastErr error
	for attempt := 0; attempt < announcementMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(800*attempt) * time.Millisecond)
		}
		err := c.writeAnnouncementOnce(ctx, chatID, text)
		if err == nil {
			if perr := c.pinAnnouncement(ctx, chatID); perr != nil {
				// 置顶 best-effort：内容已写入，pin 失败仅告警（对齐 TS）
				core.Warn(ctx, "feishu", "announcement-pin", "置顶群公告失败（可忽略）："+perr.Error())
			}
			return nil
		}
		lastErr = err
		// scope/权限类错误重试无意义，fast-fail 并给出授权提示
		if isFatalAnnouncementErr(err) {
			return fmt.Errorf("%w（请到飞书开放平台给 bot 应用授予 im:chat.announcement:read / im:chat.announcement:write_only 权限；若也要置顶还需 im:chat.top_notice:write_only）", err)
		}
	}
	return fmt.Errorf("群公告写入失败（重试 %d 次）：%w", announcementMaxRetries, lastErr)
}

// writeAnnouncementOnce 单次写入：list 公告 page block → 清空 children → 插入一行文本。
func (c *Channel) writeAnnouncementOnce(ctx context.Context, chatID, text string) error {
	cli := c.LarkClient()

	// 1. list 公告块，找 page block（block_type==1）
	listReq := larkdocx.NewListChatAnnouncementBlockReqBuilder().ChatId(chatID).Build()
	listResp, err := cli.Docx.V1.ChatAnnouncementBlock.List(ctx, listReq)
	if err != nil {
		return fmt.Errorf("docx announcement list: %w", err)
	}
	if !listResp.Success() {
		return fmt.Errorf("docx announcement list: code=%d msg=%s", listResp.Code, listResp.Msg)
	}
	var pageID string
	existing := 0
	if listResp.Data != nil {
		for _, b := range listResp.Data.Items {
			if b.BlockType != nil && *b.BlockType == announcementPageBlockType && b.BlockId != nil {
				pageID = *b.BlockId
				existing = len(b.Children)
				break
			}
		}
	}
	if pageID == "" {
		return fmt.Errorf("群公告缺少 page block")
	}

	// 2. 清空已有 children（如有）
	if existing > 0 {
		delReq := larkdocx.NewBatchDeleteChatAnnouncementBlockChildrenReqBuilder().
			ChatId(chatID).
			BlockId(pageID).
			RevisionId(announcementLatestRevision).
			Body(larkdocx.NewBatchDeleteChatAnnouncementBlockChildrenReqBodyBuilder().
				StartIndex(0).EndIndex(existing).Build()).
			Build()
		delResp, err := cli.Docx.V1.ChatAnnouncementBlockChildren.BatchDelete(ctx, delReq)
		if err != nil {
			return fmt.Errorf("docx announcement delete: %w", err)
		}
		if !delResp.Success() {
			return fmt.Errorf("docx announcement delete: code=%d msg=%s", delResp.Code, delResp.Msg)
		}
	}

	// 3. 写入一行 text block
	textBlock := &larkdocx.Block{
		BlockType: ptrInt(announcementTextBlockType),
		Text: &larkdocx.Text{
			Elements: []*larkdocx.TextElement{
				{TextRun: &larkdocx.TextRun{Content: &text}},
			},
		},
	}
	createReq := larkdocx.NewCreateChatAnnouncementBlockChildrenReqBuilder().
		ChatId(chatID).
		BlockId(pageID).
		RevisionId(announcementLatestRevision).
		Body(larkdocx.NewCreateChatAnnouncementBlockChildrenReqBodyBuilder().
			Index(0).
			Children([]*larkdocx.Block{textBlock}).
			Build()).
		Build()
	createResp, err := cli.Docx.V1.ChatAnnouncementBlockChildren.Create(ctx, createReq)
	if err != nil {
		return fmt.Errorf("docx announcement create: %w", err)
	}
	if !createResp.Success() {
		return fmt.Errorf("docx announcement create: code=%d msg=%s", createResp.Code, createResp.Msg)
	}
	return nil
}

// pinAnnouncement 把群公告置顶到群顶部横幅（action_type=2=公告）。
func (c *Channel) pinAnnouncement(ctx context.Context, chatID string) error {
	req := larkim.NewPutTopNoticeChatTopNoticeReqBuilder().
		ChatId(chatID).
		Body(larkim.NewPutTopNoticeChatTopNoticeReqBodyBuilder().
			ChatTopNotice([]*larkim.ChatTopNotice{
				larkim.NewChatTopNoticeBuilder().ActionType("2").Build(),
			}).Build()).
		Build()
	resp, err := c.LarkClient().Im.V1.ChatTopNotice.PutTopNotice(ctx, req)
	if err != nil {
		return fmt.Errorf("im chatTopNotice put: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("im chatTopNotice put: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// isFatalAnnouncementErr 判断是否为 scope/权限类错误（重试无意义）。
func isFatalAnnouncementErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range []string{"scope", "permission", "无权限", "未授权", "access denied", "not allowed", "forbidden"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
