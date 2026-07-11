package utils

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
)

// openurl.go —— best-effort 跨平台开浏览器（对齐 TS utils/open-url）。
//
// 仅在 TTY 时尝试（detached daemon 无用户在场，不该弹浏览器）；
// 失败静默吞，调用方总会同时打印 URL 供手动打开。

// OpenURL 尝试用默认浏览器打开 url；非 TTY 或启动失败返回 false。
func OpenURL(u string) bool {
	if !stdinIsTTY() {
		return false
	}
	cmd, args := openCommand(u)
	c := exec.Command(cmd, args...)
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	if err := c.Start(); err != nil {
		return false
	}
	// detached：后台 Wait 回收，不阻塞调用方。
	go func() { _ = c.Wait() }()
	return true
}

func openCommand(u string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{u}
	case "windows":
		// start 是 cmd 内建；空 "" 是其（被忽略的）窗口标题。
		return "cmd", []string{"/c", "start", "", u}
	default:
		return "xdg-open", []string{u}
	}
}

// stdinIsTTY 判断 stdin 是否终端（用字符设备近似，不加 term 依赖）。
// 包级变量便于测试替换。
var stdinIsTTY = func() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// stringsNewRegexp 包装 regexp.Compile（eventdiagnosis 用）。
func stringsNewRegexp(pattern string) *regexp.Regexp {
	r, err := regexp.Compile(pattern)
	if err != nil {
		return regexp.MustCompile(`$^`) // 永不匹配的兜底
	}
	return r
}
