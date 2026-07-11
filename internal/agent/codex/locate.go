package codex

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// locate.go —— codex CLI 二进制定位 + 版本探测（对齐 TS codex-appserver/locate，精简）。
//
// 优先级：$CODEX_BIN → PATH(codex) → macOS Codex.app 内置。
// 模块级缓存（成功才缓存；未找到/失败不缓存，用户装好后立即可见）。

var (
	binMu    sync.Mutex
	binCache string
	verCache sync.Map // bin → version
)

// ResolveCodexBin 解析 codex 二进制路径；找不到返回空字符串。force 强制重探。
func ResolveCodexBin(force bool) string {
	binMu.Lock()
	defer binMu.Unlock()
	if !force && binCache != "" && fileExists(binCache) {
		return binCache
	}
	binCache = locateBin()
	return binCache
}

func locateBin() string {
	if env := os.Getenv("CODEX_BIN"); env != "" && fileExists(env) {
		return env
	}
	if p, err := exec.LookPath("codex"); err == nil && p != "" {
		return p
	}
	if runtime.GOOS == "darwin" {
		const appBundle = "/Applications/Codex.app/Contents/Resources/codex"
		if fileExists(appBundle) {
			return appBundle
		}
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// CodexVersion spawn codex --version；失败返回空。force 绕过缓存。
func CodexVersion(bin string, force bool) string {
	if !force {
		if v, ok := verCache.Load(bin); ok {
			return v.(string)
		}
	}
	out, err := exec.Command(bin, "--version").Output()
	v := ""
	if err == nil {
		v = strings.TrimSpace(string(out))
	}
	if v != "" {
		verCache.Store(bin, v)
	}
	return v
}
