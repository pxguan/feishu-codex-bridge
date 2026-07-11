package card

import (
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func TestBuildGroupSettingsCard_NoProject(t *testing.T) {
	// TS：未绑定项目时 handler 单独发 markdown（"本群未绑定项目…"），
	// 不会用空 project 调此卡；此卡永远带 name。这里只验证空输入也能
	// 安全渲染出一张合法卡（不 panic、含「群设置」标题）。
	c := BuildGroupSettingsCard(GroupSettingsInfo{})
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !strings.Contains(joined, "群设置") {
		t.Fatalf("should render 群设置 title: %q", joined)
	}
}

func TestBuildGroupSettingsCard_WithProject(t *testing.T) {
	p := &project.Project{Name: "test", ChatID: "oc_1", Cwd: "/proj"}
	c := BuildGroupSettingsCard(GroupSettingsInfo{Project: *p})
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	// 应含免@、自动压缩、模型、权限、返回菜单按钮。
	buttonCount := 0
	for _, e := range els {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						buttonCount++
					}
				}
			}
		}
	}
	if buttonCount < 5 {
		t.Fatalf("should have at least 5 buttons (noMention+autoCompact+model+perm+back): got %d", buttonCount)
	}
}

func TestBuildGroupSettingsCard_ForwardDisabled(t *testing.T) {
	// TS buildGroupSettingsCard 不禁用转发（默认 true，与 TS 对齐）。
	// 群设置卡无敏感信息，转发无害。
	c := BuildGroupSettingsCard(GroupSettingsInfo{Project: project.Project{Name: "x"}})
	if c["config"].(CardElement)["enable_forward"] != nil {
		t.Fatal("settings card should not set enable_forward (TS default = forwardable)")
	}
}
