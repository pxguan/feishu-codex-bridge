//go:build linux

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

// pathEnvRe 从 systemd unit 的 Environment=PATH= 提取值。
var pathEnvRe = regexp.MustCompile(`(?m)^Environment=PATH=(.*)$`)

// existingPathEnv 若 unit 已存在，返回其注入的 PATH（重装时复用，避免回归）。
func existingPathEnv() string {
	b, err := os.ReadFile(unitPath())
	if err != nil {
		return ""
	}
	m := pathEnvRe.FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func systemdUserDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func unitPath() string {
	return filepath.Join(systemdUserDir(), Label+".service")
}

var unitTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=Feishu Codex Bridge bot daemon
After=network.target

[Service]
Type=simple
ExecStart={{.BinaryPath}} run
Restart=always
RestartSec=3
StandardOutput=append:{{.Stdout}}
StandardError=append:{{.Stderr}}
Environment=PATH={{.PathEnv}}

[Install]
WantedBy=default.target
`))

type unitData struct {
	BinaryPath, Stdout, Stderr, PathEnv string
}

func renderUnit(opts Options) ([]byte, error) {
	d := unitData{
		BinaryPath: opts.BinaryPath,
		Stdout:     config.ServiceLog(),
		Stderr:     config.ServiceErrLog(),
		PathEnv:    opts.PathEnv,
	}
	var buf bytes.Buffer
	if err := unitTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func platformInstall(opts Options) error {
	if err := os.MkdirAll(systemdUserDir(), 0o755); err != nil {
		return err
	}
	p := unitPath()
	data, err := renderUnit(opts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败：%w", err)
	}
	if err := run("systemctl", "--user", "enable", "--now", Label); err != nil {
		return fmt.Errorf("systemctl enable --now 失败：%w", err)
	}
	return nil
}

func platformUninstall() error {
	_ = run("systemctl", "--user", "disable", "--now", Label)
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// platformRestart 用 systemctl --user restart 重启已启用的 unit。
func platformRestart() error {
	if err := run("systemctl", "--user", "restart", Label); err != nil {
		return fmt.Errorf("systemctl restart 失败：%w", err)
	}
	return nil
}

func platformStatus() Status {
	p := unitPath()
	st := Status{FilePath: p}
	if _, err := os.Stat(p); err == nil {
		st.Installed = true
	}
	if err := exec.Command("systemctl", "--user", "is-enabled", Label).Run(); err == nil {
		st.Loaded = true
	}
	switch {
	case st.Loaded:
		st.Note = "已安装并已启用（开机自启 + 崩溃自拉起）"
	case st.Installed:
		st.Note = "unit 文件存在但未启用"
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
