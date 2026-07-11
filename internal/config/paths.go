package config

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// paths.go —— 全部文件系统路径常量（对齐 TS config/paths，见方案 §4）。
//
// Go 改进（相对 TS）：
//   - TS 用模块级 currentBotDir 全局态切目录（注释警告 daemon 不可在请求路径上调）。
//     Go 版改为 Paths{AppID} 实例 + 纯函数 BotXxxFile(appID)，显式传 appId，消除全局态。
//   - 全局文件（secrets.enc / bots.json / logs/ 等）用包级函数；per-bot 文件用 Paths 或纯函数。

const appDirName = ".feishu-codex-bridge"

// AppDir 返回 ~/.feishu-codex-bridge。
func AppDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, appDirName)
}

// ── 全局文件（与具体 bot 无关）──────────────────────────────────

func BotsFile() string            { return filepath.Join(AppDir(), "bots.json") }
func SecretsFile() string         { return filepath.Join(AppDir(), "secrets.enc") }
func KeystoreSaltFile() string    { return filepath.Join(AppDir(), ".keystore.salt") }
func SecretsGetterScript() string { return filepath.Join(AppDir(), "secrets-getter") }
func LogsDir() string             { return filepath.Join(AppDir(), "logs") }
func ServiceLog() string          { return filepath.Join(AppDir(), "service.log") }
func ServiceErrLog() string       { return filepath.Join(AppDir(), "service.err.log") }
func MediaDir() string            { return filepath.Join(AppDir(), "media") }
func InboundDir() string          { return filepath.Join(AppDir(), "inbound") }
func BackendsDir() string         { return filepath.Join(AppDir(), "backends") }
func ProjectsRootDir() string     { return filepath.Join(AppDir(), "projects") }

// ResolveProjectsRootDir 取空白项目默认父目录：偏好 ProjectsRootDir 优先，否则 AppDir()/projects。
// 支持 ~ 开头与绝对路径；相对路径按 AppDir 解析。
func ResolveProjectsRootDir(cfg AppConfig) string {
	root := ""
	if cfg.Preferences != nil {
		root = strings.TrimSpace(cfg.Preferences.ProjectsRootDir)
	}
	if root == "" {
		return ProjectsRootDir()
	}
	if strings.HasPrefix(root, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = filepath.Join(home, strings.TrimPrefix(root, "~"))
		}
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(AppDir(), root)
	}
	return root
}
func WebConsoleFile() string      { return filepath.Join(AppDir(), "web-console.json") }
func WebTokenFile() string        { return filepath.Join(AppDir(), "web-token") }
func DaemonPIDFile() string       { return filepath.Join(AppDir(), "daemon.pid") }

// ── per-bot 路径（纯函数，不碰全局态）───────────────────────────

func BotDir(appID string) string           { return filepath.Join(AppDir(), "bots", appID) }
func BotConfigFile(appID string) string    { return filepath.Join(BotDir(appID), "config.json") }
func BotSessionsFile(appID string) string  { return filepath.Join(BotDir(appID), "sessions.json") }
func BotProjectsFile(appID string) string  { return filepath.Join(BotDir(appID), "projects.json") }
func BotProcessesFile(appID string) string { return filepath.Join(BotDir(appID), "processes.json") }

// BotCommentInstructionsFile 云文档评论 @bot 的可编辑提示词 master 文件（当前 bot）。
// 用户直接编辑这一份，桥在每条评论运行前把它同步进该文档的评论工作目录（AGENTS.md / CLAUDE.md）。
// 首次缺失时由 comments 模块用内置默认模板自动落地（对齐 TS commentInstructionsFile）。
func BotCommentInstructionsFile(appID string) string {
	return filepath.Join(BotDir(appID), "comment-instructions.md")
}

// BotCommentsRootDir 评论工作目录根（当前 bot）：每个被评论文档一个 comment-<type>-<token> 子目录，
// 放同步进去的 AGENTS.md / CLAUDE.md。per-bot 隔离，避免编辑提示词时越界改到别的 bot（对齐 TS commentsRootDir）。
func BotCommentsRootDir(appID string) string {
	return filepath.Join(BotDir(appID), "comments")
}

// Paths 持有某 bot 的路径集合，替代 TS 的全局 currentBotDir。
// 启动时按活跃 bot 构造一份；跨 bot 聚合用上面的纯函数。
type Paths struct {
	AppID string
}

func NewPaths(appID string) *Paths { return &Paths{AppID: appID} }

func (p *Paths) Dir() string           { return BotDir(p.AppID) }
func (p *Paths) ConfigFile() string    { return BotConfigFile(p.AppID) }
func (p *Paths) SessionsFile() string  { return BotSessionsFile(p.AppID) }
func (p *Paths) ProjectsFile() string  { return BotProjectsFile(p.AppID) }
func (p *Paths) ProcessesFile() string { return BotProcessesFile(p.AppID) }
func (p *Paths) CommentInstructionsFile() string { return BotCommentInstructionsFile(p.AppID) }
func (p *Paths) CommentsRootDir() string         { return BotCommentsRootDir(p.AppID) }

// CliBridgeSocket 返回 cli-bridge 的 Unix socket 路径（二期 cli-bridge 用）。
func (p *Paths) CliBridgeSocket() string { return filepath.Join(p.Dir(), "cli-bridge.sock") }

// KeystoreSeed 返回 keystore 派生种子 "hostname|username"（与 TS 字节级一致）。
// 测试用 Keystore.WithSeed 注入固定值，无需依赖真实主机名。
func KeystoreSeed() string {
	host, _ := os.Hostname()
	username := ""
	if u, err := user.Current(); err == nil && u != nil {
		username = u.Username
	}
	return host + "|" + username
}
