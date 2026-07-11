package project

// onboarding.go —— 项目建群后的 onboarding 纯函数（对齐 TS project/onboarding 的纯函数部分）。
// OnboardingText 构建、sidebarPcUrl 等。
// 飞书 SDK 操作（发欢迎卡 / Pin / 建群 Tab / 建群菜单）后续 feishu wrapper port 后接上。

import "fmt"

// OnboardingText 欢迎卡文本（建群/绑定后的第一条卡）。
func OnboardingText(p Project, agentName string) string {
	if agentName == "" {
		agentName = "Codex"
	}
	return fmt.Sprintf("👋 欢迎使用 %s Bridge — 本群已绑定项目目录 `%s`，在群里 @我 即可开始。", agentName, p.Cwd)
}

// SidebarPcUrl 给 pc_url 套 applink sidebar-semi 模式（M-6 可发现性）。
func SidebarPcUrl(pcURL string) string {
	if pcURL == "" {
		return ""
	}
	return fmt.Sprintf("https://applink.feishu.cn/client/link?url=%s&mode=sidebar-semi", pcURL)
}

// ShouldOnboard 判断是否需要 onboarding（created 项目才 Pin/Tab/Menu；joined 只发欢迎卡）。
func ShouldOnboard(p Project) bool {
	return p.Origin == "" || p.Origin == "created"
}
