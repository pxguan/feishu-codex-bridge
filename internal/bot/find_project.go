package bot

// find_project.go —— 按 (cwd, backend) 身份键解析项目（对齐 TS bot/bridge.ts findProjectByCwd）。
// cli-bridge completion-sync 用：Stop 时按 (cwd, source) 路由结果到飞书（未绑定自动建群）。
// notify-scope 也用：省略 source = 跨后端通配。

import (
	"path/filepath"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// FindProjectByCwd 按 (cwd, backend) 身份键解析项目。
//   - cwd 精确匹配优先（cwd == project.Cwd）；
//   - 子目录命中（cwd 在 project.Cwd 子树内）；
//   - backend 匹配：source 非空时要求 project.Backend == source（legacy 缺省默认 codex）；
//     source 空时跨后端通配（notify-scope 用）。
//
// 返回首个命中（nil = 未找到）。
func FindProjectByCwd(store *project.Store, cwd, source string) (*project.Project, bool) {
	projects, err := store.List()
	if err != nil {
		return nil, false
	}
	cwd = filepath.Clean(cwd)

	// 第一轮：cwd 精确匹配 + backend 匹配。
	for i := range projects {
		p := &projects[i]
		if filepath.Clean(p.Cwd) != cwd {
			continue
		}
		if matchBackend(p.Backend, source) {
			return p, true
		}
	}
	// 第二轮：子目录命中 + backend 匹配。
	for i := range projects {
		p := &projects[i]
		pc := filepath.Clean(p.Cwd)
		if pc == cwd {
			continue // 精确已在第一轮
		}
		// cwd 在 p.Cwd 子树内（cwd 是 p.Cwd 的后代）。
		if isSubdir(cwd, pc) && matchBackend(p.Backend, source) {
			return p, true
		}
	}
	return nil, false
}

// matchBackend backend 匹配：source 空 → 通配；否则 project.Backend == source（legacy 缺省默认 codex）。
func matchBackend(projectBackend, source string) bool {
	if source == "" {
		return true // 通配（notify-scope）
	}
	pb := projectBackend
	if pb == "" {
		pb = agent.DEFAULT_BACKEND_ID // legacy 缺省默认 codex
	}
	return pb == source
}

// isSubdir 判断 child 是否在 parent 子树内（child == parent 或 child 以 parent/ 开头）。
func isSubdir(child, parent string) bool {
	if child == parent {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}
