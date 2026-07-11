package clibridge

// presence.go —— 离开检测（对齐 TS cli-bridge/presence.ts）。
// 通过 ioreg（macOS）读 HIDIdleTime + 锁屏，判定「人走了」；
// Windows 走 PowerShell GetLastInputInfo（尽力，失败开转发）。
// 不在 macOS/Windows 上读不到 → presence_failed（Unix 失败关转发 / win32 失败开转发）。

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// CliPresenceRoute 路由判定。
type CliPresenceRoute struct {
	RouteToFeishu bool
	Reason        string // always|local_active|away|presence_failed|disabled
}

// CliLocalActivity 本地活跃判定。
type CliLocalActivity struct {
	LocalActive bool
	Reason      string // local_active|away|presence_failed|disabled
}

// idle 读缓存窗口：一次 hook 突发 + 路由/本机回归双查在毫秒内，合并为一次 ioreg。
const idleReadTTLMs = 2000

var (
	idleCache  *struct{ seconds int; at int64 }
	lockCache  *struct{ locked bool; at int64 }
)

func readMacIdleSeconds(ctx context.Context) (int, error) {
	now := time.Now().UnixMilli()
	if idleCache != nil && now-idleCache.at < idleReadTTLMs {
		return idleCache.seconds, nil
	}
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return 0, err
	}
	m := regexp.MustCompile(`HIDIdleTime"\s*=\s*(\d+)`).FindSubmatch(out)
	seconds := 0
	if m != nil {
		ns, _ := strconv.ParseInt(string(m[1]), 10, 64)
		seconds = int(ns / 1_000_000_000)
	}
	idleCache = &struct{ seconds int; at int64 }{seconds: seconds, at: now}
	return seconds, nil
}

// ParseScreenLocked 从 ioreg 输出判定锁屏（CGSSessionScreenIsLocked = Yes）。
func ParseScreenLocked(ioregStdout string) bool {
	return regexp.MustCompile(`CGSSessionScreenIsLocked"?\s*=\s*Yes`).MatchString(ioregStdout)
}

func readMacScreenLocked(ctx context.Context) (bool, error) {
	now := time.Now().UnixMilli()
	if lockCache != nil && now-lockCache.at < idleReadTTLMs {
		return lockCache.locked, nil
	}
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-n", "Root", "-d1", "-k", "IOConsoleUsers").Output()
	if err != nil {
		return false, err
	}
	locked := ParseScreenLocked(string(out))
	lockCache = &struct{ locked bool; at int64 }{locked: locked, at: now}
	return locked, nil
}

func readWindowsIdleSeconds(ctx context.Context) (int, error) {
	now := time.Now().UnixMilli()
	if idleCache != nil && now-idleCache.at < idleReadTTLMs {
		return idleCache.seconds, nil
	}
	script := []string{
		"Add-Type @'",
		"using System;",
		"using System.Runtime.InteropServices;",
		"public class A2LIdle {",
		"  [StructLayout(LayoutKind.Sequential)] struct LII { public uint cbSize; public uint dwTime; }",
		"  [DllImport(\"user32.dll\")] static extern bool GetLastInputInfo(ref LII p);",
		"  public static uint Ms() { LII l = new LII(); l.cbSize = (uint)Marshal.SizeOf(l); GetLastInputInfo(ref l); return ((uint)Environment.TickCount) - l.dwTime; }",
		"}",
		"'@",
		"if (Get-Process LogonUI -ErrorAction SilentlyContinue) { 0x7FFFFFFF } else { [A2LIdle]::Ms() }",
	}
	encoded := encodeBase64(strings.Join(script, "\n"))
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded).Output()
	if err != nil {
		return 0, err
	}
	ms, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	seconds := int(ms / 1000)
	idleCache = &struct{ seconds int; at int64 }{seconds: seconds, at: now}
	return seconds, nil
}

// ResolveCliPresenceRoute 综合本地活跃 → 路由到飞书与否。
func ResolveCliPresenceRoute(prefs config.ResolvedCliBridgePreferences) (CliPresenceRoute, error) {
	activity, err := ResolveCliLocalActivity(prefs)
	if err != nil {
		if runtime.GOOS == "windows" && activity.Reason == "presence_failed" {
			return CliPresenceRoute{RouteToFeishu: true, Reason: "presence_failed"}, nil
		}
		return CliPresenceRoute{RouteToFeishu: false, Reason: activity.Reason}, nil
	}
	if activity.LocalActive {
		return CliPresenceRoute{RouteToFeishu: false, Reason: "local_active"}, nil
	}
	if activity.Reason == "away" {
		return CliPresenceRoute{RouteToFeishu: true, Reason: "away"}, nil
	}
	if activity.Reason == "presence_failed" && runtime.GOOS == "windows" {
		return CliPresenceRoute{RouteToFeishu: true, Reason: "presence_failed"}, nil
	}
	return CliPresenceRoute{RouteToFeishu: false, Reason: activity.Reason}, nil
}

// ResolveCliLocalActivity 判定本机是否有人（ioreg 空闲/锁屏）。
func ResolveCliLocalActivity(prefs config.ResolvedCliBridgePreferences) (CliLocalActivity, error) {
	if !prefs.Presence.Enabled {
		return CliLocalActivity{LocalActive: false, Reason: "disabled"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	useMac := prefs.Presence.Platform == "macos" ||
		(prefs.Presence.Platform == "auto" && runtime.GOOS == "darwin")
	var readIdle func() (int, error)
	if useMac {
		readIdle = func() (int, error) { return readMacIdleSeconds(ctx) }
	} else if runtime.GOOS == "windows" {
		readIdle = func() (int, error) { return readWindowsIdleSeconds(ctx) }
	} else {
		return CliLocalActivity{LocalActive: false, Reason: "presence_failed"}, nil
	}

	// 锁屏 = 明确的「走了」，立刻判离开（不必等空闲阈值）。
	if useMac {
		if locked, err := readMacScreenLocked(ctx); err == nil && locked {
			return CliLocalActivity{LocalActive: false, Reason: "away"}, nil
		}
	}
	seconds, err := readIdle()
	if err != nil {
		return CliLocalActivity{LocalActive: false, Reason: "presence_failed"}, err
	}
	if seconds >= prefs.Presence.IdleThresholdSeconds {
		return CliLocalActivity{LocalActive: false, Reason: "away"}, nil
	}
	return CliLocalActivity{LocalActive: true, Reason: "local_active"}, nil
}
