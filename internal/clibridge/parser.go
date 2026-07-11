package clibridge

// parser.go —— hook payload 解析（对齐 TS cli-bridge/parser.ts）。
// 把 agent hook 推来的原始 JSON（stdin）归一为 CliHookMessage；
// ExtractAskUserQuestion 把 AskUserQuestion tool_input 解析为 1-4 个校验过的问题。

import (
	"encoding/json"
	"strconv"
	"strings"
)

func normalizeEventName(eventName string) string {
	switch eventName {
	case "PermissionRequest", "permission_request", "permission.asked", "permission_requested":
		return "PermissionRequest"
	case "PreToolUse", "pre_tool_use":
		return "PreToolUse"
	case "PostToolUse", "post_tool_use":
		return "PostToolUse"
	case "Stop", "stop", "SubagentStop", "subagent_stop", "session.idle", "session_idle":
		return "TaskComplete"
	case "StopFailure", "stop_failure", "session.error", "session_error":
		return "TaskCompleteFailure"
	}
	return eventName
}

func stringifySummaryValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case bool, float64, int:
		return toString(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			if s := stringifySummaryValue(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		for _, key := range []string{
			"last_assistant_message", "lastAssistantMessage", "assistant_message",
			"assistantMessage", "assistant", "final", "completion", "answer",
			"response", "output", "result", "content", "text", "message", "summary", "error",
		} {
			if s := stringifySummaryValue(v[key]); s != "" {
				return s
			}
		}
	}
	return ""
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		// 整数不加点。
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

// ParseHookPayload 解析 hook 原始 JSON 为 CliHookMessage。
// env 携 bridgeOwned 标记（FEISHU_CODEX_BRIDGE=1）；传 nil 时视为无。
func ParseHookPayload(source CliBridgeAgent, rawPayload string, env map[string]string) CliHookMessage {
	var data map[string]any
	if raw := strings.TrimSpace(rawPayload); raw != "" {
		if err := json.Unmarshal([]byte(raw), &data); err != nil || data == nil {
			data = map[string]any{}
		}
	}
	if data == nil {
		data = map[string]any{}
	}

	hookEventName := stringOr(data["hook_event_name"], data["event_type"])
	if hookEventName == "" {
		hookEventName = "PermissionRequest"
	}
	normalized := normalizeEventName(hookEventName)

	typeByEvent := map[string]CliHookMessageType{
		"PermissionRequest": MsgTypePermissionRequest,
		"PostToolUse":       MsgTypePostToolUse,
		"TaskComplete":      MsgTypeTaskComplete,
		"TaskCompleteFailure": MsgTypeTaskComplete,
	}
	msgType := typeByEvent[normalized]
	if msgType == "" {
		msgType = MsgTypePreToolUse
	}

	toolInput := data["tool_input"]
	if toolInput == nil {
		toolInput = data["toolInput"]
	}
	if toolInput == nil {
		toolInput = data["metadata"]
	}
	if toolInput == nil {
		toolInput = data["properties"]
	}
	toolInputMap, _ := toolInput.(map[string]any)
	if toolInputMap == nil {
		toolInputMap = map[string]any{}
	}

	bridgeOwned := env["FEISHU_CODEX_BRIDGE"] == "1"

	return CliHookMessage{
		Type:       msgType,
		Source:     source,
		SessionID:  stringOr(data["session_id"], data["sessionId"]),
		Cwd:        stringOr(data["cwd"]),
		ToolName:   stringOr(data["tool_name"], data["toolName"], data["permission"]),
		ToolInput:  toolInputMap,
		HookEventName: hookEventName,
		StopHookActive: data["stop_hook_active"] == true || data["stopHookActive"] == true,
		PermissionMode: stringOr(data["permission_mode"], data["permissionMode"]),
		PermissionSuggestions: asAnySlice(data["permission_suggestions"], data["permissionSuggestions"]),
		TaskStatus: taskStatusOf(normalized),
		Summary:    func() string {
			if msgType == MsgTypeTaskComplete {
				return stringifySummaryValue(data)
			}
			return ""
		}(),
		BridgeOwned: bridgeOwned,
		RawPayloadBytes: len(rawPayload),
	}
}

// ExtractAskUserQuestion 把 AskUserQuestion/ask_user_question tool_input 解析为
// 1-4 个校验过的问题。任一问题非法 → 返回 nil（调用方回退本地终端）。
func ExtractAskUserQuestion(toolInput map[string]any) *struct {
	Questions []CliQuestionItem `json:"questions"`
} {
	rawQuestions, ok := toolInput["questions"].([]any)
	if !ok || len(rawQuestions) < 1 || len(rawQuestions) > 4 {
		return nil
	}
	questions := make([]CliQuestionItem, 0, len(rawQuestions))
	for _, raw := range rawQuestions {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		question := strings.TrimSpace(stringOr(obj["question"]))
		if question == "" {
			return nil
		}
		rawOptions, ok := obj["options"].([]any)
		if !ok || len(rawOptions) < 2 {
			return nil
		}
		opts := make([]CliQuestionOption, 0, len(rawOptions))
		for _, o := range rawOptions {
			om, ok := o.(map[string]any)
			if !ok {
				return nil
			}
			label := strings.TrimSpace(stringOr(om["label"]))
			if label == "" {
				return nil
			}
			opts = append(opts, CliQuestionOption{
				Label:       label,
				Description: stringOrT(om["description"]),
				Preview:     stringOrT(om["preview"]),
			})
		}
		if len(opts) != len(rawOptions) {
			return nil
		}
		questions = append(questions, CliQuestionItem{
			Question:    question,
			Header:      stringOrT(obj["header"]),
			MultiSelect: obj["multiSelect"] == true,
			Options:     opts,
		})
	}
	if len(questions) != len(rawQuestions) {
		return nil
	}
	return &struct {
		Questions []CliQuestionItem `json:"questions"`
	}{Questions: questions}
}

// ── 小工具 ───────────────────────────────────────────────────

func stringOr(vals ...any) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func stringOrT(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asAnySlice(vals ...any) []any {
	for _, v := range vals {
		if s, ok := v.([]any); ok {
			return s
		}
	}
	return nil
}

func taskStatusOf(normalized string) string {
	switch normalized {
	case "TaskCompleteFailure":
		return "failed"
	case "TaskComplete":
		return "completed"
	}
	return ""
}
