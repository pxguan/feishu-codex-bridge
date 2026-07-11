package card

// event_live.go —— 「事件已生效」播报卡（对齐 TS onboarding.announceEventsWhenLive）。
// 纯渲染：诊断结果以原始字段传入，不引入 utils 依赖（避免包循环）。

// EventLiveCardOpts 播报卡入参。
type EventLiveCardOpts struct {
	// State: ok | missing | unpublished | unchecked（对齐 utils.EventDiagnosisState）。
	State string
	// Version 最新已上架版本号（ok/missing 时有）。
	Version string
	// Events 已订阅事件列表（ok 时展示）。
	Events []string
	// MissingRequired 缺失的必需事件（missing 时非空）。
	MissingRequired []string
	// MissingOptional 缺失的可选事件（仅单列提示，不影响状态）。
	MissingOptional []string
	// Polled=true 表示由后台轮询确认（之前是 missing/unpublished）。
	Polled bool
	// GuidanceURL 事件配置页深链（非 ok 时给出，便于用户补事件并发布版本）。
	GuidanceURL string
}

// BuildEventLiveCard 事件订阅状态播报卡（DM 发给 owner/admin）。
func BuildEventLiveCard(opts EventLiveCardOpts) CardObject {
	switch opts.State {
	case "ok":
		return buildEventLiveOK(opts)
	default:
		return buildEventLiveWarn(opts)
	}
}

func buildEventLiveOK(opts EventLiveCardOpts) CardObject {
	ver := opts.Version
	if ver == "" {
		ver = "?"
	}
	title := "🟢 事件已生效"
	if opts.Polled {
		title = "🟢 事件已生效（已确认）"
	}
	elements := []CardElement{
		Md("机器人已连接飞书，**事件订阅已生效**。现在去项目群里 **@我** 即可开工。"),
		Hr(),
		Md("**已订阅事件**（版本 v" + ver + "）："),
	}
	subscribed := opts.Events
	if len(subscribed) == 0 {
		subscribed = []string{"im.message.receive_v1"}
	}
	for _, e := range subscribed {
		elements = append(elements, Md("· `"+e+"`"))
	}
	if len(opts.MissingOptional) > 0 {
		elements = append(elements, Hr(), Note("以下可选事件未订阅，对应功能（机器人菜单 / 文档评论回复 / 加入存量群绑定 / 表情回复）已静默关闭："+joinEvents(opts.MissingOptional)))
	}
	return Card(elements, CardOpts{Header: &CardHeader{Title: title, Template: HeaderGreen}})
}

func buildEventLiveWarn(opts EventLiveCardOpts) CardObject {
	title := "⚠️ 事件未生效"
	body := "机器人已连上飞书，但**事件订阅尚未生效**——群里 @我 不会有反应。请按下面步骤处理："
	if opts.State == "unpublished" {
		body = "机器人已连上飞书，但**从未发布过版本**，事件订阅尚未生效——群里 @我 不会有反应。请先发布一个版本："
	} else if opts.State == "missing" {
		ver := opts.Version
		if ver == "" {
			ver = "?"
		}
		body = "机器人已连上飞书，但已发布版本 **v" + ver + "** 缺必需事件，@我 不会有反应。请补上缺失事件并重新发布版本："
	}
	elements := []CardElement{
		Md(body),
		Hr(),
		Md("1. 打开「事件与回调 → 事件配置」，订阅方式选 **长连接**；"),
		Md("2. 添加事件：`im.message.receive_v1`（必需）"),
	}
	if len(opts.MissingRequired) > 0 {
		elements = append(elements, Md("   当前缺失："+joinEvents(opts.MissingRequired)))
	}
	elements = append(elements,
		Md("3. 到「应用发布」创建一个版本并**发布**；"),
		Md("4. 配置生效后我会在这里自动播报「事件已生效」（最多等待约 6 分钟）。"),
	)
	if opts.GuidanceURL != "" {
		elements = append(elements, Hr(), LinkButton("🔧 打开事件配置页", opts.GuidanceURL, ButtonPrimary, "small"))
	}
	return Card(elements, CardOpts{Header: &CardHeader{Title: title, Template: HeaderOrange}})
}

func joinEvents(events []string) string {
	out := ""
	for i, e := range events {
		if i > 0 {
			out += "、"
		}
		out += "`" + e + "`"
	}
	return out
}
