package card

import (
	"strings"
	"testing"
)

func TestExtractCardFences(t *testing.T) {
	text := "before\n```feishu-card\n# Title\nbody\n```\nafter"
	r := ExtractCardFences(text)
	if len(r.Fences) != 1 || strings.TrimSpace(r.Fences[0]) != "# Title\nbody" {
		t.Fatalf("fences wrong: %+v", r.Fences)
	}
	if strings.Contains(r.Stripped, "feishu-card") || strings.Contains(r.Stripped, "Title") {
		t.Fatalf("stripped should remove fence: %q", r.Stripped)
	}
	if !strings.Contains(r.Stripped, "before") || !strings.Contains(r.Stripped, "after") {
		t.Fatalf("stripped should keep surrounding text: %q", r.Stripped)
	}
}

func TestRenderRichText_PlainShortCircuit(t *testing.T) {
	els := RenderRichText("just plain text", nil)
	if len(els) != 1 || els[0]["tag"] != "markdown" || els[0]["content"] != "just plain text" {
		t.Fatalf("plain short-circuit wrong: %+v", els)
	}
	if RenderRichText("   ", nil) != nil {
		t.Fatal("whitespace-only should return nil")
	}
}

func TestRenderRichText_ImageReplaced(t *testing.T) {
	text := "see ![a](p1.png) here"
	els := RenderRichText(text, map[string]string{"p1.png": "img_key_1"})
	// 应含一个 img + 前后文本 md。
	hasImg := false
	for _, e := range els {
		if e["tag"] == "img" && e["img_key"] == "img_key_1" {
			hasImg = true
		}
	}
	if !hasImg {
		t.Fatalf("image should be replaced with img element: %+v", els)
	}
}

func TestRenderRichText_UnresolvedImageKeptLiteral(t *testing.T) {
	text := "see ![a](nonexistent.png) here"
	els := RenderRichText(text, nil)
	// 未解析 → 保留原文（不静默丢）。
	joined := ""
	for _, e := range els {
		if c, ok := e["content"].(string); ok {
			joined += c
		}
	}
	if !strings.Contains(joined, "nonexistent.png") {
		t.Fatalf("unresolved image should keep literal markdown: %q", joined)
	}
}

func TestBuildCleanCard_TitleHeader(t *testing.T) {
	c := BuildCleanCard("# 我的卡片\nbody line\n\n> note", nil, HeaderBlue)
	h, ok := c["header"]
	if !ok {
		t.Fatal("title should become header")
	}
	if h.(CardElement)["title"].(CardElement)["content"] != "我的卡片" {
		t.Fatal("header title wrong")
	}
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	if len(els) == 0 {
		t.Fatal("body should have elements")
	}
	// 应含 note（> 引用）。
	hasNote := false
	for _, e := range els {
		if e["tag"] == "div" {
			hasNote = true
		}
	}
	if !hasNote {
		t.Fatalf("quote block should map to note: %+v", els)
	}
}

func TestBuildCleanCard_NoTitle(t *testing.T) {
	c := BuildCleanCard("just body", nil, HeaderBlue)
	if _, ok := c["header"]; ok {
		t.Fatal("no heading → no header")
	}
}

func TestBuildCleanCard_HrBlock(t *testing.T) {
	c := BuildCleanCard("# T\n\n---\n\nbody", nil, HeaderBlue)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	hasHr := false
	for _, e := range els {
		if e["tag"] == "hr" {
			hasHr = true
		}
	}
	if !hasHr {
		t.Fatalf("--- should map to hr: %+v", els)
	}
}
