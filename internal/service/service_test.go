package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultOptions(t *testing.T) {
	o, err := DefaultOptions()
	if err != nil {
		t.Fatalf("DefaultOptions: %v", err)
	}
	if !filepath.IsAbs(o.BinaryPath) {
		t.Fatalf("BinaryPath 应为绝对路径：%s", o.BinaryPath)
	}
	if _, err := os.Stat(o.BinaryPath); err != nil {
		t.Fatalf("BinaryPath 应存在：%v", err)
	}
	if o.LogDir == "" {
		t.Fatal("LogDir 不应为空")
	}
	if o.PathEnv == "" {
		t.Fatal("PathEnv 不应为空")
	}
}

func TestValidateOpts(t *testing.T) {
	o, _ := DefaultOptions()
	if err := validateOpts(o); err != nil {
		t.Fatalf("合法 Options 应通过：%v", err)
	}
	bad := o
	bad.BinaryPath = ""
	if err := validateOpts(bad); err == nil {
		t.Fatal("空 BinaryPath 应报错")
	}
	bad2 := o
	bad2.BinaryPath = "/nonexistent/binary"
	if err := validateOpts(bad2); err == nil {
		t.Fatal("不存在的 BinaryPath 应报错")
	}
	bad3 := o
	bad3.BinaryPath = "."
	if err := validateOpts(bad3); err == nil {
		t.Fatal("目录 BinaryPath 应报错")
	}
}

func TestUnsupportedPlatformNoPanic(t *testing.T) {
	// service.Other 平台会返回 ErrUnsupported；在支持平台上仅确保 Status() 不 panic。
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if err := Install(Options{}); err == nil {
			t.Fatal("不支持平台 Install 应返回错误")
		}
		return
	}
	st, err := GetStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	_ = st
}
