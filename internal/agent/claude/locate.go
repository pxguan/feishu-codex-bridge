package claude

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// locate.go —— claude CLI 二进制定位 + 版本探测（对齐 codex/locate.go）。
//
// 优先级：$CLAUDE_BIN → PATH(claude) → 常见安装位置 → npm 全局前缀。
// 模块级缓存（成功才缓存；未找到/失败不缓存，用户装好后立即可见）。

var (
	binMu    sync.Mutex
	binCache string
	verCache sync.Map // bin → version
)

// ResolveClaudeBin 解析 claude 二进制路径；找不到返回空字符串。force 强制重探。
func ResolveClaudeBin(force bool) string {
	binMu.Lock()
	defer binMu.Unlock()
	if !force && binCache != "" && fileExists(binCache) {
		return binCache
	}
	binCache = locateBin()
	return binCache
}

func locateBin() string {
	if env := os.Getenv("CLAUDE_BIN"); env != "" && fileExists(env) {
		return env
	}
	if p, err := exec.LookPath("claude"); err == nil && p != "" {
		return p
	}
	candidates := []string{
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
		"/usr/bin/claude",
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	if prefix := npmGlobalBin(); prefix != "" {
		c := prefix + "/claude"
		if fileExists(c) {
			return c
		}
	}
	return ""
}

func npmGlobalBin() string {
	out, err := exec.Command("npm", "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return ""
	}
	return dir + "/bin"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ClaudeVersion spawn claude --version；失败返回空。force 绕过缓存。
func ClaudeVersion(bin string, force bool) string {
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

// HomeDir 返回用户主目录。
func HomeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}
