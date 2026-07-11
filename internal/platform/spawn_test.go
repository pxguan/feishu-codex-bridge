package platform

import (
	"strings"
	"testing"
)

// 通用 MergeEnv 契约（POSIX/Windows 都满足的基础行为）。

func envGet(env []string, key string) string {
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k == key {
			return v
		}
	}
	return ""
}

func TestMergeEnv_OverrideExisting(t *testing.T) {
	got := MergeEnv([]string{"PATH=/usr/bin", "HOME=/h"}, map[string]string{"PATH": "/x"})
	if envGet(got, "PATH") != "/x" {
		t.Fatalf("override of existing key lost: %v", got)
	}
	if envGet(got, "HOME") != "/h" {
		t.Fatalf("unrelated base key lost: %v", got)
	}
}

func TestMergeEnv_AddNew(t *testing.T) {
	got := MergeEnv([]string{"A=1"}, map[string]string{"B": "2"})
	if envGet(got, "B") != "2" {
		t.Fatalf("new key not added: %v", got)
	}
	if envGet(got, "A") != "1" {
		t.Fatalf("base key lost when adding: %v", got)
	}
}

func TestMergeEnv_NoOverridesKeepsBase(t *testing.T) {
	got := MergeEnv([]string{"A=1"}, nil)
	if envGet(got, "A") != "1" {
		t.Fatalf("base should be untouched: %v", got)
	}
}

func TestMergeEnv_OverrideToEmptyString(t *testing.T) {
	// 显式设空串是合法值（不是「未设置」）。
	got := MergeEnv([]string{"A=1"}, map[string]string{"A": ""})
	if v, ok := envLookup(got, "A"); !ok || v != "" {
		t.Fatalf("override to empty string lost: %v", got)
	}
}

func envLookup(env []string, key string) (string, bool) {
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k == key {
			return v, true
		}
	}
	return "", false
}
