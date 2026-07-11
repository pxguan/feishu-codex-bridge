package card

// dispatcher.go —— 卡片回调路由（对齐 TS card/dispatcher）。
// 按 callback value.a 路由到 handler。纯路由逻辑（不依赖飞书 SDK 调用），
// 飞书 SDK 的 cardAction 事件绑定在 bot 层（传 CardActionEvent 给 Handle）。

import (
	"context"

	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

// CardActionEvent 飞书 card.action.trigger 事件（通用结构，飞书 SDK 实际类型在 bot 层适配）。
type CardActionEvent struct {
	Action struct {
		Tag    string      `json:"tag"`
		Value  ActionValue `json:"value"`
		Option string      `json:"option"`
	} `json:"action"`
	ChatID    string `json:"chatId"`
	MessageID string `json:"messageId"`
	Operator  struct {
		OpenID string `json:"openId"`
	} `json:"operator"`
	Raw struct {
		Action struct {
			FormValue map[string]any `json:"form_value"`
		} `json:"action"`
	} `json:"raw"`
}

// CardActionContext handler 上下文。
type CardActionContext struct {
	Ctx       context.Context
	Evt       *CardActionEvent
	ActionID  string
	Option    string         // select_static 选中值
	Value     ActionValue    // 元素 value 载荷（按钮的 m/t/b 等）
	FormValue map[string]any // 表单提交值（submit 按钮，需 includeRawEvent）
}

// CardActionHandler 卡片回调处理函数。
type CardActionHandler func(ctx CardActionContext) error

// CardDispatcher 按 action id 路由卡片回调。
type CardDispatcher struct {
	handlers map[string]CardActionHandler
}

// NewCardDispatcher 构造。
func NewCardDispatcher() *CardDispatcher {
	return &CardDispatcher{handlers: map[string]CardActionHandler{}}
}

// On 注册 handler（最后注册者胜）。返回 d 便于链式。
func (d *CardDispatcher) On(actionID string, h CardActionHandler) *CardDispatcher {
	d.handlers[actionID] = h
	return d
}

// Handle 路由一个 cardAction 事件。无 key/无 handler 仅 log 不抛（卡片回调绝不能因路由失败崩溃）。
func (d *CardDispatcher) Handle(ctx context.Context, evt *CardActionEvent) {
	actionID, _ := evt.Action.Value["a"].(string)
	if actionID == "" {
		core.Info(ctx, "card", "action-unkeyed", "无 action id 的卡片回调（tag="+evt.Action.Tag+"）")
		return
	}
	handler, ok := d.handlers[actionID]
	if !ok {
		core.Info(ctx, "card", "action-nohandler", "无 handler: "+actionID)
		return
	}
	ccx := CardActionContext{
		Ctx: core.WithTrace(ctx, "", evt.ChatID, evt.MessageID),
		Evt: evt, ActionID: actionID,
		Option: evt.Action.Option, Value: evt.Action.Value, FormValue: evt.Raw.Action.FormValue,
	}
	by := evt.Operator.OpenID
	if len(by) > 6 {
		by = by[len(by)-6:]
	}
	core.Info(ctx, "card", "action", "actionId="+actionID+" by="+by)
	if err := handler(ccx); err != nil {
		core.Fail(ctx, "card", "action", err)
	}
}
