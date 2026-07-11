// Package core 提供进程级基础设施：版本号、日志、单实例锁、stdin 处理。
//
// version 子模块：bridge 的版本号来源。
//
// 与 TS 版的差异（见 .private/docs/Go重构方案.md §3）：
// TS 版运行时读 dist/cli.js 上一级的 package.json，缺失兜底 "0.0.0"；
// Go 版改为编译期注入——包级变量 version 由 -ldflags "-X" 覆盖，
// 二进制自包含，不再依赖外部 package.json 文件。
package core

// version 在构建时由 -ldflags "-X github.com/modelzen/feishu-codex-bridge/internal/core.version=<v>" 注入。
// 未注入时（go run / go build 不带 ldflags）使用下面的开发态默认值。
var version = "0.0.0-dev"

// Version 返回当前 bridge 版本号。
func Version() string { return version }
