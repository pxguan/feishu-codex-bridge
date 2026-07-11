package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// eventdiagnosis.go —— 事件订阅三态诊断（对齐 TS utils/event-diagnosis）。
//
// 飞书对「添加事件/回调/发布版本」没有写入 API，但提供只读「应用版本列表」：
// 每个版本带 events（已订阅事件）与 status（1=已上架）。据此把「@bot 没反应」从
// 「等用户发现」变成精确三态 + unchecked 优雅降级（绝不阻塞启动）。

// REQUIRED_EVENTS 核心链路必需事件。
var REQUIRED_EVENTS = []string{"im.message.receive_v1"}

// OPTIONAL_EVENTS 可选功能依赖的事件（缺失只关对应功能，不影响诊断状态）。
var OPTIONAL_EVENTS = []string{
	"application.bot.menu_v6",
	"drive.notice.comment_add_v1",
	"im.chat.member.bot.added_v1",
	"im.chat.member.bot.deleted_v1",
	"im.message.reaction.created_v1",
}

// EventDiagnosisState 四态。
type EventDiagnosisState string

const (
	EventDiagnosisUnchecked   EventDiagnosisState = "unchecked"
	EventDiagnosisUnpublished EventDiagnosisState = "unpublished"
	EventDiagnosisMissing     EventDiagnosisState = "missing"
	EventDiagnosisOK          EventDiagnosisState = "ok"
)

// EventDiagnosis 诊断结果。
type EventDiagnosis struct {
	State           EventDiagnosisState
	Reason          string
	Version         string
	Events          []string
	MissingRequired []string
	MissingOptional []string
}

// DiagnoseEventSubscription 用默认 base 诊断。
func DiagnoseEventSubscription(ctx context.Context, appID, appSecret string, tenant config.TenantBrand, hc *http.Client) EventDiagnosis {
	return diagnoseAt(ctx, hc, tenantBase(tenant), appID, appSecret)
}

func diagnoseAt(ctx context.Context, hc *http.Client, base, appID, appSecret string) EventDiagnosis {
	if hc == nil {
		hc = http.DefaultClient
	}
	// 1. token
	token, reason := fetchTenantToken(ctx, hc, base, appID, appSecret)
	if reason != "" {
		return EventDiagnosis{State: EventDiagnosisUnchecked, Reason: reason}
	}
	// 2. 版本列表
	code, body, err := getJSON(ctx, hc, fmt.Sprintf("%s/open-apis/application/v6/applications/%s/app_versions?lang=zh_cn&page_size=50&order=1",
		base, url.PathEscape(appID)), "Bearer "+token)
	if err != nil {
		return EventDiagnosis{State: EventDiagnosisUnchecked, Reason: "网络错误：" + err.Error()}
	}
	var vr versionListResp
	parseErr := json.Unmarshal(body, &vr)
	if code != http.StatusOK {
		hint := ""
		if (code == http.StatusBadRequest || code == http.StatusForbidden) && parseErr != nil {
			hint = "——可能缺 application:application.app_version:readonly 权限"
		}
		if parseErr != nil {
			return EventDiagnosis{State: EventDiagnosisUnchecked, Reason: fmt.Sprintf("HTTP %d%s", code, hint)}
		}
	}
	if parseErr != nil {
		return EventDiagnosis{State: EventDiagnosisUnchecked, Reason: "响应不是合法 JSON"}
	}
	if vr.Code != 0 {
		scopeHint := ""
		if vr.Code == 99991672 || scopePermissionRegexp.MatchString(vr.Msg) {
			scopeHint = "——请在「权限管理」授权 application:application.app_version:readonly 后重试"
		}
		return EventDiagnosis{State: EventDiagnosisUnchecked, Reason: fmt.Sprintf("code=%d msg=%s%s", vr.Code, fallback(vr.Msg, "<no msg>"), scopeHint)}
	}
	// 收集所有已上架（status=1）版本，按语义版本号取「最新」作为当前在线版本。
	// 注意：飞书 app_versions 的 order 参数语义在不同端并不一致（文档写 0=升序，
	// 旧注释误以为 0=倒序），且可能同时返回多个 status=1。故这里不依赖接口排序，
	// 自行用语义版本比较选出最新已上架版本——否则会误判「旧版本缺事件」而漏掉
	// 真正在线、已含事件的新版本（表现为「明明已配事件却报缺」）。
	var live *versionItem
	for i := range vr.Data.Items {
		if vr.Data.Items[i].Status != 1 {
			continue
		}
		if live == nil || semverGreater(vr.Data.Items[i].Version, live.Version) {
			live = &vr.Data.Items[i]
		}
	}
	if live == nil {
		return EventDiagnosis{State: EventDiagnosisUnpublished}
	}
	events := live.Events
	has := map[string]bool{}
	for _, e := range events {
		has[e] = true
	}
	// 飞书 app_versions 的 events 字段是「中文展示名」（如 "接收消息"），
	// 与 REQUIRED/OPTIONAL_EVENTS 的事件码（如 im.message.receive_v1）不匹配，
	// 直接比较会误判「明明已配事件却报缺」。故额外收集 event_infos[].event_type
	//（真实事件码）用于判定。
	for _, ei := range live.EventInfos {
		if ei.EventType != "" {
			has[ei.EventType] = true
		}
	}
	missingReq := diffEvents(has, REQUIRED_EVENTS)
	missingOpt := diffEvents(has, OPTIONAL_EVENTS)
	state := EventDiagnosisOK
	if len(missingReq) > 0 {
		state = EventDiagnosisMissing
	}
	return EventDiagnosis{
		State:           state,
		Version:         live.Version,
		Events:          events,
		MissingRequired: missingReq,
		MissingOptional: missingOpt,
	}
}

var scopePermissionRegexp = stringsNewRegexp(`(?i)permission|scope|access`)

// semverGreater 判断版本号 a 是否比 b 更大（如 1.0.10 > 1.0.3）。
// 按 "." 拆段逐段数值比较；非数字段退化为字符串比较；忽略前缀 "v"。
func semverGreater(a, b string) bool {
	pa := splitVersion(a)
	pb := splitVersion(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		sa, sb := "", ""
		if i < len(pa) {
			sa = pa[i]
		}
		if i < len(pb) {
			sb = pb[i]
		}
		na, erra := strconv.Atoi(sa)
		nb, errb := strconv.Atoi(sb)
		if erra == nil && errb == nil {
			if na != nb {
				return na > nb
			}
			continue
		}
		if sa != sb {
			return sa > sb
		}
	}
	return false
}

func splitVersion(v string) []string {
	return strings.Split(strings.TrimPrefix(v, "v"), ".")
}

func diffEvents(has map[string]bool, list []string) []string {
	out := []string{}
	for _, e := range list {
		if !has[e] {
			out = append(out, e)
		}
	}
	return out
}

type versionListResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Items []versionItem `json:"items"`
	} `json:"data"`
}

type versionItem struct {
	Version    string      `json:"version"`
	Status     int         `json:"status"`
	Events     []string    `json:"events"`      // 中文展示名（如 "接收消息"）
	EventInfos []eventInfo `json:"event_infos"` // 真实事件码（如 im.message.receive_v1）
}

type eventInfo struct {
	EventName string `json:"event_name"`
	EventType string `json:"event_type"`
}

// SummarizeEventDiagnosis 一行中文摘要（启动日志 / doctor CLI 用）。
func SummarizeEventDiagnosis(d EventDiagnosis) string {
	switch d.State {
	case EventDiagnosisOK:
		return fmt.Sprintf("✅ 已生效（版本 v%s 已订阅 %s）", fallback(d.Version, "?"), strings.Join(REQUIRED_EVENTS, " / "))
	case EventDiagnosisMissing:
		return fmt.Sprintf("❌ 已发布版本 v%s 缺事件：%s —— @我 不会有反应", fallback(d.Version, "?"), strings.Join(d.MissingRequired, "、"))
	case EventDiagnosisUnpublished:
		return "❌ 从未发布过版本 —— 事件订阅尚未生效，@我 不会有反应"
	default:
		return fmt.Sprintf("⚠️ 未能自动检测（%s）", fallback(d.Reason, "未知原因"))
	}
}

// PollEventSubscription 轮询直到 state=ok 或超时；超时返回 nil。
func PollEventSubscription(ctx context.Context, appID, appSecret string, tenant config.TenantBrand, hc *http.Client, interval, timeout time.Duration) *EventDiagnosis {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		d := DiagnoseEventSubscription(ctx, appID, appSecret, tenant, hc)
		if d.State == EventDiagnosisOK {
			return &d
		}
		if time.Now().Add(interval).After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
