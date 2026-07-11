package cli

// web.go —— Phase 2 本机 Web 控制台命令（替换原 stub）。

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/web"
	"github.com/spf13/cobra"
)

func newWebCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web",
		Short: "本机 Web 控制台（仅 127.0.0.1 + token）",
		RunE:  webRun,
	}
	cmd.Flags().String("addr", "127.0.0.1:18789", "监听地址（默认仅本机回环）")
	cmd.Flags().Bool("print-token", false, "启动时在终端打印访问 token")
	return cmd
}

func webRun(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	addr, _ := cmd.Flags().GetString("addr")
	srv := web.New()
	srv.ListenAddr = addr

	tok, err := srv.EnsureToken()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "🌐 Web 控制台已启动：http://%s （仅本机可访问）\n", addr)
	if printTok, _ := cmd.Flags().GetBool("print-token"); printTok {
		fmt.Fprintf(out, "   访问 token：%s\n", tok)
	} else {
		fmt.Fprintf(out, "   token 已写入 %s（加 ?token= 或 Authorization 头访问）\n",
			config.WebTokenFile())
	}
	fmt.Fprintln(out, "   Ctrl+C 退出。")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}
