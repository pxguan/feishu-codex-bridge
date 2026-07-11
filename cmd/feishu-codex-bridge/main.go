// feishu-codex-bridge 二进制入口。
// 接 internal/cli 命令树（cobra）。
package main

import (
	"os"

	"github.com/modelzen/feishu-codex-bridge/internal/agent/claude"
	"github.com/modelzen/feishu-codex-bridge/internal/agent/codex"
	"github.com/modelzen/feishu-codex-bridge/internal/cli"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/core"
)

//go:generate echo 版本号由 Makefile -ldflags 注入 internal/core.version

func main() {
	// 初始化日志（前台：stdout + 文件）。
	core.InitFileLogging(config.LogsDir(), os.Stdout)

	// codex / claude 后端 init 注册到 agent registry（import 触发）。
	_ = codex.CodexVersion // 保持 import（codex init 已注册 backend）
	_ = claude.ClaudeVersion

	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
