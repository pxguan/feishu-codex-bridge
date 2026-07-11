package card

// dm_doctor.go —— DM 控制台诊断卡（对齐 TS card/dm-cards 的 doctor 部分）。
// DoctorInfo + connLabel + scopeStatusText + codexDiagnosePrompt + BuildDoctorCard。
// 纯数据构造，依赖 element builder + config scopes。

import (
	"fmt"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// DoctorInfo 诊断卡数据。
type DoctorInfo struct {
	CodexOK        bool
	CodexVer       string
	Conn           string
	BotOpenID      string
	BridgeVer      string
	Node           string
	Platform       string
	LogStdout      string
	LogStderr      string
	ConfigFile     string
	MissingScopes  []string // nil=无法检查
	JoinMissing    []string // nil=无法检查
	ScopeGrantURL  string
	JoinGrantURL   string
	EventDiag      *EventDiagInfo
	EventConfigURL string
}

// EventDiagInfo 事件订阅诊断（简化版，对齐 utils.EventDiagnosis）。
type EventDiagInfo struct {
	State           string // ok|missing|unpublished|unchecked
	Version         string
	MissingRequired []string
	MissingOptional []string
	Reason          string
	Events          []string
}

// ConnLabel 长连接状态友好标签。
func ConnLabel(state string) string {
	switch state {
	case "connected":
		return "✅ 已连接"
	case "connecting":
		return "⏳ 连接中"
	case "reconnecting":
		return "↻ 重连中"
	case "disconnected":
		return "❌ 已断开"
	}
	return state
}

// ScopeStatusText 飞书权限状态单行文本。
func ScopeStatusText(missing []string) string {
	if missing == nil {
		return "未能自动检查（凭证失效或网络问题）"
	}
	if len(missing) == 0 {
		return "必需权限齐全"
	}
	return fmt.Sprintf("缺失 %d 项：%s", len(missing), strings.Join(missing, " "))
}

// CodexDiagnosePrompt 可复制的 codex diagnose 纯文本 prompt。
func CodexDiagnosePrompt(i DoctorInfo) string {
	codexVer := i.CodexVer
	if codexVer == "" {
		codexVer = "未找到（PATH / CODEX_BIN 里都没有 codex）"
	}
	lines := []string{
		"我在用 feishu-codex-bridge（飞书 ↔ 本地 Codex 桥接）遇到问题，请帮我定位原因并给出修复步骤。",
		"",
		"【环境】",
		fmt.Sprintf("- bridge 版本：v%s", i.BridgeVer),
		fmt.Sprintf("- codex 版本：%s", codexVer),
		fmt.Sprintf("- 平台：%s", i.Platform),
		fmt.Sprintf("- 项目仓库：%s", RepoURL),
		"",
		"【运行快照】",
		fmt.Sprintf("- codex 可用：%s", boolText(i.CodexOK, "是", "否")),
		fmt.Sprintf("- 飞书长连接：%s", i.Conn),
		fmt.Sprintf("- 飞书权限：%s", ScopeStatusText(i.MissingScopes)),
		"",
		"【请你做的事】",
		"1. 读取并分析日志，找出最近的报错或异常堆栈：",
		fmt.Sprintf("   - %s", i.LogStdout),
		fmt.Sprintf("   - %s", i.LogStderr),
		"2. 检查 codex 是否正确登录（codex login）",
		"3. 检查飞书权限是否齐全（上方缺失的 scope 需在开发者后台开通）",
	}
	return strings.Join(lines, "\n")
}

func boolText(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

// BuildDoctorCard DM 控制台诊断卡。
func BuildDoctorCard(i DoctorInfo) CardObject {
	noForward := false
	var elements []CardElement

	// codex 状态。
	if i.CodexOK {
		elements = append(elements, Md(fmt.Sprintf("- Codex：✅ 可用%s", suffix(i.CodexVer, "（%s）"))))
	} else {
		elements = append(elements, Md("- Codex：❌ 不可用（检查 CODEX_BIN / PATH）"))
	}

	// 长连接。
	elements = append(elements, Md(fmt.Sprintf("- 飞书长连接：%s", ConnLabel(i.Conn))))

	// bot open_id。
	if i.BotOpenID != "" {
		elements = append(elements, Md(fmt.Sprintf("- 机器人 open_id：`%s`", i.BotOpenID)))
	} else {
		elements = append(elements, Md("- 机器人 open_id：⚠️ 未能获取（凭据失效或网络不通）"))
	}

	// 权限三态。
	if i.MissingScopes == nil {
		elements = append(elements, Md("- 飞书权限：⚠️ 无法自动检查（凭证失效或网络不通）"))
		if i.ScopeGrantURL != "" {
			elements = append(elements, Actions([]CardElement{LinkButton("🔑 去权限页核对", i.ScopeGrantURL, ButtonDefault, "")}, ""))
		}
	} else if len(i.MissingScopes) == 0 {
		elements = append(elements, Md("- 飞书权限：✅ 必需权限已全部开通"))
	} else {
		elements = append(elements, Md(fmt.Sprintf("- 飞书权限：❌ 缺 %d 项 —— 开通前相关功能不可用", len(i.MissingScopes))))
		var scopeLines []string
		for _, s := range i.MissingScopes {
			scopeLines = append(scopeLines, "· "+config.LabelScope(s))
		}
		elements = append(elements, Note("待开通：\n"+strings.Join(scopeLines, "\n")))
		if i.ScopeGrantURL != "" {
			elements = append(elements, Actions([]CardElement{LinkButton("🔑 一键去开通这些权限", i.ScopeGrantURL, ButtonPrimary, "")}, ""))
		}
	}

	// 事件订阅。
	if i.EventDiag != nil {
		switch i.EventDiag.State {
		case "ok":
			elements = append(elements, Md(fmt.Sprintf("- 事件订阅：✅ 版本 v%s 已订阅 `im.message.receive_v1`", fallback(i.EventDiag.Version, "?"))))
		case "missing":
			elements = append(elements, Md(fmt.Sprintf("- 事件订阅：❌ 已发布版本 v%s 缺 `im.message.receive_v1` —— @我 不会有反应", fallback(i.EventDiag.Version, "?"))))
		case "unchecked":
			elements = append(elements, Md(fmt.Sprintf("- 事件订阅：⚠️ 无法自动检查（%s）", fallback(i.EventDiag.Reason, "未知原因"))))
		case "unpublished", "":
			elements = append(elements, Md("- 事件订阅：❌ 从未发布过版本 —— 事件订阅未生效"))
		}
	}

	// 版本信息。
	elements = append(elements, Note(fmt.Sprintf("bridge v%s　·　%s　·　%s", i.BridgeVer, i.Node, i.Platform)))

	// 加入存量群（可选）诊断。
	elements = append(elements, Hr())
	elements = append(elements, Md("**加入存量群（可选）**"))
	if i.JoinMissing == nil {
		elements = append(elements, Md("- 权限：⚠️ 未能自动检查（凭据失效或网络不通）"))
		if i.JoinGrantURL != "" {
			elements = append(elements, Actions([]CardElement{LinkButton("🔑 去开通", i.JoinGrantURL, ButtonDefault, "")}, ""))
		}
	} else if len(i.JoinMissing) == 0 {
		elements = append(elements, Md("- 权限：✅ 已开通（`im:chat:readonly` / `im:chat.members:write_only`）"))
	} else {
		elements = append(elements, Md(fmt.Sprintf("- 权限：❌ 缺 %d 项 —— 开通后才能把我加进已有群（绑定 / 退群）", len(i.JoinMissing))))
		var lines []string
		for _, s := range i.JoinMissing {
			lines = append(lines, "· "+config.LabelScope(s))
		}
		elements = append(elements, Note("待开通：\n"+strings.Join(lines, "\n")))
		if i.JoinGrantURL != "" {
			elements = append(elements, Actions([]CardElement{LinkButton("🔑 一键开通这两项权限", i.JoinGrantURL, ButtonPrimary, "")}, ""))
		}
	}

	elements = append(elements, Hr())

	// 日志路径。
	elements = append(elements, Md("**日志路径**"))
	elements = append(elements, Note(fmt.Sprintf("后台守护输出：`%s`", i.LogStdout)))
	elements = append(elements, Note(fmt.Sprintf("后台守护错误：`%s`", i.LogStderr)))
	elements = append(elements, Note("前台 `run` 模式：日志在启动它的终端窗口里"))

	elements = append(elements, Hr())

	// codex diagnose prompt。
	elements = append(elements, Md("**让 Codex 帮你深度诊断** — 复制下面整段，到任意项目群里 **@我** 粘贴发送："))
	elements = append(elements, Note(CodexDiagnosePrompt(i)))
	elements = append(elements, Actions([]CardElement{
		LinkButton("📦 项目仓库", RepoURL, ButtonDefault, ""),
		LinkButton("🐞 提 Issue", RepoURL+"/issues", ButtonDefault, ""),
	}, ""))

	elements = append(elements, BackToMenu())

	return Card(elements, CardOpts{
		Header:  &CardHeader{Title: "🩺 诊断", Template: HeaderBlue},
		Forward: &noForward,
	})
}

func suffix(s, tmpl string) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf(tmpl, s)
}
