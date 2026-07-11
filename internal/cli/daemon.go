package cli

// daemon.go —— Phase 2 daemon 生命周期命令（start/stop/restart/status/logs）。
// 复用 internal/daemon.Manager：start 脱离终端重 exec 自身 `run`，
// stop 杀进程树，status/logs 读 pid 文件与日志。

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/daemon"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "后台启动 daemon（脱离终端，日志写入 service.log）",
		RunE:  daemonStart,
	}
}

func daemonStart(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	m := daemon.New()
	if err := m.Start(); err != nil {
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			info, _ := m.Status()
			fmt.Fprintf(out, "已在运行（pid=%d, uptime=%s）\n", info.PID, info.Uptime)
			return nil
		}
		return err
	}

	// 短暂探活：run 若因配置错误（无活跃机器人/凭据缺失）立即退出，
	// 不应谎报「已启动」。探活失败则提示并展示最近日志。
	time.Sleep(800 * time.Millisecond)
	if info, _ := m.Status(); !info.Running {
		fmt.Fprintln(out, "⚠️ 后台进程启动后立即退出，可能配置有误。最近日志：")
		if lines, lerr := m.Logs(25); lerr == nil {
			for _, l := range lines {
				fmt.Fprintln(out, "  "+strings.TrimSpace(l))
			}
		}
		fmt.Fprintf(out, "   完整日志：%s\n", config.ServiceLog())
		return nil
	}

	fmt.Fprintf(out, "✅ 后台 daemon 已启动\n   日志：%s\n", config.ServiceLog())
	fmt.Fprintf(out, "   状态：feishu-codex-bridge status\n   日志：feishu-codex-bridge logs -f\n")
	return nil
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "停止后台 daemon",
		RunE:  daemonStop,
	}
}

func daemonStop(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	m := daemon.New()
	info, _ := m.Status()
	if err := m.Stop(); err != nil {
		return err
	}
	if !info.Running {
		fmt.Fprintln(out, "（没有运行中的 daemon，已清理残留 pid 文件）")
		return nil
	}
	fmt.Fprintf(out, "✅ 已停止后台 daemon（pid=%d）\n", info.PID)
	return nil
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "重启后台 daemon",
		RunE:  daemonRestart,
	}
}

func daemonRestart(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	m := daemon.New()
	if err := m.Restart(); err != nil {
		return err
	}
	fmt.Fprintln(out, "✅ 已重启后台 daemon")
	return nil
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看后台 daemon 状态",
		RunE:  daemonStatus,
	}
}

func daemonStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	m := daemon.New()
	info, _ := m.Status()
	if !info.Running {
		fmt.Fprintln(out, "● 未运行")
		return nil
	}
	fmt.Fprintf(out, "● 运行中\n  pid:    %d\n  uptime: %s\n  args:   %v\n  log:    %s\n",
		info.PID, info.Uptime, info.Args, config.ServiceLog())
	return nil
}

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "查看后台 daemon 日志",
		RunE:  daemonLogs,
	}
	cmd.Flags().IntP("lines", "n", 200, "静态查看时显示最后 N 行")
	cmd.Flags().BoolP("follow", "f", false, "持续跟踪日志输出（Ctrl+C 退出）")
	return cmd
}

func daemonLogs(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	m := daemon.New()
	follow, _ := cmd.Flags().GetBool("follow")
	if follow {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		ch, err := m.FollowLogs(ctx)
		if err != nil {
			return err
		}
		for line := range ch {
			fmt.Fprintln(out, line)
		}
		return nil
	}
	n, _ := cmd.Flags().GetInt("lines")
	lines, err := m.Logs(n)
	if err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return nil
}
