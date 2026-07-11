package bot

// context_weave.go —— 入站消息上下文织入的纯函数（对齐 TS bot/context-weave）。
// extractMessageText（消息体→文本）+ sanitizeContext（抗注入）+ weaveQuote/weaveThreadHistory/weaveSender（织入 prompt）。
// 飞书 SDK 部分（fetchQuotedMessage/fetchThreadContext）后续 bot/feishu wrapper port 后接上。

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ContextMessage 拉取的上下文消息（引用消息 / 话题历史条目）。
type ContextMessage struct {
	MessageID  string
	SenderName string
	Text       string
	FromUser   bool
	CreateTime int64 // epoch ms
}

// Mention @mention（key=@_user_N, name=展示名）。
type Mention struct{ Key, Name string }

const (
	quoteMax       = 800
	lineMax        = 280
	threadWeaveMax = 20
	senderNameMax  = 40
)

const cardUpgradeHint = "请升级至最新版本客户端"

// ExtractMessageText 从原始消息体提取人类可读文本。text/post 提文字（@mention 解析），
// 其余折叠为占位符（[图片]/[文件：x]/…）。导出供测试。
func ExtractMessageText(msgType, content string, mentions []Mention) string {
	if content == "" {
		return placeholderFor(msgType)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return placeholderFor(msgType)
	}
	switch msgType {
	case "text":
		t, _ := parsed["text"].(string)
		return replaceMentions(t, mentions)
	case "post":
		return replaceMentions(extractPostText(parsed), mentions)
	case "image":
		return "[图片]"
	case "audio":
		return "[语音]"
	case "media":
		return "[视频]"
	case "file":
		if name, ok := parsed["file_name"].(string); ok && name != "" {
			return "[文件：" + name + "]"
		}
		return "[文件]"
	case "sticker":
		return "[表情]"
	case "interactive":
		if t := ExtractCardText(parsed); t != "" {
			return t
		}
		return "[卡片消息]"
	case "share_chat":
		return "[分享群名片]"
	case "share_user":
		return "[分享个人名片]"
	case "merge_forward", "forward":
		return "[合并转发消息]"
	}
	return placeholderFor(msgType)
}

func placeholderFor(msgType string) string {
	if msgType == "" {
		return "[消息]"
	}
	return "[" + msgType + " 消息]"
}

// extractPostText 从 post（富文本）body 提取文本（支持 {title,content} 和 locale-wrapped {zh_cn:{...}}）。
func extractPostText(parsed map[string]any) string {
	title, _ := parsed["title"].(string)
	blocks, _ := parsed["content"].([]any)
	// locale-wrapped 形态。
	if blocks == nil {
		for _, v := range parsed {
			if vm, ok := v.(map[string]any); ok {
				if c, ok := vm["content"].([]any); ok {
					if t, ok := vm["title"].(string); ok {
						title = t
					}
					blocks = c
					break
				}
			}
		}
	}
	var parts []string
	if strings.TrimSpace(title) != "" {
		parts = append(parts, strings.TrimSpace(title))
	}
	for _, line := range blocks {
		lineArr, ok := line.([]any)
		if !ok {
			continue
		}
		var lineText strings.Builder
		for _, node := range lineArr {
			lineText.WriteString(nodeToText(node))
		}
		if lineText.Len() > 0 {
			parts = append(parts, lineText.String())
		}
	}
	return strings.Join(parts, "\n")
}

func nodeToText(node any) string {
	n, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	tag, _ := n["tag"].(string)
	switch tag {
	case "text":
		t, _ := n["text"].(string)
		return t
	case "a":
		t, _ := n["text"].(string)
		if t != "" {
			return t
		}
		href, _ := n["href"].(string)
		return href
	case "at":
		name, _ := n["user_name"].(string)
		if name != "" {
			return "@" + name
		}
		return "@某人"
	case "img":
		return "[图片]"
	case "media":
		return "[视频]"
	case "emotion":
		return "[表情]"
	}
	t, _ := n["text"].(string)
	return t
}

// ExtractCardText 从 interactive（卡片）body 提取文本（down-convert 形态 {title,elements:[[node]]}）。
func ExtractCardText(parsed map[string]any) string {
	var parts []string
	title := textValue(parsed["title"])
	if strings.TrimSpace(title) != "" {
		parts = append(parts, strings.TrimSpace(title))
	}
	if elements, ok := parsed["elements"].([]any); ok {
		for _, line := range elements {
			var nodes []any
			if arr, ok := line.([]any); ok {
				nodes = arr
			} else {
				nodes = []any{line}
			}
			var lineText strings.Builder
			for _, n := range nodes {
				lineText.WriteString(cardNodeToText(n))
			}
			lt := strings.TrimSpace(lineText.String())
			if lt != "" && !strings.Contains(lt, cardUpgradeHint) {
				parts = append(parts, lt)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func textValue(t any) string {
	if s, ok := t.(string); ok {
		return s
	}
	if m, ok := t.(map[string]any); ok {
		if c, ok := m["content"].(string); ok {
			return c
		}
	}
	return ""
}

func cardNodeToText(node any) string {
	if s, ok := node.(string); ok {
		return s
	}
	n, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	tag, _ := n["tag"].(string)
	switch tag {
	case "text":
		return textValue(n["text"])
	case "a":
		t := textValue(n["text"])
		if t != "" {
			return t
		}
		href, _ := n["href"].(string)
		return href
	case "at":
		name, _ := n["user_name"].(string)
		if name != "" {
			return "@" + name
		}
		return "@某人"
	case "note":
		if els, ok := n["elements"].([]any); ok {
			var sb strings.Builder
			for _, e := range els {
				sb.WriteString(cardNodeToText(e))
			}
			return sb.String()
		}
		return ""
	case "button":
		label := textValue(n["text"])
		if label != "" {
			return "[按钮：" + label + "]"
		}
		return ""
	case "img":
		return ""
	}
	return textValue(n["text"])
}

func replaceMentions(text string, mentions []Mention) string {
	if text == "" || len(mentions) == 0 {
		return text
	}
	out := text
	for _, m := range mentions {
		if m.Key == "" {
			continue
		}
		name := "@某人"
		if m.Name != "" {
			name = "@" + m.Name
		}
		out = strings.ReplaceAll(out, m.Key, name)
	}
	return out
}

var (
	controlCharRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
	crRe          = regexp.MustCompile(`\r\n?`)
	whiteAllRe    = regexp.MustCompile(`\s+`)
	trailNLRe     = regexp.MustCompile(`[ \t]+\n`)
	multiNLRe     = regexp.MustCompile(`\n{3,}`)
)

// SanitizeContext 唯一 sanitize 边界（uplink-controlled text 织入 prompt）。
// 剥控制字符 + 归一换行 + clamp 长度。oneLine=true 折叠所有空白（防伪造 fenced block）。
func SanitizeContext(s string, maxLen int, oneLine bool) string {
	if s == "" {
		return ""
	}
	out := controlCharRe.ReplaceAllString(s, "")
	out = crRe.ReplaceAllString(out, "\n")
	if oneLine {
		out = whiteAllRe.ReplaceAllString(out, " ")
	} else {
		out = trailNLRe.ReplaceAllString(out, "\n")
		out = multiNLRe.ReplaceAllString(out, "\n\n")
	}
	out = strings.TrimSpace(out)
	runes := []rune(out)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "…"
	}
	return out
}

func prependBlock(block, text string) string {
	base := strings.TrimSpace(text)
	if base == "" {
		return block
	}
	return block + "\n\n" + base
}

// WeaveQuote 在用户文本前织入引用消息块（oneLine 抗注入）。
func WeaveQuote(text string, quoted *ContextMessage) string {
	if quoted == nil {
		return text
	}
	who := SanitizeContext(quoted.SenderName, 40, true)
	if who == "" {
		who = "某人"
	}
	body := SanitizeContext(quoted.Text, quoteMax, true)
	if body == "" {
		return text
	}
	block := "[用户引用了一条消息（来自 " + who + "）：\n" + body + "\n]"
	return prependBlock(block, text)
}

// WeaveThreadHistory 在用户文本前织入话题上文块（一行一条，时间升序）。
func WeaveThreadHistory(text string, msgs []ContextMessage) string {
	if len(msgs) == 0 {
		return text
	}
	var lines []string
	for _, m := range msgs {
		who := SanitizeContext(m.SenderName, 40, true)
		if who == "" {
			who = "某人"
		}
		body := SanitizeContext(m.Text, lineMax, true)
		if body != "" {
			lines = append(lines, who+"："+body)
		}
	}
	if len(lines) == 0 {
		return text
	}
	block := "[话题中在此之前已有的消息（按时间先后排列，供你理解上下文）：\n" + strings.Join(lines, "\n") + "\n]"
	return prependBlock(block, text)
}

// WeaveSender 在用户文本前织入发信人身份块。
func WeaveSender(text, senderID, senderName string) string {
	id := strings.TrimSpace(senderID)
	if id == "" {
		return text
	}
	who := SanitizeContext(senderName, senderNameMax, true)
	if who == "" {
		who = "某用户"
	}
	block := "[本条消息的发信人：" + who + "（open_id：" + id + "）]"
	return prependBlock(block, text)
}

// FilterHistorySince 把全量历史按 sinceTime 过滤为增量。
func FilterHistorySince(msgs []ContextMessage, sinceTime int64) []ContextMessage {
	if sinceTime <= 0 {
		return msgs
	}
	var out []ContextMessage
	for _, m := range msgs {
		if m.CreateTime > sinceTime {
			out = append(out, m)
		}
	}
	return out
}
