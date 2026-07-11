package bot

// media.go —— 入站图片/文件处理的纯函数（对齐 TS bot/media）。
// cleanFileName（安全文件名）+ stripFileTokens（剥占位符）+ weaveFileManifest（文件清单织入 prompt）
// + imageKeysFromContent（提取 image_key）+ 常量。
// 飞书 SDK 部分（collectInboundImages/collectInboundFiles/downloadOne）后续 bot/feishu wrapper。

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	MediaMaxImages    = 9
	MediaMaxFiles     = 9
	MediaMaxFileBytes = 50 * 1024 * 1024
	MediaTTLMS        = 60 * 60 * 1000
)

// ExtByContentType content-type → 扩展名。
var ExtByContentType = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/jpg":  "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
	"image/bmp":  "bmp",
	"image/heic": "heic",
	"image/heif": "heif",
	"image/tiff": "tiff",
}

// InboundFile 下载的入站文件（绝对路径 + 原始安全名）。
type InboundFile struct {
	Path string
	Name string
}

// CleanFileName 安全文件名：剥路径段 + 替换控制/路径字符 + 折叠空白 + clamp 100。
// 唯一 sanitize 边界（uplink-controlled filename → 本地文件名 + prompt 文本）。
func CleanFileName(name string) string {
	if name == "" {
		return ""
	}
	// 剥路径段（防 ../ 逃逸）。
	segments := strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' })
	base := name
	if len(segments) > 0 {
		base = segments[len(segments)-1]
	}
	// 替换控制/路径字符。
	var sb strings.Builder
	for _, r := range base {
		if r < 0x20 || r == '<' || r == '>' || r == ':' || r == '"' || r == '|' || r == '?' || r == '*' {
			sb.WriteRune('_')
		} else if r == ' ' || r == '\t' {
			sb.WriteRune(' ')
		} else {
			sb.WriteRune(r)
		}
	}
	cleaned := collapseSpaces(sb.String())
	cleaned = strings.TrimSpace(cleaned)
	runes := []rune(cleaned)
	if len(runes) > 100 {
		cleaned = string(runes[:100])
	}
	if cleaned == "." || cleaned == ".." {
		return ""
	}
	return cleaned
}

func collapseSpaces(s string) string {
	out := strings.Builder{}
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				out.WriteRune(' ')
			}
			prevSpace = true
		} else {
			out.WriteRune(r)
			prevSpace = false
		}
	}
	return out.String()
}

var (
	fileTokenRe = regexp.MustCompile(`<file\b[^<]*\/>`)
	trailSpace  = regexp.MustCompile(`[ \t]+\n`)
)

// StripFileTokens 剥 <file .../> 占位符（codex 不能 act on file_key）。
func StripFileTokens(text string) string {
	s := fileTokenRe.ReplaceAllString(text, "")
	s = trailSpace.ReplaceAllString(s, "\n")
	return strings.TrimSpace(s)
}

// WeaveFileManifest 把下载的附件清单织入用户 prompt（codex 用 shell/read 工具按路径打开）。
func WeaveFileManifest(text string, files []InboundFile) string {
	stripped := StripFileTokens(text)
	if len(files) == 0 {
		return stripped
	}
	var lines []string
	for _, f := range files {
		lines = append(lines, "- "+f.Name+" → "+f.Path)
	}
	head := ""
	if stripped != "" {
		head = stripped + "\n\n"
	}
	return head + "[用户上传了 " + itoaLen(files) + " 个附件，已保存到本地，可用 shell / 读取工具按下面的绝对路径直接打开：\n" + strings.Join(lines, "\n") + "\n]"
}

func itoaLen(files []InboundFile) string {
	n := len(files)
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for n > 0 {
		p--
		buf[p] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[p:])
}

// ImageKeysFromContent 从消息内容提取 image_key（image type 直接取；post/walk 递归找 tag:img）。
func ImageKeysFromContent(msgType, content string) []string {
	if content == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil
	}
	if msgType == "image" {
		if m, ok := parsed.(map[string]any); ok {
			if key, ok := m["image_key"].(string); ok && key != "" {
				return []string{key}
			}
		}
		return nil
	}
	var keys []string
	walkForImageKeys(parsed, &keys)
	return keys
}

func walkForImageKeys(node any, out *[]string) {
	switch v := node.(type) {
	case map[string]any:
		if tag, _ := v["tag"].(string); tag == "img" {
			if key, ok := v["image_key"].(string); ok && key != "" {
				*out = append(*out, key)
			}
		}
		for _, child := range v {
			walkForImageKeys(child, out)
		}
	case []any:
		for _, child := range v {
			walkForImageKeys(child, out)
		}
	}
}

// SafeName fileKey → 安全文件名（仅 [a-zA-Z0-9_-]，clamp 40）。
func SafeName(fileKey string) string {
	var sb strings.Builder
	for _, r := range fileKey {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		}
	}
	s := sb.String()
	if len(s) > 40 {
		s = s[len(s)-40:]
	}
	if s == "" {
		return "img"
	}
	return s
}

// ExtFromContentType 从 content-type 提取扩展名（默认 png）。
func ExtFromContentType(contentType string) string {
	base := strings.SplitN(contentType, ";", 2)[0]
	base = strings.TrimSpace(strings.ToLower(base))
	if ext, ok := ExtByContentType[base]; ok {
		return ext
	}
	return "png"
}
