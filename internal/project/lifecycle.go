package project

// lifecycle.go —— 项目生命周期纯函数（对齐 TS project/lifecycle）。
// assertBackendUsable（后端可用性门禁）+ resolveCwd（路径解析）+ Input 结构。
// 飞书 SDK 部分（createGroup/addManagers/setAnnouncement/onboardGroup）后续 feishu wrapper port 后接上。

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// CreateProjectInput 新建项目输入。
type CreateProjectInput struct {
	Name            string
	OwnerOpenID     string
	ExistingPath    string
	ProjectsRootDir string // 可选：空白项目默认父目录的 config.json 覆盖（否则回退默认）
	Kind            string               // multi|single（默认 multi）
	Mode            agent.PermissionMode // 默认 full
	Backend         string
	Network         bool
}

// JoinGroupInput 加入存量群输入。
type JoinGroupInput struct {
	Name            string
	ChatID          string
	AddedBy         string
	ExistingPath    string
	ProjectsRootDir string // 可选：空白项目默认父目录的 config.json 覆盖
	Kind            string
	Mode            agent.PermissionMode // 默认 qa
	Backend         string
	Network         bool
}

// AssertBackendUsable 校验「创建时选定的后端」此刻仍可用（已下载 + 支持该权限档）。
// backend 空（落回默认 codex）直接放行。失败返回 error（调用方在 spawn 前拦截，不留孤儿群）。
func AssertBackendUsable(backend string, mode agent.PermissionMode, isInstalled func(agent.BackendCatalogEntry) bool) error {
	if backend == "" {
		return nil
	}
	creatable := agent.ProjectCreatableBackends(mode, isInstalled)
	for _, e := range creatable {
		if e.ID == backend {
			return nil
		}
	}
	entry, ok := agent.CatalogByID(backend)
	if ok && agent.IsInstallable(entry) {
		return fmt.Errorf("「%s」尚未下载——请到 Web 控制台「后端 Agent」页点「下载」装好后，再回卡片选用", entry.DisplayName)
	}
	return fmt.Errorf("所选后端「%s」当前不可用（未下载或不支持该权限档），请回卡片重新选择", backend)
}

// ResolveCwd 解析项目工作目录：existingPath（必须存在）绑定既有文件夹；否则在 projectsRoot 下建空白项目。
// 返回 cwd + blank（是否空白）。existingPath 不存在返回 error（spawn 前抛，不留孤儿群）。
func ResolveCwd(name, existingPath, projectsRoot string) (cwd string, blank bool, err error) {
	if existingPath != "" {
		abs := existingPath
		if !filepath.IsAbs(abs) {
			abs, _ = filepath.Abs(existingPath)
		}
		if _, statErr := os.Stat(abs); statErr != nil {
			return "", false, fmt.Errorf("文件夹不存在：%s", abs)
		}
		return abs, false, nil
	}
	cwd = filepath.Join(projectsRoot, name)
	return cwd, true, nil
}

// ResolveProjectsRootDir 解析 preferences.projectsRootDir，不依赖 daemon 的 cwd。
// 空值保留历史默认（config.ProjectsRootDir()）；相对路径被拒（后台服务可能从不同目录启动）；
// ~ 或 ~/ 展开到用户主目录；其余必须是绝对路径。对齐 TS project/lifecycle.resolveProjectsRootDir。
func ResolveProjectsRootDir(configured string) (string, error) {
	if configured == "" {
		return config.ProjectsRootDir(), nil
	}
	value := trimSpaces(configured)
	if value == "" {
		return config.ProjectsRootDir(), nil
	}
	if value == "~" {
		home, err := homedir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if stringsHasPrefix(value, "~/") || stringsHasPrefix(value, "~\\") {
		home, err := homedir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, value[2:]), nil
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("配置 preferences.projectsRootDir 必须是绝对路径或以 ~ 开头")
	}
	return filepath.Clean(value), nil
}

// homedir 返回用户主目录。
func homedir() (string, error) {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h, nil
	}
	if h := os.Getenv("HOME"); h != "" {
		return h, nil
	}
	u, err := user.Current()
	if err != nil || u == nil {
		return "", fmt.Errorf("无法确定用户主目录")
	}
	return u.HomeDir, nil
}

// stringsHasPrefix 避免为这一个判断引入 strings 包依赖冲突（strings 已间接可用，但显式以便阅读）。
func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// ValidateCreateProjectInput 校验建项目输入（名称非空 + 无重名）。
func ValidateCreateProjectInput(store *Store, name string) error {
	name = trimSpaces(name)
	if name == "" {
		return errors.New("项目名不能为空")
	}
	existing, err := store.GetByName(name)
	if err != nil {
		return fmt.Errorf("查重名失败：%w", err)
	}
	if existing != nil {
		return fmt.Errorf("项目名「%s」已存在，换个名或用 /projects 看已有的", name)
	}
	return nil
}

// ValidateJoinGroupInput 校验加群输入（名称非空 + 无重名 + 群未绑定）。
func ValidateJoinGroupInput(store *Store, name, chatID string) error {
	if err := ValidateCreateProjectInput(store, name); err != nil {
		return err
	}
	bound, err := store.GetByChatID(chatID)
	if err != nil {
		return fmt.Errorf("查群绑定失败：%w", err)
	}
	if bound != nil {
		return fmt.Errorf("该群已绑定为项目「%s」", bound.Name)
	}
	return nil
}

func trimSpaces(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
