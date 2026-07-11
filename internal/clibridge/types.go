// Package clibridge —— 「☕ 咖啡一下」反向桥（对齐 TS src/cli-bridge）。
// 把本机 Claude Code / Codex 的审批 / 提问 / 收尾事件，通过 Unix socket
// （agent hook → 本 daemon）路由到飞书 owner 私聊，人在手机上即可放行 / 作答 / 续聊。
//
// 本文件定义跨文件共享的类型（types.ts → types.go）。
package clibridge

// CliBridgeAgent = claude | codex。
type CliBridgeAgent = string

const (
	AgentClaude = "claude"
	AgentCodex  = "codex"
)

// CliHookMessageType hook 消息语义类型。
type CliHookMessageType = string

const (
	MsgTypePermissionRequest CliHookMessageType = "permission_request"
	MsgTypePreToolUse        CliHookMessageType = "pre_tool_use"
	MsgTypePostToolUse       CliHookMessageType = "post_tool_use"
	MsgTypeTaskComplete       CliHookMessageType = "task_complete"
)

// CliDecision hook 响应决策。
type CliDecision string

const (
	DecisionAllow          CliDecision = "allow"
	DecisionDeny           CliDecision = "deny"
	DecisionFallbackLocal  CliDecision = "fallback_local"
)

// CliHookMessage agent hook 推来的消息（IPC 线路 JSON）。
type CliHookMessage struct {
	Type       CliHookMessageType `json:"type"`
	Source     CliBridgeAgent     `json:"source"`
	SessionID  string             `json:"sessionId"`
	Cwd        string             `json:"cwd"`
	ToolName   string             `json:"toolName,omitempty"`
	ToolInput  map[string]any     `json:"toolInput"`
	HookEventName string          `json:"hookEventName,omitempty"`
	StopHookActive bool           `json:"stopHookActive,omitempty"`
	PermissionMode string         `json:"permissionMode,omitempty"`
	PermissionSuggestions []any   `json:"permissionSuggestions,omitempty"`
	TaskStatus string             `json:"taskStatus,omitempty"` // completed|failed
	Summary    string             `json:"summary,omitempty"`
	BridgeOwned bool              `json:"bridgeOwned"`
	RawPayloadBytes int           `json:"rawPayloadBytes"`
}

// CliHookResponse 回给 agent hook 的决策（IPC 线路 JSON）。
type CliHookResponse struct {
	Decision    CliDecision `json:"decision"`
	Stdout      string      `json:"stdout,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	UpdatedInput map[string]any `json:"updatedInput,omitempty"`
	Interrupt   bool        `json:"interrupt,omitempty"`
}

// CliQuestionItem AskUserQuestion 的单个问题（单/多选）。
type CliQuestionItem struct {
	Question    string   `json:"question"`
	Header      string   `json:"header,omitempty"`
	MultiSelect bool     `json:"multiSelect"`
	Options     []CliQuestionOption `json:"options"`
}

// CliQuestionOption 下拉选项。
type CliQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// CliHookInstallStatus hooks 安装状态。
type CliHookInstallStatus string

const (
	HookInstalled     CliHookInstallStatus = "installed"
	HookNotInstalled  CliHookInstallStatus = "not_installed"
	HookNeedsRepair   CliHookInstallStatus = "needs_repair"
	HookConflictAgent2Lark CliHookInstallStatus = "conflict_agent2lark"
)

// CliHookStatus 单个 agent 的 hook 安装状态（inspect 用）。
type CliHookStatus struct {
	Agent   CliBridgeAgent     `json:"agent"`
	Status  CliHookInstallStatus `json:"status"`
	Details []string           `json:"details"`
}
