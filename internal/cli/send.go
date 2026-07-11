package cli

// send.go —— `send` 子命令：给飞书群/会话发一条消息。
//
// 两种用法：
//  1. 默认：把给定文本直接发到目标 chat（用本机凭据，等价于「机器人主动发消息」）。
//  2. --codex：把文本交给 codex 后端跑一轮，把 codex 的回复发回群里。
//     这是验证「codex 群」端到端链路最省事的办法——跳过人工 @，直接看 codex 产出。
//
// 注意：本命令依赖本机凭据（~/.feishu-codex-bridge 的 keystore）+ 飞书网络，
// 必须在已注册机器人的机器上运行；沙箱/CI 里跑不了（无凭据/无网络）。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/feishu"
	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send --chat <chat_id> [--bot <name>] <文本>",
		Short: "给飞书群/会话发一条消息（可选经 codex 后端生成回复发回）",
		Long: "给飞书群/会话发一条消息。\n" +
			"默认把文本直接发到目标 chat；加 --codex 则把文本交给 codex 后端跑一轮，\n" +
			"把 codex 的回复发回群里（端到端自测 codex 链路，无需人工 @）。",
		RunE: runSend,
	}
	cmd.Flags().String("chat", "", "目标 chat_id（飞书群/会话 ID，必填）")
	cmd.Flags().String("bot", "", "指定机器人（名字或 appId），默认活跃的第一个")
	cmd.Flags().String("backend", "", "不直接发原文，而是交给指定后端（codex / claude-agent）生成回复发回群里")
	cmd.Flags().String("cwd", "", "（--backend 模式）后端工作目录，默认当前目录")
	cmd.Flags().Duration("timeout", 5*time.Minute, "（--backend 模式）后端最长运行时间")
	return cmd
}

func runSend(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	if len(args) == 0 {
		return fmt.Errorf("缺少要发送的文本")
	}
	text := strings.Join(args, " ")

	chatID, _ := cmd.Flags().GetString("chat")
	if chatID == "" {
		return fmt.Errorf("必须用 --chat <chat_id> 指定目标群/会话")
	}
	backendID, _ := cmd.Flags().GetString("backend")

	// 解析 bot + secret（与 run 同款加载路径）。
	reg, err := config.LoadBots()
	if err != nil {
		return fmt.Errorf("读 registry 失败：%w", err)
	}
	botFlag, _ := cmd.Flags().GetString("bot")
	var entry config.BotEntry
	if botFlag != "" {
		e, ok := config.FindBot(reg, botFlag)
		if !ok {
			return fmt.Errorf("找不到机器人「%s」", botFlag)
		}
		entry = e
	} else {
		active := config.ActiveBots(reg)
		if len(active) == 0 {
			return fmt.Errorf("没有活跃机器人。运行 `feishu-codex-bridge bot init` 注册一个")
		}
		entry = active[0]
	}
	botCfg, err := config.LoadConfig(config.BotConfigFile(entry.AppID))
	if err != nil {
		return fmt.Errorf("读 config 失败：%w", err)
	}
	secret, err := config.ResolveAppSecret(botCfg)
	if err != nil {
		return fmt.Errorf("解析 app secret 失败：%w", err)
	}

	ch := feishu.NewChannel(entry.AppID, secret, string(entry.Tenant))

	body := text
	if backendID != "" {
		fmt.Fprintf(out, "🧠 调用后端 %s 生成回复（chat=%s）…\n", backendID, chatID)
		reply, err := runBackendForSend(cmd, backendID, text)
		if err != nil {
			return err
		}
		if strings.TrimSpace(reply) == "" {
			return fmt.Errorf("后端 %s 没有产生文本回复", backendID)
		}
		body = reply
	}

	msgID, err := ch.SendText(context.Background(), chatID, body)
	if err != nil {
		return fmt.Errorf("发送失败：%w", err)
	}
	fmt.Fprintf(out, "✅ 已发送（message_id=%s，chat=%s）\n", msgID, chatID)
	return nil
}

// runBackendForSend 跑一轮指定后端，收集文本回复。
func runBackendForSend(cmd *cobra.Command, backendID, text string) (string, error) {
	timeout, _ := cmd.Flags().GetDuration("timeout")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	be, err := agent.CreateBackend(backendID)
	if err != nil {
		return "", fmt.Errorf("创建后端失败：%w", err)
	}
	if !be.IsAvailable(ctx) {
		return "", fmt.Errorf("codex 后端不可用（确认 codex 已登录：codex login）")
	}
	cwd, _ := cmd.Flags().GetString("cwd")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	thread, err := be.StartThread(ctx, agent.StartThreadOptions{
		Cwd:  cwd,
		Mode: agent.PermissionQA, // 只读、限 cwd，自测最安全
	})
	if err != nil {
		return "", fmt.Errorf("启动 codex 会话失败：%w", err)
	}
	defer thread.Close(ctx)

	run := thread.RunStreamed(ctx, agent.AgentInput{Text: text}, nil)
	var sb strings.Builder
	for ev := range run.Events {
		switch ev.Type {
		case agent.EvText:
			sb.WriteString(ev.Text)
		case agent.EvTextDelta:
			sb.WriteString(ev.Delta)
		case agent.EvError:
			return sb.String(), fmt.Errorf("codex 报错：%s", ev.Message)
		}
	}
	return sb.String(), nil
}
