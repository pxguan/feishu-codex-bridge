package card

import (
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func TestBuildProjectListCard_Empty(t *testing.T) {
	c := BuildProjectListCard(ProjectListInfo{})
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	joined := joinMd(els)
	if !strings.Contains(joined, "还没有项目") {
		t.Fatalf("empty should show hint: %q", joined)
	}
}

func TestBuildProjectListCard_WithProjects(t *testing.T) {
	info := ProjectListInfo{
		Projects: []project.Project{
			{Name: "alpha", ChatID: "oc_1", Cwd: "/proj/a", Kind: "multi"},
			{Name: "beta", ChatID: "oc_2", Cwd: "/proj/b", Kind: "single", Mode: "qa"},
		},
	}
	c := BuildProjectListCard(info)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	// 顶层 md 应含计数（项目名在 column_set 内，joinMd 不递归）。
	joined := joinMd(els)
	if !strings.Contains(joined, "个项目") {
		t.Fatalf("should show count: %q", joined)
	}
	// 验证 column_set 内的项目名（递归 flatten）。
	hasAlpha := false
	for _, e := range els {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if c, ok := sub["content"].(string); ok && strings.Contains(c, "alpha") {
						hasAlpha = true
					}
				}
			}
		}
	}
	if !hasAlpha {
		t.Fatal("column_set should contain project name alpha")
	}
}

func TestBuildProjectListCard_HasButtons(t *testing.T) {
	c := BuildProjectListCard(ProjectListInfo{
		Projects: []project.Project{{Name: "p", ChatID: "oc_1"}},
	})
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	// 列表项含动作按钮（打开群聊 LinkButton + 话题/设置/删除 回调按钮）。
	// 注意：DMNewProject 只在菜单卡出现，不在项目列表项。
	hasSettings := false
	for _, e := range els {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] != "button" {
						continue
					}
					bs, ok := sub["behaviors"].([]CardElement)
					if !ok || len(bs) == 0 {
						continue
					}
					if bs[0]["type"] != "callback" {
						continue // LinkButton(open_url) 无 value
					}
					val, ok := bs[0]["value"].(ActionValue)
					if !ok {
						continue
					}
					if val["a"] == DMProjectSettings {
						hasSettings = true
					}
				}
			}
		}
	}
	if !hasSettings {
		t.Fatal("should have 设置 button")
	}
}

func TestBuildProjectListCard_ForwardDisabled(t *testing.T) {
	c := BuildProjectListCard(ProjectListInfo{})
	if c["config"].(CardElement)["enable_forward"] != false {
		t.Fatal("project list should disable forward")
	}
}
