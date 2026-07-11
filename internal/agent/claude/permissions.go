package claude

// permissions.go —— 权限档（qa / write / full）→ claude CLI 参数（港口 TS permission.ts）。
//
// 安全核心（与 TS 同源，如实记录安全差）：
//   - full：--dangerously-skip-permissions（历史 danger-full-access：跳过沙箱 + 权限，全机 + 联网）。
//   - qa/write：始终 --permission-mode bypassPermissions（绝不卡人机确认），靠 Claude 自身的 OS 沙箱
//     （macOS Seatbelt / Linux bubblewrap）作为硬边界把 Bash 读写锁进 cwd；按档去工具：
//       · qa    → 去写工具（Write/Edit/NotebookEdit）→ 只读；
//       · 离线  → 去网工具（WebFetch/WebSearch）。
//   沙箱是 CLI 默认行为（只要不用 dangerously-skip-permissions 即生效），无需额外 flag。
//
// 已知差（与 TS 一致）：qa 档读未硬锁 cwd（Read/Bash 读仍可越 cwd），属已记录缺口；
// full 以外的档切勿对不可信外部群当作硬隔离信任。

import (
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// permissionFlags 把一个权限档翻译成 claude CLI 参数片段。
func permissionFlags(mode agent.PermissionMode, network bool) []string {
	tier := mode
	if tier == "" {
		tier = agent.PermissionFull
	}
	args := []string{"--permission-mode", "bypassPermissions"}
	if tier == agent.PermissionFull {
		// 历史 danger-full-access：跳过沙箱 + 权限。
		return append(args, "--dangerously-skip-permissions")
	}
	var disallowed []string
	if tier == agent.PermissionQA {
		disallowed = append(disallowed, "Write", "Edit", "NotebookEdit")
	}
	if !network {
		disallowed = append(disallowed, "WebFetch", "WebSearch")
	}
	if len(disallowed) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(disallowed, ","))
	}
	return args
}
