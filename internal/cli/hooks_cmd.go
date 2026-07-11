package cli

// hooks_cmd.go —— `hooks` 子命令（☕ 咖啡一下 安装器）。
// install：把 `hook` 子命令装进 Claude Code / Codex 的配置；
// inspect：探测安装状态（含 codex [features] hooks gate 与 agent2lark 冲突）；
// uninstall：移除。

import (
	"fmt"

	"github.com/modelzen/feishu-codex-bridge/internal/clibridge"
	"github.com/spf13/cobra"
)

func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hooks",
		Short:  "☕ 咖啡一下：管理 Claude/Codex 的 bridge hook",
		Hidden: true,
	}
	cmd.AddCommand(newHooksInstallCmd())
	cmd.AddCommand(newHooksInspectCmd())
	cmd.AddCommand(newHooksUninstallCmd())
	return cmd
}

func newHooksInstallCmd() *cobra.Command {
	var claude, codex bool
	var botAppID string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "安装 bridge hook 到 Claude Code / Codex",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !claude && !codex {
				claude, codex = true, true
			}
			opts := clibridge.InstallCliBridgeHooksOptions{
				Command: clibridge.ResolveBridgeHookCommand(botAppID),
			}
			opts.Agents.Claude = claude
			opts.Agents.Codex = codex
			if err := clibridge.InstallCliBridgeHooks(opts); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if claude {
				fmt.Fprintln(out, "✅ 已安装 Claude Code hook（~/.claude/settings.json）")
			}
			if codex {
				fmt.Fprintln(out, "✅ 已安装 Codex hook（~/.codex/hooks.json + config.toml [features] hooks=true）")
			}
			fmt.Fprintln(out, "ℹ️ 启动 `run` 后，agent 的审批/提问会路由到飞书 owner 私聊。")
			return nil
		},
	}
	cmd.Flags().BoolVar(&claude, "claude", false, "安装到 Claude Code")
	cmd.Flags().BoolVar(&codex, "codex", false, "安装到 Codex")
	cmd.Flags().StringVar(&botAppID, "bot", "", "关联机器人 appId（hook 命中同 bot 的 daemon socket）")
	return cmd
}

func newHooksInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "探测 Claude Code / Codex 的 bridge hook 安装状态",
		RunE: func(cmd *cobra.Command, _ []string) error {
			claude, codex := clibridge.InspectCliBridgeHooks(clibridge.InspectCliBridgeHooksOptions{})
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Claude Code: %s\n", claude.Status)
			if len(claude.Details) > 0 {
				for _, d := range claude.Details {
					fmt.Fprintf(out, "  · %s\n", d)
				}
			}
			fmt.Fprintf(out, "Codex:      %s\n", codex.Status)
			if len(codex.Details) > 0 {
				for _, d := range codex.Details {
					fmt.Fprintf(out, "  · %s\n", d)
				}
			}
			return nil
		},
	}
	return cmd
}

func newHooksUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "移除 Claude Code / Codex 的 bridge hook",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := clibridge.UninstallCliBridgeHooks(clibridge.InspectCliBridgeHooksOptions{}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "✅ 已移除 bridge hook（Claude Code / Codex）")
			return nil
		},
	}
	return cmd
}
