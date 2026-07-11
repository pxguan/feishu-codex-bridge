package card

import (
	"path/filepath"
	"testing"
)

func TestImageSources_DedupOrder(t *testing.T) {
	text := "before ![a](p1.png) mid ![](p2.jpg) repeat ![b](p1.png) <![x](<sp ace.png>)"
	srcs := ImageSources(text)
	// 去重 p1.png + 顺序保留 + <> 包裹剥。
	want := []string{"p1.png", "p2.jpg", "sp ace.png"}
	if len(srcs) != len(want) {
		t.Fatalf("srcs=%v want %v", srcs, want)
	}
	for i, w := range want {
		if srcs[i] != w {
			t.Errorf("srcs[%d]=%q want %q", i, srcs[i], w)
		}
	}
}

func TestImageSources_TitleInSrc(t *testing.T) {
	// ![](src "title") 形式。
	srcs := ImageSources(`![](img.png "scale")`)
	if len(srcs) != 1 || srcs[0] != "img.png" {
		t.Fatalf("title form wrong: %v", srcs)
	}
}

func TestCleanSrc(t *testing.T) {
	if CleanSrc(" <a.png> ") != "a.png" {
		t.Fatal("cleanSrc <> strip")
	}
	if CleanSrc("a.png") != "a.png" {
		t.Fatal("cleanSrc plain")
	}
}

func TestIsRemote(t *testing.T) {
	if !IsRemote("https://x/a.png") || !IsRemote("http://y/b.jpg") {
		t.Fatal("http(s) should be remote")
	}
	if IsRemote("/local/a.png") || IsRemote("ftp://x") {
		t.Fatal("non-http should not be remote")
	}
}

func TestResolveLocalPath_RelativeInsideCwd(t *testing.T) {
	cwd := t.TempDir()
	abs, ok := ResolveLocalPath("img/a.png", cwd)
	if !ok {
		t.Fatal("relative inside cwd should pass")
	}
	if abs != filepath.Clean(filepath.Join(cwd, "img/a.png")) {
		t.Fatalf("abs wrong: %q", abs)
	}
}

func TestResolveLocalPath_AbsoluteInsideCwd(t *testing.T) {
	cwd := t.TempDir()
	abs, ok := ResolveLocalPath(filepath.Join(cwd, "sub/b.jpg"), cwd)
	if !ok {
		t.Fatalf("absolute inside cwd should pass: %q", abs)
	}
}

func TestResolveLocalPath_OutsideCwdRejected(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	// 绝对路径在 cwd 外 → 拒（防 ~/.ssh 等越界）。
	_, ok := ResolveLocalPath(filepath.Join(other, "secret.png"), cwd)
	if ok {
		t.Fatal("path outside cwd must be rejected")
	}
	// 相对路径 ../escape.png → resolve 到 cwd 外 → 拒。
	_, ok = ResolveLocalPath("../escape.png", cwd)
	if ok {
		t.Fatal("../escape must be rejected")
	}
}

func TestResolveLocalPath_BadExtRejected(t *testing.T) {
	cwd := t.TempDir()
	_, ok := ResolveLocalPath("a.txt", cwd)
	if ok {
		t.Fatal(".txt must be rejected (not image ext)")
	}
	_, ok = ResolveLocalPath("a.sh", cwd)
	if ok {
		t.Fatal(".sh must be rejected")
	}
}

func TestResolveLocalPath_AllExts(t *testing.T) {
	cwd := t.TempDir()
	for _, ext := range []string{"png", "jpg", "jpeg", "webp", "gif", "tif", "tiff", "bmp", "ico"} {
		if _, ok := ResolveLocalPath("a."+ext, cwd); !ok {
			t.Errorf("allowed ext .%s rejected", ext)
		}
	}
}
