package core

import "testing"

func TestVersion_NonEmpty(t *testing.T) {
	// 契约：version 永远返回非空字符串（即便未 ldflags 注入也有开发态默认值）。
	v := Version()
	if v == "" {
		t.Fatal("Version() returned empty string; package-level version default must be non-empty")
	}
}

func TestVersion_StableAcrossCalls(t *testing.T) {
	// 契约：同一进程内 Version() 多次调用返回同一值（编译期常量语义）。
	a, b := Version(), Version()
	if a != b {
		t.Fatalf("Version() not stable: %q vs %q", a, b)
	}
}
