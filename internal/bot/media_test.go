package bot

import (
	"strings"
	"testing"
)

func TestCleanFileName_StripsPath(t *testing.T) {
	if got := CleanFileName("a/b/c.txt"); got != "c.txt" {
		t.Fatalf("should strip path: %q", got)
	}
	if got := CleanFileName("..\\secret.txt"); got != "secret.txt" {
		t.Fatalf("should strip windows path: %q", got)
	}
}

func TestCleanFileName_ReplacesControlChars(t *testing.T) {
	got := CleanFileName("a\nb<c>:d")
	if strings.Contains(got, "\n") || strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Fatalf("control/path chars should be replaced: %q", got)
	}
}

func TestCleanFileName_Empty(t *testing.T) {
	if CleanFileName("") != "" {
		t.Fatal("empty → empty")
	}
}

func TestCleanFileName_DotDot(t *testing.T) {
	if CleanFileName(".") != "" {
		t.Fatal(". → empty")
	}
	if CleanFileName("..") != "" {
		t.Fatal(".. → empty")
	}
}

func TestCleanFileName_CollapsesSpaces(t *testing.T) {
	if got := CleanFileName("a   b.txt"); got != "a b.txt" {
		t.Fatalf("collapse spaces: %q", got)
	}
}

func TestStripFileTokens(t *testing.T) {
	input := "before <file name=\"x\" key=\"k\"/> after"
	got := StripFileTokens(input)
	if strings.Contains(got, "<file") {
		t.Fatalf("file token should be stripped: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatal("surrounding text should be kept")
	}
}

func TestWeaveFileManifest_NoFiles(t *testing.T) {
	if got := WeaveFileManifest("text", nil); got != "text" {
		t.Fatalf("no files → stripped text: %q", got)
	}
}

func TestWeaveFileManifest_WithFiles(t *testing.T) {
	files := []InboundFile{
		{Path: "/tmp/a.log", Name: "a.log"},
		{Path: "/tmp/b.txt", Name: "b.txt"},
	}
	got := WeaveFileManifest("问题", files)
	if !strings.Contains(got, "问题") {
		t.Fatal("should keep user text")
	}
	if !strings.Contains(got, "a.log → /tmp/a.log") || !strings.Contains(got, "b.txt → /tmp/b.txt") {
		t.Fatalf("should list file manifest: %q", got)
	}
	if !strings.Contains(got, "2 个附件") {
		t.Fatal("should say 2 files")
	}
}

func TestImageKeysFromContent_ImageType(t *testing.T) {
	keys := ImageKeysFromContent("image", `{"image_key":"img_v3_abc"}`)
	if len(keys) != 1 || keys[0] != "img_v3_abc" {
		t.Fatalf("image type: %v", keys)
	}
}

func TestImageKeysFromContent_PostWalk(t *testing.T) {
	content := `{"zh_cn":{"content":[[{"tag":"img","image_key":"k1"},{"tag":"text","text":"hi"}]]}}`
	keys := ImageKeysFromContent("post", content)
	if len(keys) != 1 || keys[0] != "k1" {
		t.Fatalf("post walk: %v", keys)
	}
}

func TestImageKeysFromContent_BadJSON(t *testing.T) {
	if keys := ImageKeysFromContent("text", "not json"); keys != nil {
		t.Fatalf("bad JSON → nil: %v", keys)
	}
}

func TestSafeName(t *testing.T) {
	if got := SafeName("img_v3_abc/def"); !strings.Contains(got, "img_v3_abcdef") {
		t.Fatalf("safeName should keep alnum: %q", got)
	}
	// 特殊字符被剥。
	if strings.Contains(SafeName("img@#$"), "@") {
		t.Fatal("special chars should be stripped")
	}
	if SafeName("") != "img" {
		t.Fatal("empty → img fallback")
	}
}

func TestExtFromContentType(t *testing.T) {
	if ExtFromContentType("image/png") != "png" {
		t.Fatal("png")
	}
	if ExtFromContentType("image/jpeg; charset=binary") != "jpg" {
		t.Fatal("jpeg with params → jpg")
	}
	if ExtFromContentType("unknown") != "png" {
		t.Fatal("unknown → png default")
	}
}
