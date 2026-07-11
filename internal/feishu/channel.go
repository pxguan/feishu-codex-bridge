package feishu

// channel.go —— 飞书长连接 + OpenAPI channel（对齐 TS bot/bridge.ts 的 LarkChannel）。
// 长连接：ws.NewClient + Start + EventDispatcher（事件订阅）。
// OpenAPI：lark.NewClient + client.Im/Cardkit/Drive/Contact（发消息/卡片/建群/评论）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

// OnMessageFunc 消息事件回调（由 Orchestrator.OnMessage 注入）。chatType=p2p|group。
type OnMessageFunc func(ctx context.Context, msgID, chatID, threadID, senderID, senderName, senderType, msgType, content, chatType string) error

// OnCardActionFunc 卡片回调事件回调。
type OnCardActionFunc func(ctx context.Context, raw []byte) error

// OnReactionFunc 表情回复事件回调（im.message.reaction.created_v1）。
// messageID=被加表情的消息；emojiType=表情类型；operatorType=操作人类型(user/app)；
// operatorOpenID=操作人 open_id。
type OnReactionFunc func(ctx context.Context, messageID, emojiType, operatorType, operatorOpenID string) error

// OnBotMenuFunc 飞书 bot 菜单点击事件回调（application.bot.menu_v6）。
// openID=点击者 open_id；eventKey=菜单项 key；eventID=事件去重 id。
type OnBotMenuFunc func(ctx context.Context, openID, eventKey, eventID string) error

// OnBotAddedFunc 机器人被加入群事件回调（im.chat.member.bot.added_v1）。
// chatID=群 ID；operatorOpenID=操作者 open_id；chatName=群名。
type OnBotAddedFunc func(ctx context.Context, chatID, operatorOpenID, chatName string) error

// OnBotDeletedFunc 机器人被移出群事件回调（im.chat.member.bot.deleted_v1）。
// chatID=群 ID；operatorOpenID=操作者 open_id。
type OnBotDeletedFunc func(ctx context.Context, chatID, operatorOpenID string) error

// OnCommentFunc 云文档评论 @bot 事件回调（drive.notice.comment_add_v1）。
// fileToken/fileType=文档标识；commentID=评论 ID；replyID=被 @的回复 ID（可能为空）；
// isMentioned=机器人是否被 @；noticeType=add_comment|add_reply。
type OnCommentFunc func(ctx context.Context, fileToken, fileType, commentID, replyID string, isMentioned bool, noticeType string) error

// OnConnectedFunc 长连接成功建立后的回调（一次）。用于「事件已生效」播报等启动收尾。
type OnConnectedFunc func(ctx context.Context)

// Channel 飞书长连接 + OpenAPI 操作。
type Channel struct {
	AppID             string
	AppSecret         string
	Tenant            string
	VerificationToken string
	EncryptKey        string
	larkCli           *lark.Client
	wsCli             *ws.Client
	OnMessage         OnMessageFunc
	OnCardAction      OnCardActionFunc
	OnReaction        OnReactionFunc
	OnBotMenu         OnBotMenuFunc
	OnBotAdded        OnBotAddedFunc
	OnBotDeleted      OnBotDeletedFunc
	OnComment         OnCommentFunc
	// OnConnected 长连接成功建立后触发（一次）：用于「事件已生效」播报等启动收尾。
	// 在 Connect 内以 goroutine 调用，不阻塞 WS 启动；nil 则跳过。
	OnConnected       OnConnectedFunc

	// threadRoots 维护各群的「话题(thread)根消息」id，使同一群的多次群话题
	// 归并到同一段 thread（飞书 create 接口在本 SDK 版本不暴露 reply_in_thread，
	// 故首次群话题当根、之后用 reply API 挂回该根）。仅内存，daemon 重启后丢弃
	// （重启后首次群话题会再建一个根，可接受）。
	threadMu    sync.Mutex
	threadRoots map[string]string

	// connMu / connState 维护 WS 长连接状态（供诊断卡真实展示）。
	// 初始 "disconnected"；SDK 的 OnReady/OnReconnecting/OnReconnected/OnDisconnected
	// 回调驱动更新。仅内存。
	connMu    sync.RWMutex
	connState string
}

// ConnState 当前飞书长连接状态（"connected" | "connecting" | "reconnecting" |
// "disconnected"）。供诊断卡真实展示，不依赖网络探测。
func (c *Channel) ConnState() string {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	if c.connState == "" {
		return "disconnected"
	}
	return c.connState
}

// setConnState 内部更新长连接状态（由 SDK 回调触发）。
func (c *Channel) setConnState(s string) {
	c.connMu.Lock()
	c.connState = s
	c.connMu.Unlock()
}

// NewChannel 构造（未连接）。
func NewChannel(appID, appSecret, tenant string) *Channel {
	return &Channel{AppID: appID, AppSecret: appSecret, Tenant: tenant, threadRoots: map[string]string{}}
}

// LarkClient 返回底层 lark client（OpenAPI 调用用；首次调时懒建）。
func (c *Channel) LarkClient() *lark.Client {
	if c.larkCli == nil {
		c.larkCli = lark.NewClient(c.AppID, c.AppSecret)
	}
	return c.larkCli
}

// CardKitClient 返回 card.CardKitClient（供 bot 层流式运行卡 RunCardStream 使用）。
func (c *Channel) CardKitClient() card.CardKitClient {
	return NewCardKitClientAdapter(c)
}

// AddMessageReaction 给指定消息加表情回复（飞书 message reaction）。返回 reaction_id。
// 需 im:message.reactions:write_only 权限；失败向上返回由调用方决定降级。
func (c *Channel) AddMessageReaction(ctx context.Context, messageID, emojiType string) (string, error) {
	resp, err := c.LarkClient().Im.MessageReaction.Create(ctx,
		larkim.NewCreateMessageReactionReqBuilder().
			MessageId(messageID).
			Body(larkim.NewCreateMessageReactionReqBodyBuilder().
				ReactionType(larkim.NewEmojiBuilder().EmojiType(emojiType).Build()).
				Build()).
			Build())
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", fmt.Errorf("add message reaction %q failed: code=%d msg=%s", emojiType, resp.Code, resp.Msg)
	}
	if resp.Data != nil && resp.Data.ReactionId != nil {
		return *resp.Data.ReactionId, nil
	}
	return "", nil
}

// RemoveMessageReaction 移除指定消息上的表情回复（message_id + reaction_id）。
func (c *Channel) RemoveMessageReaction(ctx context.Context, messageID, reactionID string) error {
	resp, err := c.LarkClient().Im.MessageReaction.Delete(ctx,
		larkim.NewDeleteMessageReactionReqBuilder().
			MessageId(messageID).
			ReactionId(reactionID).
			Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("remove message reaction failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// Connect 启动长连接 + 注册事件 dispatcher（消息/卡片回调）。
func (c *Channel) Connect(ctx context.Context) error {
	// 构造 EventDispatcher（verificationToken + encryptKey 用于事件验签/解密）。
	dp := dispatcher.NewEventDispatcher(c.VerificationToken, c.EncryptKey)

	// 注册 IM 消息接收事件（P2 协议——飞书长连接推送的格式）。
	dp.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		if c.OnMessage == nil {
			return nil
		}
		msg := event.Event.Message
		sender := event.Event.Sender
		if msg == nil {
			return nil
		}
		msgID := ptrStr(msg.MessageId)
		chatID := ptrStr(msg.ChatId)
		threadID := ptrStr(msg.RootId)
		if threadID == "" {
			threadID = ptrStr(msg.ThreadId)
		}
		msgType := ptrStr(msg.MessageType)
		chatType := ptrStr(msg.ChatType)
		senderID := ""
		senderType := "user"
		if sender != nil {
			senderType = ptrStr(sender.SenderType)
			if sender.SenderId != nil {
				senderID = ptrStr(sender.SenderId.OpenId)
			}
		}
		content := ptrStr(msg.Content)
		return c.OnMessage(ctx, msgID, chatID, threadID, senderID, "", senderType, msgType, content, chatType)
	})

	// 注册卡片回调（自定义事件，handler 接 *larkevent.EventReq，Body 在 EventReq.Body []byte）。
	dp.OnCustomizedEvent("card.action.trigger", func(ctx context.Context, eventReq *larkevent.EventReq) error {
		if c.OnCardAction != nil {
			return c.OnCardAction(ctx, eventReq.Body)
		}
		return nil
	})

	// 注册表情回复事件（running 卡 OK/DONE=终止，终态卡 👍=续轮）。
	dp.OnP2MessageReactionCreatedV1(func(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) error {
		if c.OnReaction == nil || event.Event == nil {
			return nil
		}
		e := event.Event
		messageID := ptrStr(e.MessageId)
		emojiType := ""
		if e.ReactionType != nil {
			emojiType = ptrStr(e.ReactionType.EmojiType)
		}
		operatorType := ptrStr(e.OperatorType)
		operatorOpenID := ""
		if e.UserId != nil {
			operatorOpenID = ptrStr(e.UserId.OpenId)
		}
		return c.OnReaction(ctx, messageID, emojiType, operatorType, operatorOpenID)
	})

	// 注册 bot 菜单点击事件（application.bot.menu_v6：用户在飞书 bot 资料页 / 私聊菜单点菜单项）。
	// 事件体字段在顶层或 event 下都可能出现（schema 版本差异），两者都读。
	dp.OnCustomizedEvent("application.bot.menu_v6", func(ctx context.Context, eventReq *larkevent.EventReq) error {
		if c.OnBotMenu == nil {
			return nil
		}
		var ev struct {
			EventID  string `json:"event_id"`
			EventKey string `json:"event_key"`
			Operator struct {
				OperatorID struct {
					OpenID string `json:"open_id"`
				} `json:"operator_id"`
			} `json:"operator"`
			Event struct {
				EventKey string `json:"event_key"`
				Operator struct {
					OperatorID struct {
						OpenID string `json:"open_id"`
					} `json:"operator_id"`
				} `json:"operator"`
			} `json:"event"`
		}
		if err := json.Unmarshal(eventReq.Body, &ev); err != nil {
			core.Warn(ctx, "feishu", "bot-menu-parse", "解析 bot 菜单事件失败: "+err.Error())
			return nil
		}
		openID := ev.Operator.OperatorID.OpenID
		eventKey := ev.EventKey
		if openID == "" {
			openID = ev.Event.Operator.OperatorID.OpenID
		}
		if eventKey == "" {
			eventKey = ev.Event.EventKey
		}
		return c.OnBotMenu(ctx, openID, eventKey, ev.EventID)
	})

	// 注册机器人被加入群事件（im.chat.member.bot.added_v1：被拉进群 → 提示绑定）。
	dp.OnP2ChatMemberBotAddedV1(func(ctx context.Context, event *larkim.P2ChatMemberBotAddedV1) error {
		if c.OnBotAdded == nil || event.Event == nil {
			return nil
		}
		e := event.Event
		chatID := ptrStr(e.ChatId)
		chatName := ptrStr(e.Name)
		operatorOpenID := ""
		if e.OperatorId != nil {
			operatorOpenID = ptrStr(e.OperatorId.OpenId)
		}
		return c.OnBotAdded(ctx, chatID, operatorOpenID, chatName)
	})

	// 注册机器人被移出群事件（im.chat.member.bot.deleted_v1：被踢 → 解绑）。
	dp.OnP2ChatMemberBotDeletedV1(func(ctx context.Context, event *larkim.P2ChatMemberBotDeletedV1) error {
		if c.OnBotDeleted == nil || event.Event == nil {
			return nil
		}
		e := event.Event
		chatID := ptrStr(e.ChatId)
		operatorOpenID := ""
		if e.OperatorId != nil {
			operatorOpenID = ptrStr(e.OperatorId.OpenId)
		}
		return c.OnBotDeleted(ctx, chatID, operatorOpenID)
	})

	// 注册表情移除事件（im.message.reaction.deleted_v1）：飞书会顺带推送，本桥不处理，
	// 仅静默消费避免 dispatcher 打印 "not found handler" 噪声。
	dp.OnP2MessageReactionDeletedV1(func(ctx context.Context, event *larkim.P2MessageReactionDeletedV1) error {
		core.Info(ctx, "feishu", "reaction-deleted", "已忽略 reaction 移除事件（无需处理）")
		return nil
	})

	// 注册云文档评论 @bot 事件（drive.notice.comment_add_v1：评论里 @机器人 → 跑 agent 回帖）。
	dp.OnP2NoticeCommentAddV1(func(ctx context.Context, event *larkdrive.P2NoticeCommentAddV1) error {
		if c.OnComment == nil || event.Event == nil {
			return nil
		}
		e := event.Event
		fileToken, fileType, commentID, replyID := "", "", "", ""
		noticeType := ""
		isMentioned := false
		if e.NoticeMeta != nil {
			fileToken = ptrStr(e.NoticeMeta.FileToken)
			fileType = ptrStr(e.NoticeMeta.FileType)
			noticeType = ptrStr(e.NoticeMeta.NoticeType)
		}
		commentID = ptrStr(e.CommentId)
		replyID = ptrStr(e.ReplyId)
		if e.IsMentioned != nil {
			isMentioned = *e.IsMentioned
		}
		return c.OnComment(ctx, fileToken, fileType, commentID, replyID, isMentioned, noticeType)
	})

	c.wsCli = ws.NewClient(c.AppID, c.AppSecret,
		ws.WithEventHandler(dp),
	)
	// 长连接状态回调：维护真实连接态（供诊断卡展示），并在首次建立成功时
	// fire-and-forget 触发启动收尾（事件生效播报等）。断线/重连不会重复触发
	// OnConnected（与 TS announceEventsWhenLive 行为一致）。
	c.wsCli.SetOnReady(func() {
		c.setConnState("connected")
		if c.OnConnected != nil {
			go c.OnConnected(ctx)
		}
	})
	c.wsCli.SetOnReconnecting(func() { c.setConnState("reconnecting") })
	c.wsCli.SetOnReconnected(func() { c.setConnState("connected") })
	c.wsCli.SetOnDisconnected(func() { c.setConnState("disconnected") })
	if err := c.wsCli.Start(ctx); err != nil {
		c.setConnState("disconnected")
		return fmt.Errorf("feishu ws start: %w", err)
	}
	return nil
}

// Shutdown 关闭长连接（幂等）。
func (c *Channel) Shutdown() {
	if c.wsCli != nil {
		c.wsCli.Close()
		c.wsCli = nil
	}
}

// Reconnect 断开并重连长连接（best-effort；失败向上返回，由调用方决定降级）。
func (c *Channel) Reconnect(ctx context.Context) error {
	c.Shutdown()
	return c.Connect(ctx)
}

// WsClient 返回底层 ws client。
func (c *Channel) WsClient() *ws.Client { return c.wsCli }

// ── 云文档评论 OpenAPI ─────────────────────────────────────────────

// FileCommentData 拉取到的评论内容（供 bot 层构造 prompt + 回帖）。
type FileCommentData struct {
	FileToken     string
	FileType      string
	IsWhole       bool
	Quote         string
	Question      string // 问题文本（@bot 的那条）
	TargetReplyID string // 事件里的 reply_id（若有），回帖时挂回该回复
}

// GetFileComment 拉取评论/回复内容，定位 @bot 的问题文本。
// commentID=评论 ID；targetReplyID=事件里的 reply_id（可能为空）。
func (c *Channel) GetFileComment(ctx context.Context, fileToken, fileType, commentID, targetReplyID string) (*FileCommentData, error) {
	resp, err := c.LarkClient().Drive.V1.FileComment.Get(ctx,
		larkdrive.NewGetFileCommentReqBuilder().
			FileToken(fileToken).
			FileType(fileType).
			CommentId(commentID).
			UserIdType("open_id").
			Build())
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, fmt.Errorf("get file comment failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("get file comment: empty data")
	}
	out := &FileCommentData{
		FileToken:     fileToken,
		FileType:      fileType,
		TargetReplyID: targetReplyID,
	}
	if resp.Data.IsWhole != nil {
		out.IsWhole = *resp.Data.IsWhole
	}
	out.Quote = ptrStr(resp.Data.Quote)
	// 优先取事件指向的那条回复；否则取该评论下的第一条回复作为问题。
	if targetReplyID != "" && resp.Data.ReplyList != nil {
		for _, r := range resp.Data.ReplyList.Replies {
			if r != nil && ptrStr(r.ReplyId) == targetReplyID && r.Content != nil {
				out.Question = replyElementsToText(r.Content.Elements)
				return out, nil
			}
		}
	}
	if resp.Data.ReplyList != nil && len(resp.Data.ReplyList.Replies) > 0 {
		first := resp.Data.ReplyList.Replies[0]
		if first != nil && first.Content != nil {
			out.Question = replyElementsToText(first.Content.Elements)
		}
	}
	return out, nil
}

// CreateFileCommentReply 在指定评论下回帖（接 @bot 的问题回复）。
func (c *Channel) CreateFileCommentReply(ctx context.Context, fileToken, fileType, commentID, text string) error {
	reply := larkdrive.NewFileCommentReplyBuilder().
		Content(larkdrive.NewReplyContentBuilder().
			Elements([]*larkdrive.ReplyElement{
				larkdrive.NewReplyElementBuilder().
					Type("text_run").
					TextRun(larkdrive.NewTextRunBuilder().Text(text).Build()).
					Build(),
			}).Build()).
		Build()
	resp, err := c.LarkClient().Drive.V1.FileComment.Create(ctx,
		larkdrive.NewCreateFileCommentReqBuilder().
			FileToken(fileToken).
			FileType(fileType).
			UserIdType("open_id").
			FileComment(larkdrive.NewFileCommentBuilder().
				CommentId(commentID). // 填 comment_id = 视为回复该评论
				ReplyList(larkdrive.NewReplyListBuilder().
					Replies([]*larkdrive.FileCommentReply{reply}).
					Build()).
				Build()).
			Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("create file comment reply failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// replyElementsToText 把评论回复元素（与 bot.CommentReplyElement 同构）转纯文本。
func replyElementsToText(elements []*larkdrive.ReplyElement) string {
	var sb []byte
	for _, el := range elements {
		if el == nil {
			continue
		}
		if el.TextRun != nil && el.TextRun.Text != nil {
			sb = append(sb, []byte(*el.TextRun.Text)...)
		}
	}
	return string(sb)
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
