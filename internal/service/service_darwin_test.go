//go:build darwin

package service

import (
	"strings"
	"testing"
)

func TestRenderPlist(t *testing.T) {
	o, _ := DefaultOptions()
	data, err := renderPlist(o)
	if err != nil {
		t.Fatalf("renderPlist: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<string>" + o.BinaryPath + "</string>",
		"<string>run</string>",
		"KeepAlive",
		"StandardOutPath",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("plist 缺少 %q\n---\n%s", want, s)
		}
	}
}
