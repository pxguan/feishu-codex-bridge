package clibridge

// protocol.go —— hook 响应协议（对齐 TS cli-bridge/protocol.ts）。
// 把内部决策（CliHookResponse）转成 agent hook 期望的 stdout JSON：
//   - Claude Code / Codex 的 PermissionRequest：{hookSpecificOutput:{hookEventName,decision}}
//   - Codex PreToolUse deny：permissionDecision
//   - 其它（post_tool_use / task_complete / fallback）：{}
// 中性 JSON 字符串（无多余换行由调用方统一加）。

import "encoding/json"

// BuildHookStdout 导出包装：把内部决策转成 agent hook 期望的 stdout JSON（hook 命令用）。
func BuildHookStdout(msg CliHookMessage, response CliHookResponse) string {
	return buildHookStdout(msg, response)
}

func buildHookStdout(msg CliHookMessage, response CliHookResponse) string {
	if response.Stdout != "" {
		return response.Stdout
	}
	if msg.Type == MsgTypePostToolUse {
		return "{}"
	}
	if response.Decision == DecisionFallbackLocal {
		return ""
	}
	if msg.Type == MsgTypeTaskComplete {
		return "{}"
	}

	decision := DecisionDeny
	if response.Decision == DecisionAllow {
		decision = DecisionAllow
	}

	if msg.Source == AgentCodex {
		if msg.HookEventName == "PermissionRequest" {
			decisionObj := map[string]any{"behavior": string(decision)}
			if decision == DecisionDeny {
				reason := response.Reason
				if reason == "" {
					reason = "Denied by feishu-codex-bridge."
				}
				decisionObj["message"] = reason
			}
			return mustJSON(map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName": "PermissionRequest",
					"decision":      decisionObj,
				},
			})
		}
		if msg.HookEventName == "PreToolUse" && decision == DecisionDeny {
			return mustJSON(map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName":           "PreToolUse",
					"permissionDecision":      "deny",
					"permissionDecisionReason": orDefault(response.Reason, "Denied by feishu-codex-bridge."),
				},
			})
		}
		return "{}"
	}

	// Claude Code PermissionRequest。
	decisionObj := map[string]any{"behavior": string(decision)}
	if decision == DecisionAllow && response.UpdatedInput != nil {
		decisionObj["updatedInput"] = response.UpdatedInput
	}
	if decision == DecisionDeny {
		reason := response.Reason
		if reason == "" {
			reason = "Denied by feishu-codex-bridge."
		}
		decisionObj["message"] = reason
		if response.Interrupt {
			decisionObj["interrupt"] = true
		}
	}
	return mustJSON(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PermissionRequest",
			"decision":      decisionObj,
		},
	})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
