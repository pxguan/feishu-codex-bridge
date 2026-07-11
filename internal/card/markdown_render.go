package card

// markdown_render.go —— markdown → 卡片元素渲染（对齐 TS card/markdown-render）。
// 两个职责：renderRichText（markdown+图片→元素）、buildCleanCard（feishu-card 围栏→独立卡）。
// 纯函数，依赖 element builder + outbound imgRe，零飞书 SDK 调用。

import (
	"regexp"
	"strings"
)

var (
	fenceRe     = regexp.MustCompile("```feishu-card[^\\n]*\\n([\\s\\S]*?)```")
	headingRe   = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)
	hrBlockRe   = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	quoteLineRe = regexp.MustCompile(`^\s*>\s?`)
)

// FencesResult extractCardFences 结果。
type FencesResult struct {
	Fences   []string // 围栏内 markdown（trim）
	Stripped string   // 移除围栏后的文本
}

// ExtractCardFences 抽出全部 ```feishu-card 围栏（返回内部 markdown + 移除围栏的文本）。
func ExtractCardFences(text string) FencesResult {
	var fences []string
	stripped := fenceRe.ReplaceAllStringFunc(text, func(full string) string {
		m := fenceRe.FindStringSubmatch(full)
		if len(m) >= 2 {
			fences = append(fences, strings.TrimSpace(m[1]))
		}
		return ""
	})
	return FencesResult{Fences: fences, Stripped: stripped}
}

// RenderRichText markdown → 元素：已上传图片换 Image 元素，未解析保留原文，剥 feishu-card 围栏。
// 纯文本（无 ![）短路为单个 Md 元素。
func RenderRichText(text string, images map[string]string) []CardElement {
	body := ExtractCardFences(text).Stripped
	if !strings.Contains(body, "![") {
		t := strings.TrimSpace(body)
		if t == "" {
			return nil
		}
		return []CardElement{Md(t)}
	}
	var els []CardElement
	buf := ""
	flush := func() {
		if t := strings.TrimSpace(buf); t != "" {
			els = append(els, Md(t))
		}
		buf = ""
	}
	locs := imgRe.FindAllStringSubmatchIndex(body, -1)
	last := 0
	for _, loc := range locs {
		fullStart, fullEnd := loc[0], loc[1]
		altStart, altEnd, srcStart, srcEnd := loc[2], loc[3], loc[4], loc[5]
		buf += body[last:fullStart]
		alt := body[altStart:altEnd]
		src := CleanSrc(body[srcStart:srcEnd])
		if key, ok := images[src]; ok {
			flush()
			els = append(els, Image(key, alt))
		} else {
			// 未解析（cwd 外 / 上传失败）：保留原文，绝不静默丢引用。
			buf += body[fullStart:fullEnd]
		}
		last = fullEnd
	}
	buf += body[last:]
	flush()
	return els
}

// BuildCleanCard 从一个 feishu-card 围栏的 markdown 构造独立卡：
// 首行 # 标题 → header（蓝），其余按空行分块映射元素（--- → hr、> 引用 → note、其它 → 图片感知 markdown）。
func BuildCleanCard(fenceMarkdown string, images map[string]string, template HeaderTemplate) CardObject {
	lines := strings.Split(fenceMarkdown, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	title := ""
	if start < len(lines) {
		if m := headingRe.FindStringSubmatch(lines[start]); len(m) >= 2 {
			title = strings.TrimSpace(m[1])
			start++
		}
	}
	bodyMd := strings.TrimSpace(strings.Join(lines[start:], "\n"))
	elements := renderCleanBody(bodyMd, images)
	if len(elements) == 0 {
		fallback := title
		if fallback == "" {
			fallback = ""
		}
		elements = []CardElement{Md(fallback)}
	}
	summary := title
	if summary == "" {
		summary = "卡片"
	}
	opts := CardOpts{Summary: summary}
	if title != "" {
		opts.Header = &CardHeader{Title: title, Template: template}
	}
	return Card(elements, opts)
}

// renderCleanBody 按空行分块映射：分隔线 → hr；引用块 → note；其余 → renderRichText。
func renderCleanBody(bodyMarkdown string, images map[string]string) []CardElement {
	var out []CardElement
	for _, raw := range regexp.MustCompile(`\n{2,}`).Split(bodyMarkdown, -1) {
		block := strings.TrimSpace(raw)
		if block == "" {
			continue
		}
		if hrBlockRe.MatchString(block) {
			out = append(out, Hr())
			continue
		}
		blockLines := strings.Split(block, "\n")
		allQuote := true
		for _, l := range blockLines {
			if strings.TrimSpace(l) != "" && !quoteLineRe.MatchString(l) {
				allQuote = false
				break
			}
		}
		if allQuote {
			stripped := []string{}
			for _, l := range blockLines {
				stripped = append(stripped, quoteLineRe.ReplaceAllString(l, ""))
			}
			noteText := strings.TrimSpace(strings.Join(stripped, "\n"))
			if noteText != "" {
				out = append(out, Note(noteText))
			}
			continue
		}
		out = append(out, RenderRichText(block, images)...)
	}
	return out
}
