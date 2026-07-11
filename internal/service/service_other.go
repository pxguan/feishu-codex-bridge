//go:build !darwin && !linux

package service

// 非 macOS / Linux 平台不提供一键安装（无对应 service manager）。

func platformInstall(opts Options) error      { return ErrUnsupported }
func platformUninstall() error                { return ErrUnsupported }
func platformStatus() Status                  { return Status{Note: ErrUnsupported.Error()} }
func platformRestart() error                 { return ErrUnsupported }
func existingPathEnv() string                 { return "" }
