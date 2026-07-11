// daemon.go —— 后台 daemon 进程管理（Phase 2 service 层）。
//
// 模型：整个 `feishu-codex-bridge` 进程的「后台化」由 daemon 负责。
// `daemon start` 会以**脱离终端**的方式重新 exec 自身 `run` 子命令（日志重定向到
// ServiceLog / ServiceErrLog），并把主进程 pid 写进 DaemonPIDFile。
// `daemon stop` 按 pid 杀整棵进程树（含 run 拉起的 codex/claude 子进程）。
//
// 与 TS 端「每 bot 一个进程」不同，Go 版 run 是单进程跑活跃 bot，
// 所以 daemon 只需管一个主 pid。bot 维度的单实例由 core.AcquirePIDLock 负责。

package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/shirou/gopsutil/v3/process"
)

// 错误哨兵。
var (
	ErrAlreadyRunning = fmt.Errorf("daemon: already running")
	ErrNotRunning     = fmt.Errorf("daemon: not running")
)

const stopGrace = 5 * time.Second

// pidRecord 是 DaemonPIDFile 的内容。
type pidRecord struct {
	PID       int      `json:"pid"`
	StartedAt int64    `json:"startedAt"` // unix ms
	Args      []string `json:"args"`
	Self      string   `json:"self"`
}

// Info 是 Status() 的返回值。
type Info struct {
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"startedAt"`
	Uptime    string `json:"uptime"`
	Args      []string `json:"args"`
	Self      string `json:"self"`
}

// Manager 封装 daemon 的文件路径与可执行体。
type Manager struct {
	// PIDFile / LogFile / ErrLogFile 默认走 config 的预留路径；可覆盖（测试用）。
	PIDFile    string
	LogFile    string
	ErrLogFile string
	// Self 默认 os.Executable()；可覆盖（测试用）。
	Self string
	// ExtraArgs 是 start 时追加给 `run` 的参数。
	ExtraArgs []string
	// CommandArgs 若非 nil，则直接作为完整参数（覆盖默认的 ["run"]+ExtraArgs）。测试用。
	CommandArgs []string
}

// New 构造默认 Manager（路径取自 config）。
func New() *Manager {
	self, _ := os.Executable()
	return &Manager{
		PIDFile:    config.DaemonPIDFile(),
		LogFile:    config.ServiceLog(),
		ErrLogFile: config.ServiceErrLog(),
		Self:       self,
	}
}

// Start 启动后台 daemon。若已在运行则返回 ErrAlreadyRunning。
func (m *Manager) Start() error {
	info, _ := m.Status()
	if info.Running {
		return ErrAlreadyRunning
	}
	// 清掉可能残留的死 pid 文件。
	_ = os.Remove(m.PIDFile)

	if m.Self == "" {
		s, err := os.Executable()
		if err != nil {
			return fmt.Errorf("定位自身可执行文件失败：%w", err)
		}
		m.Self = s
	}

	if err := os.MkdirAll(filepath.Dir(m.LogFile), 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败：%w", err)
	}
	logF, err := os.OpenFile(m.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败：%w", err)
	}
	errF, err := os.OpenFile(m.ErrLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logF.Close()
		return fmt.Errorf("打开错误日志失败：%w", err)
	}

	var args []string
	if m.CommandArgs != nil {
		args = append([]string{}, m.CommandArgs...)
	} else {
		args = append([]string{"run"}, m.ExtraArgs...)
	}
	cmd := startCommand(m.Self, args)
	cmd.Stdout = logF
	cmd.Stderr = errF
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		logF.Close()
		errF.Close()
		return fmt.Errorf("启动后台进程失败：%w", err)
	}
	// 父进程不持有日志 fd（已交给子进程）；子进程独立会话，父退出不影响子。
	logF.Close()
	errF.Close()

	rec := pidRecord{
		PID:       cmd.Process.Pid,
		StartedAt: time.Now().UnixMilli(),
		Args:      args,
		Self:      m.Self,
	}
	if err := writePIDFile(m.PIDFile, rec); err != nil {
		// 启动已发生，仅 pid 文件写入失败：尽力 kill 回滚。
		killTree(cmd.Process.Pid, 0)
		return fmt.Errorf("写 pid 文件失败：%w", err)
	}
	return nil
}

// Stop 停止后台 daemon。幂等：未在运行直接返回 nil。
func (m *Manager) Stop() error {
	rec, err := readPIDFile(m.PIDFile)
	if err != nil {
		// 无 pid 文件 → 视为未运行。
		return nil
	}
	if !pidAlive(rec.PID) {
		_ = os.Remove(m.PIDFile)
		return nil
	}
	killTree(rec.PID, stopGrace)
	// 再确认一次是否真的退了。
	if pidAlive(rec.PID) {
		return fmt.Errorf("停止失败：pid %d 仍存活", rec.PID)
	}
	_ = os.Remove(m.PIDFile)
	return nil
}

// Restart 先停后起。
func (m *Manager) Restart() error {
	_ = m.Stop()
	// 给一点时间让端口/锁释放。
	time.Sleep(300 * time.Millisecond)
	return m.Start()
}

// Status 读取 pid 文件并判定是否存活。
func (m *Manager) Status() (Info, error) {
	rec, err := readPIDFile(m.PIDFile)
	if err != nil {
		return Info{Running: false}, nil
	}
	alive := pidAlive(rec.PID)
	info := Info{
		Running:   alive,
		PID:       rec.PID,
		StartedAt: rec.StartedAt,
		Args:      rec.Args,
		Self:      rec.Self,
	}
	if alive && rec.StartedAt > 0 {
		info.Uptime = time.Since(time.UnixMilli(rec.StartedAt)).Round(time.Second).String()
	}
	return info, nil
}

// Logs 返回日志文件最后 n 行（静态查看，不 follow）。
// follow 的持续跟踪请用 FollowLogs。
func (m *Manager) Logs(n int) ([]string, error) {
	return tailLines(m.LogFile, n)
}

// ── pid 文件 ───────────────────────────────────────────────────

func writePIDFile(path string, rec pidRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func readPIDFile(path string) (pidRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return pidRecord{}, err
	}
	var rec pidRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return pidRecord{}, err
	}
	if rec.PID <= 0 {
		return pidRecord{}, fmt.Errorf("pid 文件损坏")
	}
	return rec, nil
}

// pidAlive 跨平台判定进程是否存活。
func pidAlive(pid int) bool {
	exists, err := process.PidExists(int32(pid))
	if err != nil {
		return false
	}
	return exists
}
