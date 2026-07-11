package cli

// hook.go —— 隐藏的 `hook` 子命令（☕ 咖啡一下 客户端）。
// agent（Claude Code / Codex）的 hook 进程调用：从 stdin 读 hook payload JSON，
// 经 Unix socket 发给本机 run 进程的 cli-bridge server，等人在飞书上决策后把
// 决策 stdout 回给 agent。桥未运行时静默回退本地审批（不输出决策）。

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/clibridge"
	"github.com/spf13/cobra"
)

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "☕ 咖啡一下：agent hook 客户端（被 Claude/Codex hook 调用）",
		Hidden: true,
		RunE:   runHook,
	}
	cmd.Flags().String("agent", "", "调用方 agent：claude | codex（必填）")
	cmd.Flags().String("bot", "", "关联机器人 appId（仅用于排查；socket 按用户全局共享）")
	return cmd
}

func runHook(cmd *cobra.Command, _ []string) error {
	agentStr, _ := cmd.Flags().GetString("agent")
	if agentStr != clibridge.AgentClaude && agentStr != clibridge.AgentCodex {
		return fmt.Errorf("hook --agent 必须是 claude 或 codex")
	}

	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("读 stdin 失败：%w", err)
	}

	env := map[string]string{}
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 {
			env[e[:i]] = e[i+1:]
		}
	}

	msg := clibridge.ParseHookPayload(agentStr, string(stdin), env)
	resp, err := clibridge.SendCliHookMessage(clibridge.DefaultSocketPath(), msg)
	if err != nil {
		// 桥未运行：静默回退本地（不输出决策，让 agent 自己提示）。
		fmt.Fprintf(cmd.ErrOrStderr(), "⚠️ cli-bridge 未连接（%v），回退本地审批\n", err)
		return nil
	}

	stdout := clibridge.BuildHookStdout(msg, resp)
	if stdout != "" {
		fmt.Fprint(cmd.OutOrStdout(), stdout)
	}
	return nil
}
