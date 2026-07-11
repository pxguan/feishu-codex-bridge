package cli

// chats.go —— 枚举已建/已绑定的飞书群与会话（来自 projects.json / sessions.json，TS 与 Go 共用同一文件）。
//
// 背景：TS 版在 ~/.feishu-codex-bridge/bots/<appId>/{projects,sessions}.json 记录了所有建过的群
// （chatId ↔ name/cwd/backend）与会话（chatId ↔ backend session）。Go 版读的是同一文件，
// 所以历史群不会丢——只是之前没有「列出来」的命令，才显得「找不到」。本命令就是那个枚举入口。

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/bot"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
	"github.com/spf13/cobra"
)

func newChatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "枚举已建/已绑定的飞书群与会话（projects.json / sessions.json）",
	}
	cmd.AddCommand(newChatsListCmd())
	cmd.AddCommand(newChatsSessionsCmd())
	return cmd
}

// resolveBot 按 --bot 解析目标 bot（默认活跃的第一个）。
func resolveBot(cmd *cobra.Command) (config.BotEntry, error) {
	reg, err := config.LoadBots()
	if err != nil {
		return config.BotEntry{}, fmt.Errorf("读 registry 失败：%w", err)
	}
	botFlag, _ := cmd.Flags().GetString("bot")
	if botFlag != "" {
		e, ok := config.FindBot(reg, botFlag)
		if !ok {
			return config.BotEntry{}, fmt.Errorf("找不到机器人「%s」", botFlag)
		}
		return e, nil
	}
	active := config.ActiveBots(reg)
	if len(active) == 0 {
		return config.BotEntry{}, fmt.Errorf("没有活跃机器人。运行 `feishu-codex-bridge bot init` 注册一个")
	}
	return active[0], nil
}

func newChatsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出所有群（chatId / 名 / 后端 / 工作目录）",
		RunE:  chatsList,
	}
	cmd.Flags().String("bot", "", "指定机器人（名字或 appId），默认活跃的第一个")
	cmd.Flags().String("backend", "", "按后端过滤：codex / claude / claude-agent（空=全部）")
	return cmd
}

// matchesBackend 后端过滤：codex 匹配空或含 codex；claude 匹配 claude-agent。
func matchesBackend(be, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	if filter == "claude" {
		filter = "claude-agent"
	}
	if filter == "codex" {
		return be == "" || strings.Contains(be, "codex")
	}
	return be == filter
}

func chatsList(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	entry, err := resolveBot(cmd)
	if err != nil {
		return err
	}
	backendFilter, _ := cmd.Flags().GetString("backend")

	store := project.NewStore(config.BotProjectsFile(entry.AppID))
	projects, err := store.List()
	if err != nil {
		return fmt.Errorf("读 projects.json 失败：%w", err)
	}
	if len(projects) == 0 {
		fmt.Fprintf(out, "（bot %s 还没有绑定任何群。在飞书群里 @ 一下 bot 即可自动建群）\n", entry.AppID)
		return nil
	}

	var shown, claude, codex int
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tBACKEND\tKIND\tCHAT_ID\tCWD")
	for _, p := range projects {
		if !matchesBackend(p.Backend, backendFilter) {
			continue
		}
		shown++
		be := p.Backend
		if be == "" {
			be = "codex(默认)"
		}
		if p.Backend == "claude-agent" {
			claude++
		} else {
			codex++
		}
		kind := p.Kind
		if kind == "" {
			kind = "multi"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, be, kind, p.ChatID, p.Cwd)
	}
	tw.Flush()

	fmt.Fprintf(out, "\n显示 %d / 共 %d 个群（codex %d · claude-agent %d）\n", shown, len(projects), codex, claude)
	fmt.Fprintf(out, "（数据来源：%s）\n", filepath.Join(config.BotDir(entry.AppID), "projects.json"))
	return nil
}

func newChatsSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "列出所有持久化会话（sessions.json：chatId ↔ backend session）",
		RunE:  chatsSessions,
	}
	cmd.Flags().String("bot", "", "指定机器人（名字或 appId），默认活跃的第一个")
	cmd.Flags().String("backend", "", "按后端过滤：codex / claude / claude-agent（空=全部）")
	return cmd
}

func chatsSessions(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	entry, err := resolveBot(cmd)
	if err != nil {
		return err
	}
	backendFilter, _ := cmd.Flags().GetString("backend")

	store := bot.NewSessionStore(config.BotSessionsFile(entry.AppID))
	sessions, err := store.List()
	if err != nil {
		return fmt.Errorf("读 sessions.json 失败：%w", err)
	}
	if len(sessions) == 0 {
		fmt.Fprintf(out, "（bot %s 还没有持久化会话。在群里 @ 一下 bot 即会创建）\n", entry.AppID)
		return nil
	}

	var shown, claude, codex int
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHAT_ID\tBACKEND\tMODEL\tLASTSEEN\tSUMMARY")
	for _, s := range sessions {
		if !matchesBackend(s.Backend, backendFilter) {
			continue
		}
		shown++
		be := s.Backend
		if be == "" {
			be = "codex(默认)"
		}
		if s.Backend == "claude-agent" {
			claude++
		} else {
			codex++
		}
		last := "-"
		if s.LastSeenAt > 0 {
			last = time.UnixMilli(s.LastSeenAt).Format("2006-01-02 15:04")
		}
		summary := s.Summary
		if len(summary) > 40 {
			summary = summary[:40] + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.ChatID, be, s.Model, last, summary)
	}
	tw.Flush()

	fmt.Fprintf(out, "\n显示 %d / 共 %d 个会话（codex %d · claude-agent %d）\n", shown, len(sessions), codex, claude)
	fmt.Fprintf(out, "（数据来源：%s）\n", filepath.Join(config.BotDir(entry.AppID), "sessions.json"))
	return nil
}
