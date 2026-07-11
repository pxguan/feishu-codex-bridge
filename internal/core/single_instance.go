package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// single_instance.go —— 进程级单实例锁（防同一 bot 被多进程同时 start）。
//
// 对齐 TS core/single-instance（见方案 §4）：
//   - pid 文件 + O_EXCL 原子创建；内容 {pid, appId, startedAt}（0600）。
//   - appId 维度逻辑锁：同一锁文件路径，按 appId 字段判定归属。
//   - pid 复用缓解：startedAt 记持有进程的 CreateTime；判定活体时若该 pid 当前
//     CreateTime 远晚于记录（>15s），视为复用的无关 pid，当死处理。
//   - 残留处理：自己 pid 残留→接管；死 pid→接管；活 pid→拒；损坏 JSON→fail-closed。
//   - 空文件（O_EXCL 抢到但 write 未完成的瞬态）：<2s 重试，超龄 unlink 重抢。

// 错误哨兵。
var (
	ErrAlreadyRunning  = errors.New("single-instance: already running")
	ErrHeldByOther     = errors.New("single-instance: held by another app")
	ErrCorruptLockFile = errors.New("single-instance: corrupt lock file")
	ErrAcquireTimeout  = errors.New("single-instance: acquire timeout")
)

const (
	defaultAcquireAttempts   = 5
	defaultAcquireRetryDelay = 200 * time.Millisecond
	pidReuseBuffer           = 15 * time.Second
	emptyLockStaleThreshold  = 2 * time.Second
)

type pidFile struct {
	PID       int    `json:"pid"`
	AppID     string `json:"appId"`
	StartedAt int64  `json:"startedAt"` // 持有进程 CreateTime（ms since epoch）
}

// Instance 表示一把已获取的锁。Release 归还（仅持有方 unlink）。
type Instance struct {
	path      string
	appID     string
	pid       int
	startedAt int64
	owns      bool
}

func (i *Instance) Path() string  { return i.path }
func (i *Instance) PID() int      { return i.pid }
func (i *Instance) AppID() string { return i.appID }

// Release 归还锁（仅当本实例持有时 unlink）；幂等。
func (i *Instance) Release() error {
	if i == nil || !i.owns {
		return nil
	}
	i.owns = false
	return os.Remove(i.path)
}

// ── 可注入配置（测试用）────────────────────────────────────────

type acquireConfig struct {
	attempts   int
	delay      time.Duration
	createTime func(pid int) (int64, error)
	probeLive  func(pid int, startedAt int64) (bool, error)
	now        func() time.Time
	sleep      func(time.Duration)
}

// AcquireOption 配置 AcquirePIDLock（主要供测试注入）。
type AcquireOption func(*acquireConfig)

func defaultAcquireConfig() acquireConfig {
	return acquireConfig{
		attempts:   defaultAcquireAttempts,
		delay:      defaultAcquireRetryDelay,
		createTime: gopsutilCreateTime,
		probeLive:  defaultProbeLive,
		now:        time.Now,
		sleep:      time.Sleep,
	}
}

// WithAcquireAttempts 覆盖重试次数。
func WithAcquireAttempts(n int) AcquireOption {
	return func(c *acquireConfig) {
		if n > 0 {
			c.attempts = n
		}
	}
}

// WithProbeLive 注入自定义 pid 活体判定（测试）。
func WithProbeLive(f func(pid int, startedAt int64) (bool, error)) AcquireOption {
	return func(c *acquireConfig) { c.probeLive = f }
}

// WithCreateTime 注入自定义 pid→CreateTime（测试）。
func WithCreateTime(f func(pid int) (int64, error)) AcquireOption {
	return func(c *acquireConfig) { c.createTime = f }
}

// AcquirePIDLock 抢占 pid 锁文件。appId 维度逻辑锁。
func AcquirePIDLock(path, appID string, opts ...AcquireOption) (*Instance, error) {
	cfg := defaultAcquireConfig()
	for _, o := range opts {
		o(&cfg)
	}
	pid := os.Getpid()
	ct, err := cfg.createTime(pid)
	if err != nil {
		ct = cfg.now().UnixMilli() // 探不到 CreateTime 退化用 now
	}

	for attempt := 0; attempt < cfg.attempts; attempt++ {
		// 1. O_EXCL 原子创建
		f, oerr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if oerr == nil {
			b, _ := json.Marshal(pidFile{PID: pid, AppID: appID, StartedAt: ct})
			if _, werr := f.Write(b); werr != nil {
				f.Close()
				_ = os.Remove(path)
				cfg.sleep(cfg.delay)
				continue
			}
			f.Close()
			return &Instance{path: path, appID: appID, pid: pid, startedAt: ct, owns: true}, nil
		}

		// 2. EEXIST → 读现有记录
		existing, empty, rerr := readPidFile(path)
		if rerr != nil {
			if errors.Is(rerr, errCorrupt) {
				return nil, fmt.Errorf("%w: %s", ErrCorruptLockFile, path)
			}
			// 文件被并发 unlink 或读失败 → 瞬态重试
			cfg.sleep(cfg.delay)
			continue
		}
		if empty {
			fi, _ := os.Stat(path)
			if fi != nil && cfg.now().Sub(fi.ModTime()) > emptyLockStaleThreshold {
				_ = os.Remove(path) // 超龄空锁：抢
			}
			cfg.sleep(cfg.delay)
			continue
		}

		// 3. 合法记录
		sameApp := existing.AppID == appID || existing.AppID == ""
		if !sameApp {
			// 其他 app：按活体
			live, _ := cfg.probeLive(existing.PID, existing.StartedAt)
			if live {
				return nil, ErrHeldByOther
			}
			if os.Remove(path) == nil {
				continue
			}
			cfg.sleep(cfg.delay)
			continue
		}
		// 同 app
		if existing.PID == pid {
			// 自己 pid 残留 → 接管（覆盖写）
			if overwriteLock(path, pidFile{PID: pid, AppID: appID, StartedAt: ct}) == nil {
				return &Instance{path: path, appID: appID, pid: pid, startedAt: ct, owns: true}, nil
			}
			cfg.sleep(cfg.delay)
			continue
		}
		live, _ := cfg.probeLive(existing.PID, existing.StartedAt)
		if !live {
			// 死 / pid 复用 → 接管
			if os.Remove(path) == nil {
				continue
			}
		}
		return nil, ErrAlreadyRunning
	}
	return nil, ErrAcquireTimeout
}

var errCorrupt = errors.New("corrupt pid file")

func readPidFile(path string) (rec pidFile, empty bool, err error) {
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return pidFile{}, false, errors.New("gone")
		}
		return pidFile{}, false, rerr
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return pidFile{}, true, nil
	}
	var p pidFile
	if jerr := json.Unmarshal(b, &p); jerr != nil {
		return pidFile{}, false, fmt.Errorf("%w: %v", errCorrupt, jerr)
	}
	return p, false, nil
}

func overwriteLock(path string, rec pidFile) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ── gopsutil 默认实现 ──────────────────────────────────────────

func gopsutilCreateTime(pid int) (int64, error) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, err
	}
	return p.CreateTime()
}

// defaultProbeLive 判定 pid 是否为「记录中的同一个活进程」。
//   - pid 不存在 → 死（可接管）。
//   - pid 当前 CreateTime 远晚于记录的 startedAt（>buffer）→ pid 被复用 → 当死。
//   - 否则 → 活（拒绝）。
func defaultProbeLive(pid int, startedAt int64) (bool, error) {
	exists, err := process.PidExists(int32(pid))
	if err != nil {
		return true, nil // 探不到保守当活，避免误抢
	}
	if !exists {
		return false, nil
	}
	p2, err := process.NewProcess(int32(pid))
	if err != nil {
		return true, nil
	}
	ct, err := p2.CreateTime()
	if err != nil {
		return true, nil
	}
	if ct-startedAt > pidReuseBuffer.Milliseconds() {
		return false, nil // pid 复用
	}
	return true, nil
}
