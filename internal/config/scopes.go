package config

import "net/url"

// scopes.go —— 飞书权限 scope 清单 + 授权/事件页 URL（对齐 TS config/scopes）。
//
// 飞书没有「API 声明 scope」的能力，扫码创建流程不接受权限参数；唯一授权途径是
// 开发者后台「申请权限」页。故 buildScopeGrantUrl 预选全部 scope，让用户一页点齐。

// REQUIRED_SCOPES —— daemon-install 强制 gate（缺则反复提示，阻塞启动）。
var REQUIRED_SCOPES = []string{
	"im:message.group_at_msg:readonly",
	"im:message.group_msg",
	"im:message.p2p_msg:readonly",
	"im:message:send_as_bot",
	"im:message.pins:write_only",
	"im:message.reactions:write_only",
	"im:resource",
	"im:chat:create",
	"im:chat:update",
	"im:chat.managers:write_only",
	"im:chat.announcement:read",
	"im:chat.announcement:write_only",
	"im:chat.top_notice:write_only",
	"im:chat.tabs:write_only",
	"cardkit:card:write",
}

// COMMENT_SCOPES —— 文档评论回复（可选增强，不阻塞启动）。
var COMMENT_SCOPES = []string{
	"docs:document.comment:read",
	"docs:document.comment:create",
	"wiki:wiki:readonly",
}

// JOIN_GROUP_SCOPES —— 加入存量群（可选）。
var JOIN_GROUP_SCOPES = []string{
	"im:chat:readonly",
	"im:chat.members:write_only",
}

// CONTACT_SCOPES —— 解析 open_id→姓名（可选展示）。
var CONTACT_SCOPES = []string{"contact:user.base:readonly"}

// APP_VERSION_SCOPES —— 事件订阅三态诊断（可选）。
var APP_VERSION_SCOPES = []string{"application:application.app_version:readonly"}

// DISCOVERY_SCOPES —— 群内可发现性三件套（群菜单 + reaction 事件，可选）。
var DISCOVERY_SCOPES = []string{
	"im:chat.menu_tree:write_only",
	"im:message.reactions:read",
}

// GRANT_SCOPES —— 一键授权 URL 预选的全部 scope（required + 全部可选）。
var GRANT_SCOPES = concatScopes(
	REQUIRED_SCOPES,
	COMMENT_SCOPES,
	JOIN_GROUP_SCOPES,
	CONTACT_SCOPES,
	APP_VERSION_SCOPES,
	DISCOVERY_SCOPES,
)

func concatScopes(groups ...[]string) []string {
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	out := make([]string, 0, n)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// SCOPE_LABELS —— scope token → 中文说明（doctor 卡展示用）。
var SCOPE_LABELS = map[string]string{
	"im:message.group_at_msg:readonly":             "接收群里 @机器人 的消息",
	"im:message.group_msg":                         "接收群内所有消息（免@）",
	"im:message.p2p_msg:readonly":                  "接收私聊消息（管理台）",
	"im:message:send_as_bot":                       "发送消息 / 卡片",
	"im:message.pins:write_only":                   "置顶消息到群 Pin",
	"im:message.reactions:write_only":              "消息表情回复（运行状态）",
	"im:resource":                                  "图片 / 文件上传与下载",
	"im:chat:create":                               "创建项目群",
	"im:chat:update":                               "转移群主（解绑时）",
	"im:chat.managers:write_only":                  "设置群管理员",
	"im:chat.announcement:read":                    "读取群公告",
	"im:chat.announcement:write_only":              "编辑群公告",
	"im:chat.top_notice:write_only":                "置顶群公告横幅",
	"im:chat.tabs:write_only":                      "添加群标签页",
	"im:chat:readonly":                             "读取群信息（群名/群主，加入存量群用）",
	"im:chat.members:write_only":                   "群成员增减（绑定的存量群解绑时机器人退群）",
	"cardkit:card:write":                           "交互按钮卡片",
	"docs:document.comment:read":                   "读取文档评论",
	"docs:document.comment:create":                 "发表文档评论回复",
	"wiki:wiki:readonly":                           "读取知识库节点",
	"contact:user.base:readonly":                   "读取成员姓名（管理员 / 白名单展示）",
	"application:application.app_version:readonly": "读取应用版本信息（自动诊断事件订阅）",
	"im:chat.menu_tree:write_only":                 "添加群菜单（群内常驻命令入口）",
	"im:message.reactions:read":                    "接收表情回复事件（终态卡 👍 续轮 / 运行卡 OK 终止）",
}

// LabelScope 已知 scope 返回「<中文>（<token>）」，否则返回原 token。
func LabelScope(scope string) string {
	if label, ok := SCOPE_LABELS[scope]; ok {
		return label + "（" + scope + "）"
	}
	return scope
}

func tenantHost(tenant TenantBrand) string {
	if tenant == TenantLark {
		return "open.larksuite.com"
	}
	return "open.feishu.cn"
}

// BuildScopeGrantUrl 构造开发者后台「一键预选全部 scope」授权页 URL。
// scopes 默认 GRANT_SCOPES；逗号 join 后 URL 编码，防止 &/# 注入。
func BuildScopeGrantUrl(appID string, tenant TenantBrand, scopes ...string) string {
	if len(scopes) == 0 {
		scopes = GRANT_SCOPES
	}
	host := tenantHost(tenant)
	q := url.QueryEscape(joinComma(scopes))
	return "https://" + host + "/app/" + url.QueryEscape(appID) + "/auth?q=" + q
}

// BuildEventConfigUrl 事件与回调页（无 ?q= 预选，需手填）。
func BuildEventConfigUrl(appID string, tenant TenantBrand) string {
	return "https://" + tenantHost(tenant) + "/app/" + url.QueryEscape(appID) + "/event"
}

func joinComma(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += "," + s
	}
	return out
}
