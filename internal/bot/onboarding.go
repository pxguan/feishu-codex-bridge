package bot

// onboarding.go —— 建群后的 onboarding 编排（对齐 TS project/onboarding.ts 的 onboardGroup）：
// 1) 发「🤖 本群使用说明」欢迎卡（card.BuildWelcomeCard，经 SendCardFunc 走 CardKit 实体路径）；
// 2) created 群额外 Pin 该卡 + 加群 Tab「👈 使用说明」+ 加群菜单「🤖 <后端名>」→ 手册。
// joined 群（bot 只是普通成员）只发欢迎卡，不 Pin/Tab/Menu（与 TS 一致）。
// 全部 best-effort：任何一步失败仅告警，不阻断建群。

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// helpDocURL 公开命令手册（互联网可读飞书文档）。对齐 TS onboarding.ts 的 HELP_DOC_URL。
const helpDocURL = "https://my.feishu.cn/wiki/PZ23wGr7JiKK5RkIG4rcZXzGn5g"

// onboardGroup 建群/绑群后的 onboarding（对齐 TS onboardGroup）。
func (o *Orchestrator) onboardGroup(ctx context.Context, p project.Project, chatID string) {
	if o.SendCardFunc == nil {
		core.Warn(ctx, "bot", "onboard", "SendCardFunc 未注入，欢迎卡无法发出")
		return
	}
	agentName := backendDisplayName(p.Backend)
	caps := welcomeCaps(p.Backend)
	noMention := effectiveNoMention(p)
	welcome := card.BuildWelcomeCard(p.Kind, helpDocURL, noMention, caps, agentName)
	jsonBytes, err := json.Marshal(welcome)
	if err != nil {
		core.Warn(ctx, "bot", "onboard", "欢迎卡序列化失败："+err.Error())
		return
	}
	msgID, err := o.SendCardFunc(ctx, chatID, jsonBytes)
	if err != nil {
		core.Warn(ctx, "bot", "onboard", "欢迎卡发送失败（可忽略）："+err.Error())
		return
	}
	core.Info(ctx, "bot", "onboard", "欢迎卡已发送 chatID="+chatID+" msgID="+msgID)

	// joined 群（bot 是普通成员）不 Pin/Tab/Menu —— 与 TS 一致。
	if !project.ShouldOnboard(p) {
		return
	}
	// 2a. Pin 欢迎卡。
	if err := o.pinMessage(ctx, msgID); err != nil {
		core.Warn(ctx, "bot", "onboard-pin", "欢迎卡 Pin 失败（可忽略，可能缺 im:chat:pin）："+err.Error())
	} else {
		core.Info(ctx, "bot", "onboard-pin", "欢迎卡已 Pin chatID="+chatID)
	}
	// 2b. 群 Tab「👈 使用说明」→ 手册。
	if err := o.addChatTab(ctx, chatID, "👈 使用说明", helpDocURL); err != nil {
		core.Warn(ctx, "bot", "onboard-tab", "群 Tab 失败（可忽略，可能缺 im:chat.tabs:write_only）："+err.Error())
	} else {
		core.Info(ctx, "bot", "onboard-tab", "群 Tab 已添加 chatID="+chatID)
	}
	// 2c. 群菜单「🤖 <后端名>」→ 手册（PC 端侧边栏打开）。
	if err := o.addChatMenu(ctx, chatID, "🤖 "+agentName, helpDocURL, project.SidebarPcUrl(helpDocURL)); err != nil {
		core.Warn(ctx, "bot", "onboard-menu", "群菜单失败（可忽略，可能缺 im:chat.menu_tree:write_only）："+err.Error())
	} else {
		core.Info(ctx, "bot", "onboard-menu", "群菜单已添加 chatID="+chatID)
	}
}

// pinMessage / addChatTab / addChatMenu 经局部接口断言调用 feishu.Channel 的原生方法。

func (o *Orchestrator) pinMessage(ctx context.Context, messageID string) error {
	type pinner interface{ PinMessage(context.Context, string) error }
	p, ok := o.Channel.(pinner)
	if !ok {
		return fmt.Errorf("Channel 未实现 PinMessage")
	}
	return p.PinMessage(ctx, messageID)
}

func (o *Orchestrator) addChatTab(ctx context.Context, chatID, name, url string) error {
	type tabber interface{ AddChatTab(context.Context, string, string, string) error }
	t, ok := o.Channel.(tabber)
	if !ok {
		return fmt.Errorf("Channel 未实现 AddChatTab")
	}
	return t.AddChatTab(ctx, chatID, name, url)
}

func (o *Orchestrator) addChatMenu(ctx context.Context, chatID, name, url, pcURL string) error {
	type menuer interface{ AddChatMenu(context.Context, string, string, string, string) error }
	m, ok := o.Channel.(menuer)
	if !ok {
		return fmt.Errorf("Channel 未实现 AddChatMenu")
	}
	return m.AddChatMenu(ctx, chatID, name, url, pcURL)
}

// backendDisplayName 本群后端的展示名（Codex / Claude …）；未知/未设 → Codex（向后兼容）。
func backendDisplayName(backend string) string {
	if backend == "" {
		backend = agent.DEFAULT_BACKEND_ID
	}
	if e, ok := agent.CatalogByID(backend); ok {
		return e.DisplayName
	}
	return "Codex"
}

// welcomeCaps 按后端能力裁剪欢迎卡命令（对齐 TS welcomeCaps）。
// 未知/手编 id 解析失败 → 返回零值 HelpCaps（nil 字段 ⇒ 全列，与 codex 一致）。
func welcomeCaps(backend string) card.HelpCaps {
	if backend == "" {
		backend = agent.DEFAULT_BACKEND_ID
	}
	b, err := agent.CreateBackend(backend)
	if err != nil {
		return card.HelpCaps{}
	}
	c := b.Capabilities()
	return card.HelpCaps{Goal: boolPtr(c.Goal), Compact: boolPtr(c.Compact), Resume: boolPtr(c.Resume)}
}

// effectiveNoMention 群有效免@状态（项目显式设置优先，否则取默认）。
func effectiveNoMention(p project.Project) bool {
	if p.NoMention != nil {
		return *p.NoMention
	}
	return project.DefaultNoMention(p)
}
