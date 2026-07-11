package card

import (
	"strings"
	"testing"
)

func TestIsDegradedCardContent(t *testing.T) {
	degraded := []string{"", "[interactive card]", "请升级至最新版本客户端，以查看内容", "please upgrade your client to view"}
	for _, s := range degraded {
		if !IsDegradedCardContent(s) {
			t.Errorf("should be degraded: %q", s)
		}
	}
	if IsDegradedCardContent("真实业务文本") {
		t.Fatal("real text should not be degraded")
	}
}

func TestParseRawCardWrapper_StringifiedJsonCard(t *testing.T) {
	// json_card 是字符串化 JSON。
	body := `{"json_card":"{\"property\":{\"content\":\"hi\"}}"}`
	v, ok := ParseRawCardWrapper(body)
	if !ok {
		t.Fatal("should parse")
	}
	m, _ := v.(map[string]any)
	if m["property"] == nil {
		t.Fatal("json_card should be unwrapped")
	}
}

func TestParseRawCardWrapper_ObjectDirect(t *testing.T) {
	body := `{"foo":"bar"}`
	v, ok := ParseRawCardWrapper(body)
	if !ok {
		t.Fatal("should parse non-wrapped object")
	}
	if v.(map[string]any)["foo"] != "bar" {
		t.Fatal("object passthrough wrong")
	}
}

func TestParseRawCardWrapper_BadJson(t *testing.T) {
	if _, ok := ParseRawCardWrapper("not json"); ok {
		t.Fatal("bad json should return false")
	}
}

func TestExtractRawCardText_ContentAndLink(t *testing.T) {
	card := map[string]any{
		"header": map[string]any{
			"property": map[string]any{"content": "卡片标题"},
		},
		"body": map[string]any{
			"property": map[string]any{
				"elements": []any{
					map[string]any{"property": map[string]any{"content": "请处理这条记录", "url": map[string]any{"url": "https://base.feishu.cn/x"}}},
				},
			},
		},
	}
	text := ExtractRawCardText(card)
	if !strings.Contains(text, "卡片标题") {
		t.Fatalf("missing title: %q", text)
	}
	if !strings.Contains(text, "请处理这条记录") {
		t.Fatalf("missing body content: %q", text)
	}
	if !strings.Contains(text, "[请处理这条记录](https://base.feishu.cn/x)") {
		t.Fatalf("link should pair content+url: %q", text)
	}
}

func TestExtractRawCardText_Dedup(t *testing.T) {
	card := map[string]any{
		"body": map[string]any{
			"property": map[string]any{
				"elements": []any{
					map[string]any{"property": map[string]any{"content": "重复"}},
					map[string]any{"property": map[string]any{"content": "重复"}},
				},
			},
		},
	}
	text := ExtractRawCardText(card)
	if strings.Count(text, "重复") != 1 {
		t.Fatalf("should dedup: %q", text)
	}
}

func TestExtractRawCardText_I18nOneLocale(t *testing.T) {
	card := map[string]any{
		"property": map[string]any{
			"i18nElements": map[string]any{
				"zh_cn": []any{map[string]any{"property": map[string]any{"content": "来自"}}},
				"en_us": []any{map[string]any{"property": map[string]any{"content": "from"}}},
			},
		},
	}
	text := ExtractRawCardText(card)
	if strings.Contains(text, "from") {
		t.Fatalf("en_us should not appear (zh_cn first): %q", text)
	}
	if !strings.Contains(text, "来自") {
		t.Fatalf("zh_cn content missing: %q", text)
	}
}
