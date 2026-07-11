package bot

// comments.go —— 云文档评论回复的纯函数（对齐 TS bot/comments）。
// stripMarkdown（剥标记）+ buildCommentPrompt（评论→prompt）+ elementsToText + 常量。
// 飞书 SDK 部分（resolveComment/fetchCommentContext/postCommentReply）后续 bot/feishu wrapper。

import (
	"os"
	"regexp"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// 支持的文档类型（drive.v1.fileComment.* 覆盖；slides/mindnote 等不支持）。
// bitable 已支持（评论事件为 drive.v1.fileComment；URL 段用 /base/）。
var SupportedCommentFileTypes = map[string]bool{
	"doc": true, "docx": true, "sheet": true, "bitable": true, "file": true,
}

// 评论回复最大字符（飞书拒超长评论）。
const ReplyMaxChars = 2000

// 文档 host（按 tenant）。
var docHosts = map[config.TenantBrand]string{
	config.TenantFeishu: "feishu.cn",
	config.TenantLark:   "larksuite.com",
}

// ResolvedTarget 评论所属文档。
type ResolvedTarget struct {
	FileToken string
	FileType  string // doc|docx|sheet|bitable|file
}

// fileTypeURLSegment 文档类型 → 飞书分享链接里的路径段（doc→docs / sheet→sheets / bitable→base）。
var fileTypeURLSegment = map[string]string{
	"doc":     "docs",
	"docx":    "docs",
	"sheet":   "sheets",
	"bitable": "base",
	"file":    "file",
	"wiki":    "wiki",
}

// fileTypeURLSegmentOf 取文档类型的 URL 路径段（未知类型回退原值，保证仍可用）。
func fileTypeURLSegmentOf(t string) string {
	if s, ok := fileTypeURLSegment[t]; ok {
		return s
	}
	return t
}

// CommentContext 评论上下文。
type CommentContext struct {
	Question      string
	Quote         string
	IsWhole       bool
	TargetReplyID string
}

// CommentReplyElement 评论回复内容元素。
type CommentReplyElement struct {
	Type    string `json:"type"`
	TextRun struct {
		Text string `json:"text"`
	} `json:"text_run,omitempty"`
	DocsLink struct {
		URL string `json:"url"`
	} `json:"docs_link,omitempty"`
}

// ElementsToText 评论元素 → 纯文本。
func ElementsToText(elements []CommentReplyElement) string {
	var sb strings.Builder
	for _, el := range elements {
		switch el.Type {
		case "text_run":
			sb.WriteString(el.TextRun.Text)
		case "docs_link":
			sb.WriteString(el.DocsLink.URL)
		}
	}
	return strings.TrimSpace(sb.String())
}

// DefaultCommentInstructions 评论提示词的默认人设（用户可在卡里编辑 / 直接改 master 文件）。
const DefaultCommentInstructions = `你是飞书云文档的评论助手。用简体中文、纯文本回答（不要 markdown 标记）。
- 直接给出答案，不要复述分析过程或「我现在去…」这类说明。
- 回答要简洁、可操作；涉及代码给可复制的最小片段。
- 若需要文档正文，可用 lark-cli 只读获取，但不要去发表 / 修改任何评论或文档。`

// BuildCommentPrompt 构造 codex prompt（云文档评论 @bot）。
// instructions 为用户自定义提示词（来自 master 文件，空则忽略）。
func BuildCommentPrompt(target ResolvedTarget, ctx CommentContext, tenant config.TenantBrand, instructions string) string {
	host := docHosts[tenant]
	if host == "" {
		host = "feishu.cn"
	}
	docURL := "https://" + host + "/" + fileTypeURLSegmentOf(target.FileType) + "/" + target.FileToken
	var parts []string
	parts = append(parts, "我在飞书云文档的评论里被 @了，需要你回答评论中的问题。文档信息：")
	parts = append(parts, "- 链接："+docURL)
	parts = append(parts, "- file_token："+target.FileToken)
	parts = append(parts, "- 类型："+target.FileType)
	scope := "行内评论（针对选中的文字）"
	if ctx.IsWhole {
		scope = "全文评论（针对整篇文档）"
	}
	parts = append(parts, "- 评论范围："+scope)
	if ctx.Quote != "" {
		parts = append(parts, "")
		quoted := strings.ReplaceAll(ctx.Quote, "\n", "\n> ")
		parts = append(parts, "用户选中的原文：\n> "+quoted)
	}
	parts = append(parts, "")
	parts = append(parts, "用户的问题："+ctx.Question)
	parts = append(parts, "")
	parts = append(parts,
		"如果回答需要文档正文内容，可用 lark-cli 只读地获取（仅用于读取，不要用它写任何东西）：\n"+
			"  lark-cli docs +fetch --doc "+target.FileToken+" --api-version v2")
	parts = append(parts, "")
	parts = append(parts, "【非常重要，务必遵守】")
	parts = append(parts,
		"1. 不要自己去发表 / 回复 / 修改任何飞书评论或文档（也不要用 lark-cli 或任何工具去发评论）——"+
			"系统会自动把你下面给出的最终回复发到这条评论里，你只管把答案写出来。")
	parts = append(parts, "2. 只输出要发给用户的「最终答案」本身，不要复述分析过程、步骤、或「我现在去…」这类说明。")
	parts = append(parts,
		"3. 用纯文本，不要用 markdown 标记（不要 ** __ # - * > ` 之类），不要代码块；"+
			"评论框不渲染 markdown，会原样显示这些符号。回答简洁直接。")
	if instructions != "" {
		parts = append(parts, "", "【用户自定义提示词】", instructions)
	}
	return strings.Join(parts, "\n")
}

// commentMasterPath 评论提示词 master 文件路径（bot 目录内）。
func commentMasterPath() string {
	return config.AppDir() + "/comment-instructions.md"
}

// readCommentInstructions 读取 master 提示词（缺失/读失败回退默认）。
func readCommentInstructions() string {
	b, err := os.ReadFile(commentMasterPath())
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return DefaultCommentInstructions
	}
	return strings.TrimSpace(string(b))
}

// writeCommentInstructions 写回 master 提示词（空→默认）。
func writeCommentInstructions(s string) error {
	if strings.TrimSpace(s) == "" {
		s = DefaultCommentInstructions
	}
	dir := config.AppDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(commentMasterPath(), []byte(s), 0o644)
}

// ── stripMarkdown ───────────────────────────────────────────────
// Go RE2 不支持 lookbehind/lookahead，单分隔符 * / _ 的精确保护规则简化。
// 核心：fence / heading / bold(** __) / inline-code / list / blockquote 剥离。

var (
	cmFenceLangRe = regexp.MustCompile("```[a-zA-Z]*\n?")
	cmFenceRe     = regexp.MustCompile("```")
	cmHeadingRe   = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	cmBoldRe      = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	cmBoldURe     = regexp.MustCompile(`__([^_]+)__`)
	cmInlineCode  = regexp.MustCompile("`([^`]+)`")
	cmListRe      = regexp.MustCompile(`(?m)^[-*]\s+`)
	cmQuoteRe     = regexp.MustCompile(`(?m)^>\s?`)
)

// StripMarkdown 剥 markdown 标记（评论框不渲染 markdown）。
func StripMarkdown(s string) string {
	s = cmFenceLangRe.ReplaceAllString(s, "")
	s = cmFenceRe.ReplaceAllString(s, "")
	s = cmHeadingRe.ReplaceAllString(s, "")
	s = cmBoldRe.ReplaceAllString(s, "$1")
	s = cmBoldURe.ReplaceAllString(s, "$1")
	s = cmInlineCode.ReplaceAllString(s, "$1")
	s = cmListRe.ReplaceAllString(s, "")
	s = cmQuoteRe.ReplaceAllString(s, "")
	return s
}
