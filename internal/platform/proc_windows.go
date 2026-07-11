//go:build windows

package platform

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// proc_windows.go —— Windows 进程组 + env 合并（CREATE_NO_WINDOW + taskkill /T）。

// CREATE_NO_WINDOW 在新进程中不创建控制台窗口（Go syscall 未导出该常量）。
const _CREATE_NO_WINDOW = 0x08000000

func envEnviron() []string { return os.Environ() }

func applySysProcAttr(cmd *exec.Cmd, opts SpawnOptions) {
	flags := uint32(0)
	if opts.WindowsHide {
		flags |= _CREATE_NO_WINDOW
	}
	if opts.Detached {
		flags |= syscall.CREATE_NEW_PROCESS_GROUP
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
}

func killGroup(pid int, _ syscall.Signal) error {
	// Windows 用 taskkill /T /F 杀整树（POSIX 信号收不回孙进程）。
	c := exec.Command("taskkill", "/T", "/F", "/PID", itoa(pid))
	return c.Run()
}

func sigTerm() syscall.Signal { return 0 } // Windows 不用信号
func sigKill() syscall.Signal { return 0 }

// MergeEnv Windows：env key 大小写不敏感去重（Path≡PATH），保留首次出现的大小写。
func MergeEnv(base []string, overrides map[string]string) []string {
	type entry struct{ key, val string }
	m := map[string]*entry{} // lower(key) → entry
	order := []string{}
	addOrUpdate := func(key, val string) {
		lk := strings.ToLower(key)
		if e, ok := m[lk]; ok {
			e.val = val
			return
		}
		e := &entry{key: key, val: val}
		m[lk] = e
		order = append(order, lk)
	}
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		addOrUpdate(k, v)
	}
	for k, v := range overrides {
		addOrUpdate(k, v)
	}
	out := make([]string, 0, len(order))
	for _, lk := range order {
		e := m[lk]
		out = append(out, e.key+"="+e.val)
	}
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }
