package codex

// backend.go —— codex 后端编排层的【纯函数核心】（对齐 TS codex-appserver/backend 的纯函数部分）。
//
// 含：SandboxParams（权限档 → codex thread/start|resume 参数 + 平台 fail-closed 守卫）、
//     WithAutoCompact（关闭自动压缩设 1e9）。
//
// 编排层（CodexThread 的 runStreamed/runGoal goroutine 状态机、CodexAppServerBackend 的
// doctor/listModels/listThreads/readHistory/startThread/resumeThread、mapTurn/mapModel、
// HTTP 拉数层 fetchUsageBundle/401 兜底）依赖 AppServerClient 运行时 + client-pool + 真实
// codex，是下一个接力点（见方案 §11）。

import (
	"errors"
	"runtime"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// APPROVAL_POLICY codex 审批策略：永不请求审批（沙箱外的不让做）。
const APPROVAL_POLICY = "never"

// autoCompactOffLimit codex auto-compact 关闭哨兵：config.model_auto_compact_token_limit
// 设一个任何 session 都达不到的值（1e9，JS 安全整数内、远超任何上下文窗口）即关闭。
const autoCompactOffLimit = 1_000_000_000

// SandboxParams 构造 codex thread/start|resume 的权限档参数。
//   - full（或空）→ danger-full-access（历史默认）；
//   - qa/write → 自定义 feishu permissions profile，靠 OS 沙箱把读写锁进 cwd：
//     macOS Seatbelt / Windows restricted token；
//   - fail-closed：qa/write 在非 darwin/windows 平台抛错（Linux/WSL 沙箱只挡写不挡读
//     且不拒绝 → 静默放行隐私泄露，绝不降级）。platform 默认 runtime.GOOS，测试可注入。
func SandboxParams(mode agent.PermissionMode, network bool, platform string) (map[string]any, error) {
	if platform == "" {
		platform = runtime.GOOS
	}
	if mode == "" || mode == agent.PermissionFull {
		return map[string]any{"sandbox": "danger-full-access"}, nil
	}
	if platform != "darwin" && platform != "windows" {
		return nil, errors.New("「项目内只读 / 项目内读写」靠操作系统沙箱把读写锁进项目文件夹，目前只有 macOS 与原生 Windows 能强制执行。当前平台（Linux / WSL 只挡写、不限制读取，无法保证不泄露隐私）已拒绝启动（绝不降级为完全访问）。请改用「完全访问」、把 Codex 跑进容器/隔离环境，或在 macOS / Windows 上运行。")
	}
	write := "read"
	if mode == agent.PermissionWrite {
		write = "write"
	}
	return map[string]any{
		"config": map[string]any{
			"default_permissions": "feishu",
			"permissions": map[string]any{
				"feishu": map[string]any{
					"filesystem": map[string]any{
						":minimal":         "read",
						":workspace_roots": map[string]any{".": write},
					},
					"network": map[string]any{"enabled": network},
				},
			},
		},
	}, nil
}

// WithAutoCompact 在 autoCompact 显式 false 时合并关闭自动压缩；nil/true 留 codex 默认。
func WithAutoCompact(params map[string]any, autoCompact *bool) map[string]any {
	if autoCompact == nil || *autoCompact {
		return params
	}
	cfg, _ := params["config"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	} else {
		// 复制 config 避免改原 map。
		dup := make(map[string]any, len(cfg)+1)
		for k, v := range cfg {
			dup[k] = v
		}
		cfg = dup
	}
	cfg["model_auto_compact_token_limit"] = autoCompactOffLimit
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	out["config"] = cfg
	return out
}
