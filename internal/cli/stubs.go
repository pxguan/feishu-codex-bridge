package cli

// stubs.go —— Phase 1 子命令实现。
// bot list / doctor 是 Phase 1 完整子命令（依赖 config + agent 全就绪）。
// run/bot init/bot use/bot rm/secrets 需飞书 SDK channel + orchestrator。
// daemon 生命周期（start/stop/restart/status/logs）在 cli/daemon.go，
// web 控制台在 cli/web.go，update 在 cli/update.go。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/bot"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/feishu"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
	"github.com/modelzen/feishu-codex-bridge/internal/service"
	"github.com/modelzen/feishu-codex-bridge/internal/web"
	"github.com/spf13/cobra"
)

// Phase 1 子命令。

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "前台启动活跃机器人（含本机 Web 控制台；Ctrl+C 优雅退出）",
		RunE:  runRun,
	}
	cmd.Flags().String("bot", "", "只启动指定的一个机器人（名字或 appId）")
	cmd.Flags().Bool("web", true, "同时启动本机 Web 控制台（仅 127.0.0.1）")
	cmd.Flags().String("web-addr", "127.0.0.1:18789", "Web 控制台监听地址")
	return cmd
}

func runRun(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 0. 本机 Web 控制台（与飞书桥接同进程；ctx 取消时随 run 一起退出）。
	//    token 只落盘到 WebTokenFile，不打印到 stdout（避免写进 daemon service.log）。
	var webSrv *web.Server
	if webOn, _ := cmd.Flags().GetBool("web"); webOn {
		webAddr, _ := cmd.Flags().GetString("web-addr")
		srv := web.New()
		srv.ListenAddr = webAddr
		if _, err := srv.EnsureToken(); err != nil {
			fmt.Fprintf(out, "⚠️ Web 控制台启动失败（已跳过）：%v\n", err)
		} else {
			go func() { _ = srv.Run(ctx) }()
			fmt.Fprintf(out, "🌐 Web 控制台：http://%s （token 见 %s）\n", webAddr, config.WebTokenFile())
			webSrv = srv
		}
	}

	// 1. 活跃 bot。
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

	// 2. resolve secret。
	botCfg, err := config.LoadConfig(config.BotConfigFile(entry.AppID))
	if err != nil {
		return fmt.Errorf("读 config 失败：%w", err)
	}
	secret, err := config.ResolveAppSecret(botCfg)
	if err != nil {
		return fmt.Errorf("解析 app secret 失败：%w", err)
	}

	// 4. Orchestrator（先建，ch.OnMessage 引用它）。
	projStore := project.NewStore(config.BotProjectsFile(entry.AppID))
	sessStore := bot.NewSessionStore(config.BotSessionsFile(entry.AppID))
	orch := bot.NewOrchestrator(botCfg, projStore, sessStore, config.BotConfigFile(entry.AppID))

	// 3. feishu Channel。
	ch := feishu.NewChannel(entry.AppID, secret, string(entry.Tenant))
	ch.OnMessage = func(mctx context.Context, msgID, chatID, threadID, senderID, senderName, senderType, msgType, content, chatType string) error {
		orch.OnMessage(mctx, bot.NormalizedMessage{
			MessageID: msgID, ChatID: chatID, ThreadID: threadID,
			SenderID: senderID, SenderName: senderName, SenderType: senderType,
			Content: content, RawType: msgType, ChatType: chatType,
		})
		return nil
	}
	ch.OnCardAction = func(actx context.Context, raw []byte) error {
		// 飞书 card.action.trigger 事件中 action / operator 都包在 event 下：
		//   event.action.value["a"] → 路由 key
		//   event.action.form_value → 表单提交值
		//   event.operator.open_id  → 操作者
		// 兼容部分事件把 action 直接放顶层的情况。
		var outer struct {
			Event struct {
				Operator struct {
					OpenID     string `json:"open_id"`
					OperatorID struct {
						OpenID string `json:"open_id"`
					} `json:"operator_id"`
				} `json:"operator"`
				Action struct {
					Tag       string         `json:"tag"`
					Option    string         `json:"option"`
					Value     map[string]any `json:"value"`
					FormValue map[string]any `json:"form_value"`
				} `json:"action"`
				Context struct {
					OpenChatID    string `json:"open_chat_id"`
					OpenMessageID string `json:"open_message_id"`
				} `json:"context"`
				// 部分事件直接把 chat_id / message_id 放 action 同级
				OpenChatID    string `json:"open_chat_id"`
				OpenMessageID string `json:"open_message_id"`
				ChatID        string `json:"chat_id"`
				MessageID     string `json:"message_id"`
			} `json:"event"`
			Action struct {
				Tag       string         `json:"tag"`
				Option    string         `json:"option"`
				Value     map[string]any `json:"value"`
				FormValue map[string]any `json:"form_value"`
			} `json:"action"`
			Context struct {
				OpenChatID    string `json:"open_chat_id"`
				OpenMessageID string `json:"open_message_id"`
			} `json:"context"`
		}
		if json.Unmarshal(raw, &outer) != nil {
			return nil
		}
		act := outer.Event.Action
		if len(act.Value) == 0 && act.Tag == "" && len(act.FormValue) == 0 {
			act = outer.Action // 退回顶层 action
		}
		openID := outer.Event.Operator.OpenID
		if openID == "" {
			openID = outer.Event.Operator.OperatorID.OpenID
		}
		// chat_id / message_id 在 event.context（飞书 card.action.trigger 标准位置）
		chatID := outer.Event.Context.OpenChatID
		msgID := outer.Event.Context.OpenMessageID
		if chatID == "" {
			chatID = outer.Event.OpenChatID
		}
		if chatID == "" {
			chatID = outer.Event.ChatID
		}
		if chatID == "" {
			chatID = outer.Context.OpenChatID
		}
		if msgID == "" {
			msgID = outer.Event.OpenMessageID
		}
		if msgID == "" {
			msgID = outer.Event.MessageID
		}
		cae := card.CardActionEvent{}
		cae.ChatID = chatID
		cae.MessageID = msgID
		cae.Action.Tag = act.Tag
		cae.Action.Option = act.Option
		cae.Action.Value = act.Value
		cae.Operator.OpenID = openID
		cae.Raw.Action.FormValue = act.FormValue
		if aid, _ := cae.Action.Value["a"].(string); aid == "" {
			dump := raw
			if len(dump) > 1500 {
				dump = append(dump[:1500], []byte("…(truncated)")...)
			}
			core.Info(actx, "card", "action-unkeyed-raw", "无法解析 action id，原始 body="+string(dump))
		}
		orch.Dispatcher.Handle(actx, &cae)
		return nil
	}
	orch.Channel = ch
	orch.SendCardFunc = func(sctx context.Context, chatID string, cardJSON []byte) (string, error) {
		return ch.SendCardByEntity(sctx, chatID, string(cardJSON))
	}
	orch.SendDMCardFunc = func(sctx context.Context, openID string, cardJSON []byte) (string, error) {
		return ch.SendCardByOpenID(sctx, openID, string(cardJSON))
	}
	orch.SendDMTextFunc = ch.SendDM
	ch.OnReaction = func(rctx context.Context, messageID, emojiType, operatorType, operatorOpenID string) error {
		return orch.HandleReaction(rctx, messageID, emojiType, operatorType, operatorOpenID)
	}
	ch.OnBotMenu = orch.HandleBotMenu
	ch.OnBotAdded = func(rctx context.Context, chatID, operatorOpenID, chatName string) error {
		return orch.HandleBotAdded(rctx, chatID, operatorOpenID, chatName)
	}
	ch.OnBotDeleted = func(rctx context.Context, chatID, operatorOpenID string) error {
		return orch.HandleBotDeleted(rctx, chatID, operatorOpenID)
	}
	ch.OnComment = func(rctx context.Context, fileToken, fileType, commentID, replyID string, isMentioned bool, noticeType string) error {
		return orch.HandleComment(rctx, fileToken, fileType, commentID, replyID, isMentioned, noticeType)
	}
	// 长连接连上后播报事件订阅状态（对齐 TS announceEventsWhenLive）。
	ch.OnConnected = orch.AnnounceWhenLive

	// 0b. 把写操作依赖注入 Web 控制台（项目列表/设置写、重连、日志流、系统服务安装/卸载）。
	if webSrv != nil {
		webSrv.Deps = &web.Deps{
			Projects: orch.ProjectStore,
			LogFile:  config.ServiceLog(),
		}
		if ch != nil {
			webSrv.Deps.Reconnect = ch.Reconnect
		}
		webSrv.Deps.SvcInstall = func(ctx context.Context) error {
			opts, err := service.DefaultOptions()
			if err != nil {
				return err
			}
			return service.Install(opts)
		}
		webSrv.Deps.SvcUninstall = func(ctx context.Context) error {
			return service.Uninstall()
		}
		webSrv.Deps.SvcStatus = func(ctx context.Context) (service.Status, error) {
			return service.GetStatus()
		}
		webSrv.Deps.SetCompletionReminder = func(mode string, longTaskMinutes int) error {
			return orch.SetCompletionReminder(config.CompletionReminderMode(mode), longTaskMinutes)
		}
	}

	// 4b. cli-bridge（☕ 咖啡一下）反向桥：启用则构造并启动 IPC server + 卡片回调。
	cliSvc, cliEnabled, err := setupCliBridge(ctx, out, orch, ch, botCfg)
	if err != nil {
		return fmt.Errorf("cli-bridge 初始化失败：%w", err)
	}
	if cliEnabled {
		defer cliSvc.Shutdown(ctx)
	}
	fmt.Fprintf(out, "🔗 连接飞书长连接（%s / %s）…\n", entry.Name, entry.AppID)
	if err := ch.Connect(ctx); err != nil {
		return fmt.Errorf("飞书连接失败：%w", err)
	}
	defer ch.Shutdown()
	fmt.Fprintf(out, "✅ 已连接。Ctrl+C 退出。\n\n")

	// 5. 阻塞等信号。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Fprintf(out, "\n👋 收到退出信号，正在关闭…\n")
	return nil
}

func newBotCmd() *cobra.Command {
	bot := &cobra.Command{
		Use:   "bot",
		Short: "飞书机器人管理（多机器人）",
	}
	bot.AddCommand(
		newBotInitCmd(),
		newBotListCmd(),
		func() *cobra.Command {
			c := &cobra.Command{
				Use:   "use [names...]",
				Short: "勾选要同时连接的机器人（无参进入交互多选；或 `bot use --all/--none`）",
				RunE:  botUse,
			}
			c.Flags().Bool("all", false, "激活全部已注册机器人")
			c.Flags().Bool("none", false, "停用全部（run/start 将不启动任何机器人）")
			return c
		}(),
		&cobra.Command{Use: "rm <name>", Short: "移除一个机器人配置", RunE: botRm},
		&cobra.Command{
			Use:   "install",
			Short: "把守护进程注册为系统服务（开机自启；macOS launchd / Linux systemd）",
			RunE:  botInstall,
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "注销系统服务并停止守护进程",
			RunE:  botUninstall,
		},
		&cobra.Command{
			Use:   "service",
			Short: "查看守护进程系统服务安装/运行态",
			RunE:  botService,
		},
	)
	return bot
}

// botInstall 把当前二进制注册为当前用户的常驻系统服务（开机自启）。
func botInstall(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	opts, err := service.DefaultOptions()
	if err != nil {
		return err
	}
	if err := service.Install(opts); err != nil {
		return err
	}
	fmt.Fprintf(out, "✅ 已安装并启动守护进程（%s）\n", opts.BinaryPath)
	fmt.Fprintf(out, "   日志：%s / %s\n", config.ServiceLog(), config.ServiceErrLog())
	return nil
}

// botUninstall 注销系统服务。
func botUninstall(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	if err := service.Uninstall(); err != nil {
		return err
	}
	fmt.Fprintln(out, "✅ 已注销守护进程系统服务")
	return nil
}

// botService 查看守护进程系统服务安装/运行态。
func botService(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	st, err := service.GetStatus()
	if err != nil && !st.Installed && !st.Loaded {
		// 不支持平台也会返回 ErrUnsupported，仍打印状态说明。
		fmt.Fprintf(out, "⚠️ %v\n", err)
	}
	fmt.Fprintf(out, "installed=%v loaded=%v\npath=%s\n%s\n", st.Installed, st.Loaded, st.FilePath, st.Note)
	return nil
}

// newBotListCmd bot list —— 列出已注册的飞书机器人。
func newBotListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出已注册的飞书机器人",
		RunE:  botList,
	}
}

func botList(cmd *cobra.Command, args []string) error {
	reg, err := config.LoadBots()
	if err != nil {
		return fmt.Errorf("读 registry 失败：%w", err)
	}
	if len(reg.Bots) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "（还没有注册的机器人。运行 `feishu-codex-bridge bot init` 注册一个。）")
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tAPP_ID\tTENANT\tACTIVE\tBOT_NAME")
	for _, b := range reg.Bots {
		active := ""
		if b.Active != nil {
			if *b.Active {
				active = "✓"
			} else {
				active = "·"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", b.Name, b.AppID, b.Tenant, active, b.BotName)
	}
	tw.Flush()
	return nil
}

func botRm(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法：feishu-codex-bridge bot rm <name>")
	}
	nameOrID := args[0]
	reg, err := config.LoadBots()
	if err != nil {
		return err
	}
	target, ok := config.FindBot(reg, nameOrID)
	if !ok {
		return fmt.Errorf("找不到机器人「%s」", nameOrID)
	}
	if _, err := config.RemoveBot(target.AppID); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "已移除机器人「%s」（appId=%s）\n", target.Name, target.AppID)
	return nil
}

// botUse 勾选要随 run/start 启动的机器人（多机器人同时连接）。
// 三种用法：
//   - `bot use 名字1 名字2 …`：按 name/appId 激活这些（其余停用）；
//   - `bot use --all` / `bot use --none`：全部激活 / 全部停用；
//   - `bot use`（无参且 TTY）：打印编号列表，读一行选择（如 "1,3" 或 "all"/"none"）。
func botUse(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	reg, err := config.LoadBots()
	if err != nil {
		return fmt.Errorf("读 registry 失败：%w", err)
	}
	if len(reg.Bots) == 0 {
		return fmt.Errorf("还没有注册的机器人。运行 `feishu-codex-bridge bot init` 注册一个")
	}
	all, _ := cmd.Flags().GetBool("all")
	none, _ := cmd.Flags().GetBool("none")

	var chosen []string
	switch {
	case none:
		chosen = nil
	case all:
		for _, b := range reg.Bots {
			chosen = append(chosen, b.AppID)
		}
	case len(args) > 0:
		for _, nameOrID := range args {
			b, ok := config.FindBot(reg, nameOrID)
			if !ok {
				return fmt.Errorf("找不到机器人「%s」", nameOrID)
			}
			chosen = append(chosen, b.AppID)
		}
		default:
		if !isTerminal(os.Stdin) {
			return fmt.Errorf("未指定机器人。用法：`bot use <name...>` 或 `--all` / `--none`；在终端可直接 `bot use` 交互选择")
		}
		chosen, err = interactivePickBots(out, os.Stdin, reg)
		if err != nil {
			return err
		}
	}

	newReg, err := config.SetActiveBots(chosen)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "已更新活跃机器人：")
	for _, b := range newReg.Bots {
		mark := "·"
		if b.Active != nil && *b.Active {
			mark = "✓"
		}
		fmt.Fprintf(out, "  %s %s（%s）\n", mark, b.Name, b.AppID)
	}
	return nil
}

// interactivePickBots 在 TTY 前编号列出机器人，读一行选择（逗号分隔编号 / all / none）。
// in 用于读取选择（注入 io.Reader 便于测试，运行时传 os.Stdin）。
func interactivePickBots(out io.Writer, in io.Reader, reg config.BotsRegistry) ([]string, error) {
	fmt.Fprintln(out, "选择要同时连接的机器人（输入编号，逗号分隔；或 all / none）：")
	for i, b := range reg.Bots {
		mark := " "
		if b.Active != nil && *b.Active {
			mark = "*"
		}
		fmt.Fprintf(out, "  [%d] %s%s %s（%s）\n", i+1, mark, b.Name, b.AppID, b.BotName)
	}
	fmt.Fprint(out, "选择> ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return nil, fmt.Errorf("读取选择失败：%w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" || strings.EqualFold(line, "none") {
		return nil, nil
	}
	if strings.EqualFold(line, "all") {
		ids := make([]string, 0, len(reg.Bots))
		for _, b := range reg.Bots {
			ids = append(ids, b.AppID)
		}
		return ids, nil
	}
	parts := strings.Split(line, ",")
	seen := map[int]bool{}
	var chosen []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > len(reg.Bots) {
			return nil, fmt.Errorf("无效选择「%s」", p)
		}
		idx := n - 1
		if seen[idx] {
			continue
		}
		seen[idx] = true
		chosen = append(chosen, reg.Bots[idx].AppID)
	}
	return chosen, nil
}

// isTerminal 判断文件是否为字符设备（TTY）。用于 CLI 交互式选择降级。
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "本地自检：codex / 登录 / 配置 / 事件订阅",
		RunE:  doctorRun,
	}
}

func doctorRun(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := context.Background()

	// 1. codex 后端探测。
	fmt.Fprintln(out, "🧠 后端探测")
	be, err := agent.CreateBackend(agent.DEFAULT_BACKEND_ID)
	if err != nil {
		fmt.Fprintf(out, "  ❌ 创建后端失败：%v\n", err)
	} else {
		probe := be.Doctor(ctx, true)
		if probe.Ok {
			fmt.Fprintf(out, "  ✅ Codex %s（%s）\n", probe.Version, probe.Location)
		} else {
			fmt.Fprintf(out, "  ❌ Codex 不可用：%s\n", probe.Hint)
		}
	}

	// 2. 已注册机器人。
	fmt.Fprintln(out, "\n🤖 已注册机器人")
	reg, _ := config.LoadBots()
	if len(reg.Bots) == 0 {
		fmt.Fprintln(out, "  （无）")
	} else {
		for _, b := range reg.Bots {
			fmt.Fprintf(out, "  · %s（appId=%s, tenant=%s）\n", b.Name, b.AppID, b.Tenant)
		}
	}

	// 3. scope 授权提示。
	fmt.Fprintln(out, "\n🔐 权限")
	fmt.Fprintf(out, "  一键授权页（复制到浏览器打开）：\n")
	fmt.Fprintf(out, "  开发者后台「权限管理」页手动授权。\n")

	return nil
}

func newSecretsCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "secrets",
		Short:  "keystore exec-provider（lark-cli 调用解析密钥）",
		Hidden: true,
		RunE:   secretsRun,
	}
}

func secretsRun(cmd *cobra.Command, args []string) error {
	// 简化：仅打印 keystore 条目数（完整 exec-provider 协议后续）。
	ks := config.NewKeystore(config.SecretsFile(), config.KeystoreSaltFile())
	ids, err := ks.List()
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "keystore 条目：%d\n", len(ids))
	return nil
}

// newBotInitCmd bot init —— 交互式注册机器人。
func newBotInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [name]",
		Short: "注册一个飞书机器人并授权",
		RunE:  botInit,
	}
}

func botInit(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	reader := bufio.NewReader(os.Stdin)

	// 期望短名（可选）。
	desiredName := ""
	if len(args) > 0 {
		desiredName = args[0]
	}

	fmt.Fprint(out, "App ID (cli_ 开头): ")
	appID, _ := reader.ReadString('\n')
	appID = strings.TrimSpace(appID)

	fmt.Fprint(out, "App Secret: ")
	appSecret, _ := reader.ReadString('\n')
	appSecret = strings.TrimSpace(appSecret)

	fmt.Fprint(out, "租户 (feishu/lark，默认 feishu): ")
	tenant, _ := reader.ReadString('\n')
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		tenant = "feishu"
	}

	result := bot.RegisterBotFromCredentials(context.Background(), bot.RegisterBotInput{
		AppID:       appID,
		AppSecret:   appSecret,
		Tenant:      tenant,
		DesiredName: desiredName,
	}, http.DefaultClient)

	if !result.Ok {
		return fmt.Errorf("注册失败（%s）：%s", result.Code, result.Reason)
	}

	fmt.Fprintf(out, "\n✅ 注册成功！\n")
	fmt.Fprintf(out, "  名称：%s\n", result.Name)
	fmt.Fprintf(out, "  App ID：%s\n", result.AppID)
	fmt.Fprintf(out, "  租户：%s\n", result.Tenant)
	if result.BotName != "" {
		fmt.Fprintf(out, "  Bot 名称：%s\n", result.BotName)
	}
	if result.MissingScopes != nil && len(result.MissingScopes) > 0 {
		fmt.Fprintf(out, "\n⚠️  缺少 %d 项权限：\n", len(result.MissingScopes))
		for _, s := range result.MissingScopes {
			fmt.Fprintf(out, "  · %s\n", config.LabelScope(s))
		}
	}
	// 打印一键授权 URL。
	reg, _ := config.LoadBots()
	if cur, ok := config.FindBot(reg, result.AppID); ok {
		fmt.Fprintf(out, "\n🔑 一键授权页：%s\n", config.BuildScopeGrantUrl(cur.AppID, cur.Tenant))
	}
	return nil
}
