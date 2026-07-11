package card

// card_content.go —— 降级卡文本还原（对齐 TS bot/card-content 的纯函数部分）。
// 多维表格「发送消息卡片」自动化发的 CardKit 卡，bot 收到的是降级占位符；
// 需用 card_msg_content_type=raw_card_content 重取 json_card，提取真实文本+链接。
// 这里 port 纯解析（isDegradedCardContent/parseRawCardWrapper/extractRawCardText）；
// fetchInteractiveCardText 依赖 channel（飞书 SDK），在 feishu 包接上。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var degradedCardRe = regexp.MustCompile(`(?i)请升级至最新版本客户端|请使用新版本.*查看|client to view|upgrade .*client`)

// IsDegradedCardContent 内容是否为降级占位符（[interactive card] / 「请升级…」客户端占位）。
func IsDegradedCardContent(content string) bool {
	t := strings.TrimSpace(content)
	if t == "" || t == "[interactive card]" {
		return true
	}
	return degradedCardRe.MatchString(t)
}

// ParseRawCardWrapper 解包 raw_card_content 响应体：
// `{"json_card":"<stringified>","json_attachment":{…}}` → 解析后的 json_card 对象。
// 非 wrapped 则返回解析后的整体；坏 JSON 返回 (nil, false)。
func ParseRawCardWrapper(bodyContent string) (any, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(bodyContent), &parsed); err != nil {
		return nil, false
	}
	if s, ok := parsed["json_card"].(string); ok {
		var card any
		if err := json.Unmarshal([]byte(s), &card); err != nil {
			return nil, false
		}
		return card, true
	}
	return parsed, true
}

// ExtractRawCardText 从 json_card 提取可读文本+链接（property-wrapped card-builder schema）。
// i18nElements 只取一个 locale（zh_cn 优先，避免 5× 重复）；输出去重保序。
func ExtractRawCardText(jsonCard any) string {
	var out []string
	visitCard(jsonCard, &out)
	seen := map[string]bool{}
	var lines []string
	for _, p := range out {
		k := strings.TrimSpace(p)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		lines = append(lines, k)
	}
	return strings.Join(lines, "\n")
}

func visitCard(node any, out *[]string) {
	if node == nil {
		return
	}
	switch v := node.(type) {
	case []any:
		for _, c := range v {
			visitCard(c, out)
		}
		return
	case map[string]any:
		// 顶层容器（header 先，让标题领正文）。
		if h, ok := v["header"]; ok {
			visitCard(h, out)
		}
		if b, ok := v["body"]; ok {
			visitCard(b, out)
		}
		prop, ok := v["property"].(map[string]any)
		if !ok {
			return
		}
		// 叶子文本：plain_text / link / markdown-leaf。link 配对 content+url。
		if c, ok := prop["content"].(string); ok {
			ct := strings.TrimSpace(c)
			if ct != "" {
				url := ""
				if u, ok := prop["url"].(map[string]any); ok {
					if uu, ok := u["url"].(string); ok {
						url = uu
					}
				}
				if url != "" {
					*out = append(*out, fmt.Sprintf("[%s](%s)", ct, url))
				} else {
					*out = append(*out, c)
				}
			}
		}
		// 多语言 footer：只取一个 locale（zh_cn 优先）。
		if i18n, ok := prop["i18nElements"].(map[string]any); ok {
			locale := firstNonNil(i18n["zh_cn"], i18n["zh_hk"], i18n["zh_tw"], i18n["en_us"])
			if locale == nil {
				for _, lv := range i18n {
					locale = lv
					break
				}
			}
			visitCard(locale, out)
		}
		// 嵌套结构：title / button label / rows / columns / action groups。
		visitCard(prop["title"], out)
		visitCard(prop["text"], out)
		visitCard(prop["elements"], out)
		visitCard(prop["columns"], out)
		visitCard(prop["actions"], out)
	}
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}
