package card

// dm_projects.go —— DM 控制台项目列表卡 + 话题钻取卡（对齐 TS card/dm-cards）。
// 列表为分页概览：每项目一行摘要 + 操作行（进群 / 🧵话题 / ⚙️设置 / 🗑删除）+ 翻页。
// 话题不内联（防止超出飞书 ~200 组件上限静默截断）；点 🧵 进话题卡。

import (
	"fmt"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// 列表单页项目数（飞书嵌套组件计数 ~17/项目，≥9 会超 ~200 上限被静默丢弃）。
const ProjectListPageSize = 8

// ProjectListInfo 项目列表卡数据。
type ProjectListInfo struct {
	Projects     []project.Project
	TopicsByChat map[string]int // chatID → 话题数
	Page         int            // 0-indexed，越界自动夹紧
}

// BuildProjectListCard DM 项目列表卡（分页）。
func BuildProjectListCard(info ProjectListInfo) CardObject {
	noForward := false
	var elements []CardElement
	if len(info.Projects) == 0 {
		elements = append(elements,
			Md("还没有项目。点 **➕ 新建项目** 或直接发我一个项目名。"),
			Actions([]CardElement{BackToMenu()}, ""),
		)
		return Card(elements, CardOpts{Header: &CardHeader{Title: "📁 项目列表", Template: HeaderWathet}, Forward: &noForward})
	}
	pageCount := (len(info.Projects) + ProjectListPageSize - 1) / ProjectListPageSize
	cur := info.Page
	if cur < 0 {
		cur = 0
	}
	if cur > pageCount-1 {
		cur = pageCount - 1
	}
	start := cur * ProjectListPageSize
	end := start + ProjectListPageSize
	if end > len(info.Projects) {
		end = len(info.Projects)
	}
	for _, p := range info.Projects[start:end] {
		if p.ChatID != "" {
			elements = append(elements, projectListEntry(p, info.TopicsByChat[p.ChatID]))
		} else {
			elements = append(elements, projectListEntryNoChat(p))
		}
		elements = append(elements, Hr())
	}
	if pageCount > 1 {
		elements = append(elements, Note(fmt.Sprintf("共 %d 个项目 · 第 %d/%d 页", len(info.Projects), cur+1, pageCount)))
	} else {
		elements = append(elements, Note(fmt.Sprintf("共 %d 个项目", len(info.Projects))))
	}
	nav := []CardElement{}
	if cur > 0 {
		nav = append(nav, Button("⬅️ 上一页", ActionValue{"a": DMProjects, "p": fmt.Sprintf("%d", cur-1)}, ButtonDefault))
	}
	if cur < pageCount-1 {
		nav = append(nav, Button("下一页 ➡️", ActionValue{"a": DMProjects, "p": fmt.Sprintf("%d", cur+1)}, ButtonDefault))
	}
	nav = append(nav, BackToMenu())
	elements = append(elements, Actions(nav, ""))
	return Card(elements, CardOpts{Header: &CardHeader{Title: "📁 项目列表", Template: HeaderWathet}, Forward: &noForward})
}

func projectListEntry(p project.Project, topicCount int) CardElement {
	dir := fmt.Sprintf("📂 `%s`", p.Cwd)
	if p.Branch != "" && p.Branch != "—" {
		dir += fmt.Sprintf("   🌿 %s", p.Branch)
	}
	meta := fmt.Sprintf("%s%s   ·   免@：%s",
		KindLabel(p.Kind),
		ternary(p.Origin == "joined", " · 🔗已加入", ""),
		ternary(project.DefaultNoMention(p), "开", "关"),
	)
	row := []CardElement{LinkButton("💬 打开群聊", OpenChatURL(p.ChatID), ButtonDefault, "")}
	row = append(row, Button(fmt.Sprintf("🧵 %d 话题", topicCount), ActionValue{"a": DMProjectTopics, "n": p.Name}, ButtonDefault))
	row = append(row, Button("⚙️ 设置", ActionValue{"a": DMProjectSettings, "n": p.Name}, ButtonDefault))
	row = append(row, Button("🗑 删除", ActionValue{"a": DMRmConfirm, "n": p.Name}, ButtonDanger))
	return columnRow(NoteMd(strings.Join([]string{fmt.Sprintf("**%s**%s", p.Name, ternary(p.Blank, " _(空白)_", "")), dir, meta}, " · ")), row)
}

func projectListEntryNoChat(p project.Project) CardElement {
	meta := fmt.Sprintf("%s%s   ·   免@：%s",
		KindLabel(p.Kind),
		ternary(p.Origin == "joined", " · 🔗已加入", ""),
		ternary(project.DefaultNoMention(p), "开", "关"),
	)
	row := []CardElement{
		Button("⚙️ 设置", ActionValue{"a": DMProjectSettings, "n": p.Name}, ButtonDefault),
		Button("🗑 删除", ActionValue{"a": DMRmConfirm, "n": p.Name}, ButtonDanger),
	}
	body := fmt.Sprintf("**%s**%s", p.Name, ternary(p.Blank, " _(空白)_", ""))
	dir := fmt.Sprintf("📂 `%s`", p.Cwd)
	return columnRow(NoteMd(strings.Join([]string{body, dir, meta, "⚠️ 未绑定群"}, " · ")), row)
}

// ProjectTopic 话题钻取卡的一行。
type ProjectTopic struct {
	Summary   string
	UpdatedAt int64
}

// BuildProjectTopicsCard 单项目话题钻取卡（ newest first，截断到上限）。
func BuildProjectTopicsCard(projectName, chatID string, topics []ProjectTopic) CardObject {
	elements := []CardElement{Md(fmt.Sprintf("**%s** · 共 %d 个话题", projectName, len(topics)))}
	if len(topics) == 0 {
		elements = append(elements, Note("（暂无话题）"))
	} else {
		const max = 50
		n := len(topics)
		if n > max {
			n = max
		}
		for i := 0; i < n; i++ {
			t := topics[i]
			title := strings.TrimSpace(t.Summary)
			if len(title) > 50 {
				title = title[:50]
			}
			if title == "" {
				title = "(空)"
			}
			elements = append(elements, Note(fmt.Sprintf("· %s", title)))
		}
		if len(topics) > max {
			elements = append(elements, Note(fmt.Sprintf("· …还有 %d 个话题（更早的可在群里 /resume 恢复）", len(topics)-max)))
		}
	}
	nav := []CardElement{}
	if chatID != "" {
		nav = append(nav, LinkButton("💬 打开群聊", OpenChatURL(chatID), ButtonDefault, ""))
	}
	nav = append(nav, Button("⬅️ 项目列表", ActionValue{"a": DMProjects}, ButtonDefault))
	elements = append(elements, Hr(), Actions(nav, ""))
	return Card(elements, CardOpts{Header: &CardHeader{Title: fmt.Sprintf("🧵 话题 · %s", projectName), Template: HeaderWathet}})
}

// columnRow 左文右按钮双列。
func columnRow(left CardElement, right []CardElement) CardElement {
	return CardElement{
		"tag":                "column_set",
		"flex_mode":          "none",
		"horizontal_spacing": "medium",
		"columns": []CardElement{
			{"tag": "column", "width": "weighted", "weight": 1, "elements": []CardElement{left}},
			{"tag": "column", "width": "auto", "elements": right},
		},
	}
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
