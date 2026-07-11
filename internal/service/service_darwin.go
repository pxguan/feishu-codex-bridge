//go:build darwin

package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"text/template"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// pathEnvRe 从 plist 的 EnvironmentVariables.PATH 提取值。
var pathEnvRe = regexp.MustCompile(`(?s)<key>PATH</key>\s*<string>(.*?)</string>`)

// existingPathEnv 若 plist 已存在，返回其注入的 PATH（重装时复用，避免回归）。
func existingPathEnv() string {
	b, err := os.ReadFile(plistPath())
	if err != nil {
		return ""
	}
	m := pathEnvRe.FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func launchAgentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func plistPath() string {
	return filepath.Join(launchAgentsDir(), Label+".plist")
}

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.BinaryPath}}</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>{{.Stdout}}</string>
  <key>StandardErrorPath</key>
  <string>{{.Stderr}}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>{{.PathEnv}}</string>
  </dict>
</dict>
</plist>
`))

type plistData struct {
	Label, BinaryPath, Stdout, Stderr, PathEnv string
}

func renderPlist(opts Options) ([]byte, error) {
	d := plistData{
		Label:      Label,
		BinaryPath: opts.BinaryPath,
		Stdout:     config.ServiceLog(),
		Stderr:     config.ServiceErrLog(),
		PathEnv:    opts.PathEnv,
	}
	var buf bytes.Buffer
	if err := plistTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func platformInstall(opts Options) error {
	if err := os.MkdirAll(launchAgentsDir(), 0o755); err != nil {
		return err
	}
	p := plistPath()
	data, err := renderPlist(opts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	// 若已加载先 bootout，避免 label 冲突。
	_ = run("launchctl", "bootout", domain+"/"+Label)
	if err := run("launchctl", "bootstrap", domain, p); err != nil {
		return fmt.Errorf("launchctl bootstrap 失败：%w", err)
	}
	if err := run("launchctl", "kickstart", "-k", domain+"/"+Label); err != nil {
		return fmt.Errorf("launchctl kickstart 失败：%w", err)
	}
	return nil
}

func platformUninstall() error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = run("launchctl", "bootout", domain+"/"+Label) // 未加载也不报错
	if err := os.Remove(plistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// platformRestart 用 kickstart -k 重启已加载的 launchd 服务（进程会被替换）。
func platformRestart() error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if err := run("launchctl", "kickstart", "-k", domain+"/"+Label); err != nil {
		return fmt.Errorf("launchctl kickstart 失败：%w", err)
	}
	return nil
}

func platformStatus() Status {
	p := plistPath()
	st := Status{FilePath: p}
	if _, err := os.Stat(p); err == nil {
		st.Installed = true
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if err := exec.Command("launchctl", "print", domain+"/"+Label).Run(); err == nil {
		st.Loaded = true
	}
	switch {
	case st.Loaded:
		st.Note = "已安装并已加载（开机自启 + 崩溃自拉起）"
	case st.Installed:
		st.Note = "plist 存在但未加载"
	default:
		st.Note = "未安装"
	}
	return st
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
