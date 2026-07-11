// service.go —— 守护进程「一键安装/卸载为系统服务」。
//
// 把当前二进制注册为当前用户的常驻服务：
//   - macOS：写入 ~/Library/LaunchAgents/<Label>.plist，launchctl bootstrap+kickstart。
//   - Linux：写入 ~/.config/systemd/user/<Label>.service，systemctl --user enable --now。
// 平台相关实现在 service_darwin.go / service_linux.go / service_other.go（build tag 分发）。
//
// 这是 TS 版「Web 后端一键安装/卸载」在 Go 侧的等价物——Go 守护进程本身就是后端，
// 故「安装后端」=「把守护进程注册为系统服务」。TS 的 npm catalog 按需装依赖机制在 Go
// 侧无对应物（后端为内置/外部探测，无 npm 包），故不移植。
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// Label 是系统服务的唯一标识（launchd Label / systemd unit 名）。
const Label = "ai.feishu-codex-bridge.bot"

// ErrUnsupported 是当前平台不支持一键安装时的错误。
var ErrUnsupported = fmt.Errorf("当前平台不支持一键安装守护进程（仅 macOS / Linux 支持）")

// Options 控制服务文件生成。
type Options struct {
	BinaryPath string // 守护进程二进制绝对路径（默认 os.Executable()）
	LogDir     string // 日志目录（默认 config.AppDir()）
	PathEnv    string // 注入给守护进程的 PATH（默认当前进程 PATH）
}

// Status 描述服务安装/运行态。
type Status struct {
	Installed bool   `json:"installed"` // plist/unit 文件已存在
	Loaded    bool   `json:"loaded"`    // 已被 launchd/systemd 加载（开机自启/崩溃自拉起）
	FilePath  string `json:"file_path"` // 服务文件路径
	Note      string `json:"note"`      // 人类可读说明
}

// DefaultOptions 用当前进程信息填默认值。
func DefaultOptions() (Options, error) {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		return Options{}, fmt.Errorf("无法确定当前二进制路径：%w", err)
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return Options{}, err
	}
	// PATH：若已有服务文件（plist/unit），复用其注入的 PATH，避免重装后
	// 守护进程找不到 codex/claude 等后端；否则用当前 PATH + 常见 bin 目录充实。
	pathEnv := existingPathEnv()
	if pathEnv == "" {
		pathEnv = enrichPath(os.Getenv("PATH"))
	}
	return Options{
		BinaryPath: bin,
		LogDir:     config.AppDir(),
		PathEnv:    pathEnv,
	}, nil
}

// commonBinDirs 是重装时若无可复用 PATH 也要保证包含的常见二进制目录。
var commonBinDirs = []string{
	"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin",
	"/usr/sbin", "/sbin",
	"/opt/homebrew/opt/openjdk@17/bin",
}

// enrichPath 把常见 bin 目录并入 base（去重、保序）。
func enrichPath(base string) string {
	seen := map[string]bool{}
	var parts []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		parts = append(parts, p)
	}
	if base != "" {
		for _, p := range strings.Split(base, string(os.PathListSeparator)) {
			add(p)
		}
	}
	home, _ := os.UserHomeDir()
	dirs := append([]string{}, commonBinDirs...)
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".bun", "bin"),
		)
	}
	for _, p := range dirs {
		add(p)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func validateOpts(o Options) error {
	if o.BinaryPath == "" {
		return fmt.Errorf("BinaryPath 为空")
	}
	if !filepath.IsAbs(o.BinaryPath) {
		return fmt.Errorf("BinaryPath 必须是绝对路径：%s", o.BinaryPath)
	}
	if fi, err := os.Stat(o.BinaryPath); err != nil || fi.IsDir() {
		return fmt.Errorf("BinaryPath 不是可执行的文件：%s", o.BinaryPath)
	}
	if o.LogDir == "" {
		return fmt.Errorf("LogDir 为空")
	}
	if err := os.MkdirAll(o.LogDir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败：%w", err)
	}
	return nil
}

// Install 把守护进程注册为当前用户的系统服务并启动。
// opts 为零值时自动用 DefaultOptions()。
func Install(opts Options) error {
	if opts.BinaryPath == "" {
		d, err := DefaultOptions()
		if err != nil {
			return err
		}
		opts = d
	}
	if err := validateOpts(opts); err != nil {
		return err
	}
	return platformInstall(opts)
}

// Uninstall 注销系统服务并停止守护进程。
func Uninstall() error {
	return platformUninstall()
}

// Restart 重启已安装的系统服务（不动 plist/unit 文件）。
// 无系统服务可重启（未安装 / 当前平台不支持）时返回 error。
// 调用方（bot 的 🔁 重启 handler）应在回发「正在重启」卡之后再触发，
// 因为本进程会被它自己 kick 掉。
func Restart() error {
	return platformRestart()
}

// GetStatus 报告安装/运行态。
func GetStatus() (Status, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return platformStatus(), ErrUnsupported
	}
	return platformStatus(), nil
}
