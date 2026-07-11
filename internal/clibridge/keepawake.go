package clibridge

// keepawake.go —— 离开保活控制器（对齐 TS cli-bridge/keep-awake.ts）。
// 离开且本机有任务在跑时，用 caffeinate -i 顶住系统休眠（屏幕照常熄灭），
// 回到电脑/解锁即释放。引用计数：多个并发等待共享一个 caffeinate 进程。

import (
	"os"
	"os/exec"
	"runtime"
)

// KeepAwakeProcess 被控制器驱动的进程切片（可注入 fake 便于测试）。
type KeepAwakeProcess interface {
	Kill() error
}

// spawnCaffeinate 在 macOS 上 spawn `caffeinate -i -w <pid>`：
//   - -i 仅防系统休眠（屏幕可熄），不强制亮屏；
//   - -w 绑定到本进程，daemon 崩溃未清理时 caffeinate 自行退出。
// 非 macOS / spawn 失败 → 返回 nil（no-op）。
func spawnCaffeinate() KeepAwakeProcess {
	if runtime.GOOS != "darwin" {
		return nil
	}
	cmd := exec.Command("/usr/bin/caffeinate", "-i", "-w", itoaPID(os.Getpid()))
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil
	}
	return &osProcess{p: cmd}
}

type osProcess struct{ p *exec.Cmd }

func (o *osProcess) Kill() error {
	if o.p == nil || o.p.Process == nil {
		return nil
	}
	return o.p.Process.Kill()
}

// KeepAwakeController 引用计数的保活控制器。
type KeepAwakeController interface {
	Acquire()
	Release()
	Shutdown()
	IsActive() bool
}

type keepAwakeController struct {
	enabled    func() bool
	spawn      func() KeepAwakeProcess
	count      int
	proc       KeepAwakeProcess
}

// CreateKeepAwakeController 构造。enabled 每次 Acquire 时读（支持运行时开关）；
// spawn 默认 spawnCaffeinate（可注入 fake）。
func CreateKeepAwakeController(enabled func() bool, spawn func() KeepAwakeProcess) KeepAwakeController {
	if enabled == nil {
		enabled = func() bool { return true }
	}
	if spawn == nil {
		spawn = spawnCaffeinate
	}
	return &keepAwakeController{enabled: enabled, spawn: spawn}
}

func (k *keepAwakeController) Acquire() {
	k.count++
	if k.proc == nil && k.enabled() {
		k.proc = k.spawn()
	}
}

func (k *keepAwakeController) Release() {
	if k.count == 0 {
		return
	}
	k.count--
	if k.count == 0 {
		k.stop()
	}
}

func (k *keepAwakeController) Shutdown() {
	k.count = 0
	k.stop()
}

func (k *keepAwakeController) stop() {
	if k.proc == nil {
		return
	}
	_ = k.proc.Kill()
	k.proc = nil
}

func (k *keepAwakeController) IsActive() bool {
	return k.proc != nil
}

func itoaPID(n int) string {
	// 小整数转字符串，避免再引 strconv。
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
