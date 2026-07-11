package feishu

import (
	"encoding/json"
	"testing"
)

// TestMarkdownCardJSON 验证 SendMarkdown 发出的交互卡片：
//   - schema=2.0
//   - body.elements[0] 是 tag=markdown 的元素，且原样保留 markdown 标记（由卡片渲染层负责渲染）
func TestMarkdownCardJSON(t *testing.T) {
	cardJSON, err := markdownCardJSON("**bold** 文本\n\n- 列表项")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("card not valid JSON: %v", err)
	}
	if card["schema"] != "2.0" {
		t.Errorf("schema = %v, want 2.0", card["schema"])
	}
	body, ok := card["body"].(map[string]any)
	if !ok {
		t.Fatalf("body missing or wrong type: %#v", card["body"])
	}
	elements, ok := body["elements"].([]any)
	if !ok || len(elements) != 1 {
		t.Fatalf("elements = %#v, want exactly 1 element", body["elements"])
	}
	el, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatalf("element wrong type: %#v", elements[0])
	}
	if el["tag"] != "markdown" {
		t.Errorf("element tag = %v, want markdown", el["tag"])
	}
	if el["content"] != "**bold** 文本\n\n- 列表项" {
		t.Errorf("content = %v, want raw markdown (markers preserved for card renderer)", el["content"])
	}
}
