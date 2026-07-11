package cli

// root.go —— CLI 命令树根（对齐 TS cli/index.ts 的 commander 注册）。
// cobra root + version 子命令 + 各子命令的注册点。

import (
	"fmt"
	"io"

	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/spf13/cobra"
)

// NewRootCmd 构造根命令（含全部子命令注册）。
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "feishu-codex-bridge",
		Short: "把飞书/Lark 桥接到本机 Codex（项目=群, 话题=会话）",
		Long: "把飞书/Lark 桥接到本机 Codex（项目=群, 话题=会话）。\n" +
			"群里 @机器人就能让它在指定项目目录里跑 Codex / Claude，结果以流式 Markdown 卡片实时回到群里。",
		Version: core.Version(),
		// 让 cobra 的 --version 用我们的 version（默认已通过 Version 字段实现）。
	}

	// ── 进程 / 守护 ──
	root.AddCommand(newRunCmd())
	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newRestartCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newLogsCmd())

	// ── 更新 / Web ──
	root.AddCommand(newUpdateCmd())
	root.AddCommand(newWebCmd())

	// ── 飞书机器人管理 ──
	root.AddCommand(newBotCmd())

	// ── 群枚举（TS/Go 共用的 projects.json）──
	root.AddCommand(newChatsCmd())

	// ── 杂项 ──
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newSecretsCmd())
	root.AddCommand(newSendCmd())
	root.AddCommand(newHookCmd())
	root.AddCommand(newHooksCmd())

	return root
}

// Execute 解析 args 并执行；输出写到 out/stderr。
func Execute(args []string, out, errOut io.Writer) int {
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
