//go:build linux

package service

import (
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	o, _ := DefaultOptions()
	data, err := renderUnit(o)
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		"[Unit]",
		"[Service]",
		"ExecStart=" + o.BinaryPath + " run",
		"Restart=always",
		"[Install]",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("unit 缺少 %q\n---\n%s", want, s)
		}
	}
}
