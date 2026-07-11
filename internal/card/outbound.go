package card

// outbound.go —— 出站图片源解析 + 安全校验（对齐 TS card/outbound-images 的纯函数部分）。
// 飞书不渲染 markdown ![](…)，必须 im.v1.image.create 换 image_key。本模块负责：
// ① 从回复文本提取 ![](src) 源；② 本地路径 cwd 子树 + 扩展名白名单校验（防越界读）。
// 实际字节加载 + 上传（loadLocal stat/read + loadRemote fetch + uploadBuffer）依赖飞书 SDK，
// 在 feishu/card 包 port channel 后接上。

import (
	"path/filepath"
	"regexp"
	"strings"
)

// 上限与白名单（对齐 TS outbound-images 常量）。
const (
	MaxOutboundImages = 9
	MaxImageBytes     = 10 * 1024 * 1024
)

// AllowedImageExt im.v1.image.create 接受的格式。
var AllowedImageExt = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "webp": true,
	"gif": true, "tif": true, "tiff": true, "bmp": true, "ico": true,
}

// imgRe 匹配 ![alt](src)（src 可 <> 包裹、可带 title）。
var imgRe = regexp.MustCompile(`!\[([^\]]*)\]\(\s*(<[^>]+>|[^)\s]+)(?:\s+(?:"[^"]*"|'[^']*'))?\s*\)`)

// CleanSrc 剥可选 <> 包裹 + 首尾空白。
func CleanSrc(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// ImageSources 提取文本里全部 ![](src) 源（去重、保序）。
// feishu-card 围栏内是 markdown，对全文一次扫描即覆盖内联（run 卡）与 clean 卡图片。
func ImageSources(text string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range imgRe.FindAllStringSubmatch(text, -1) {
		if len(m) >= 3 {
			src := CleanSrc(m[2])
			if src != "" && !seen[src] {
				seen[src] = true
				out = append(out, src)
			}
		}
	}
	return out
}

// IsRemote http(s) 远程图源。
func IsRemote(src string) bool {
	l := strings.ToLower(src)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// ResolveLocalPath 解析本地图源为绝对路径 + 安全校验。
// 安全：仅 cwd 子树内的文件（防 agent 让 bot 上传 ~/.ssh 等越界文件）。
// 返回 abs（无论是否通过）+ ok（通过=在 cwd 子树 + 扩展名白名单）。
// 文件存在/大小校验是 IO（调用方 stat），本函数只做路径 + 扩展名校验。
func ResolveLocalPath(src, cwd string) (abs string, ok bool) {
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		cwdAbs = cwd
	}
	if filepath.IsAbs(src) {
		abs = filepath.Clean(src)
	} else {
		abs = filepath.Clean(filepath.Join(cwdAbs, src))
	}
	// 必须在 cwd 子树（或 == cwd 本身）。
	sep := string(filepath.Separator)
	if abs != cwdAbs && !strings.HasPrefix(abs, cwdAbs+sep) {
		return abs, false
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(abs), "."))
	if !AllowedImageExt[ext] {
		return abs, false
	}
	return abs, true
}
