package project

// announcement.go —— 群公告文本构建（对齐 TS project/announcement 的纯函数部分）。
// AnnouncementText: 📁 name · 📣 cwd · 🌿 branch。
// 飞书 docx 公告 API 写入后续 feishu wrapper port 后接上。

import "strings"

// AnnouncementText 构建群公告横幅文本。
// 非空项目显 cwd 路径；git 仓库显 branch（非 git 不显）。
func AnnouncementText(p Project, branch string) string {
	parts := []string{}
	if p.Name != "" {
		parts = append(parts, "📁 "+p.Name)
	}
	if p.Cwd != "" {
		parts = append(parts, "📣 "+p.Cwd)
	}
	if branch != "" {
		parts = append(parts, "🌿 "+branch)
	}
	return strings.Join(parts, " · ")
}

// ShouldRefreshBranch 判断分支是否变化（懒检测：仅分支变化才重写公告）。
func ShouldRefreshBranch(oldBranch, newBranch string) bool {
	return oldBranch != newBranch
}
