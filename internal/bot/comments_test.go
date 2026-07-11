package bot

import (
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

func TestSupportedFileTypes(t *testing.T) {
	for _, ft := range []string{"doc", "docx", "sheet", "bitable", "file"} {
		if !SupportedCommentFileTypes[ft] {
			t.Errorf("%s should be supported", ft)
		}
	}
	if SupportedCommentFileTypes["slides"] {
		t.Fatal("slides should NOT be supported")
	}
}

func TestElementsToText(t *testing.T) {
	els := []CommentReplyElement{
		{Type: "text_run"},
		{Type: "docs_link"},
	}
	els[0].TextRun.Text = "hello "
	els[1].DocsLink.URL = "https://doc.x"
	if got := ElementsToText(els); got != "hello https://doc.x" {
		t.Fatalf("elementsToText: %q", got)
	}
}

func TestBuildCommentPrompt(t *testing.T) {
	target := ResolvedTarget{FileToken: "tok1", FileType: "docx"}
	ctx := CommentContext{Question: "这段怎么用？", Quote: "选中的原文", IsWhole: false}
	prompt := BuildCommentPrompt(target, ctx, config.TenantFeishu, "")
	for _, want := range []string{"tok1", "docx", "这段怎么用？", "选中的原文", "lark-cli", "非常重要"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if !strings.Contains(prompt, "行内评论") {
		t.Fatal("inline comment should say 行内评论")
	}
	// whole-doc 标签。
	ctx2 := CommentContext{Question: "Q", IsWhole: true}
	prompt2 := BuildCommentPrompt(target, ctx2, config.TenantFeishu, "")
	if !strings.Contains(prompt2, "全文评论") {
		t.Fatal("whole comment should say 全文评论")
	}
}

func TestStripMarkdown_Bold(t *testing.T) {
	if got := StripMarkdown("**bold** text"); got != "bold text" {
		t.Fatalf("bold: %q", got)
	}
	if got := StripMarkdown("__bold__ text"); got != "bold text" {
		t.Fatalf("bold __: %q", got)
	}
}

func TestStripMarkdown_Heading(t *testing.T) {
	if got := StripMarkdown("# Title\nbody"); !strings.Contains(got, "Title") || strings.Contains(got, "#") {
		t.Fatalf("heading: %q", got)
	}
}

func TestStripMarkdown_InlineCode(t *testing.T) {
	if got := StripMarkdown("use `code` here"); got != "use code here" {
		t.Fatalf("inline code: %q", got)
	}
}

func TestStripMarkdown_FencedCode(t *testing.T) {
	input := "before\n```go\nfmt.Println()\n```\nafter"
	got := StripMarkdown(input)
	if strings.Contains(got, "```") {
		t.Fatalf("fence should be stripped: %q", got)
	}
	if !strings.Contains(got, "fmt.Println()") {
		t.Fatal("fence body should be kept")
	}
}

func TestStripMarkdown_ListAndQuote(t *testing.T) {
	if got := StripMarkdown("- item"); strings.TrimSpace(got) != "item" {
		t.Fatalf("list: %q", got)
	}
	if got := StripMarkdown("> quote"); strings.TrimSpace(got) != "quote" {
		t.Fatalf("quote: %q", got)
	}
}
