package clibridge

// hooks.go —— agent hook 安装器（对齐 TS cli-bridge/hooks.ts）。
// 把本 daemon 的 `hook` 子命令装进 Claude Code（~/.claude/settings.json）和
// Codex（~/.codex/hooks.json + config.toml 的 [features] hooks=true）。
// inspect 探测安装状态（含 agent2lark 冲突检测 + codex feature gate）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const agent2LarkMarker = "agent2lark-hook"

const (
	codexEvents = "PermissionRequest,Stop"
	claudeEvents = "PermissionRequest,Stop"
)

// resolveBridgeHookCommand 生成 hook 要执行的命令（绝对路径 + hook 子命令）。
// botAppID 非空时追加 --bot，使 hook 命中同 bot 的 daemon socket。
func ResolveBridgeHookCommand(botAppID string) string {
	self, err := os.Executable()
	if err != nil || self == "" {
		self = "feishu-codex-bridge"
	}
	var base string
	if runtime.GOOS == "windows" {
		base = shellQuote(self) + " hook"
	} else {
		base = `"` + self + `" hook`
	}
	if botAppID != "" {
		base += " --bot " + shellQuote(botAppID)
	}
	return base
}

// InstallCliBridgeHooksOptions 安装选项。
type InstallCliBridgeHooksOptions struct {
	HomeDir string
	Command string
	Agents  struct {
		Claude bool
		Codex  bool
	}
}

// InspectCliBridgeHooksOptions 探测选项。
type InspectCliBridgeHooksOptions struct {
	HomeDir string
}

func resolveHome(homeDir string) string {
	if homeDir != "" {
		return homeDir
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return map[string]any{}, nil
	}
	return out, nil
}

func writeJSON(path string, value map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func isBridgeCommand(command string) bool {
	return strings.Contains(command, " hook") &&
		strings.Contains(command, "--agent") &&
		!strings.Contains(command, agent2LarkMarker)
}

func isBridgeAgentCommand(command, agent string) bool {
	return isBridgeCommand(command) && strings.Contains(command, "--agent "+agent)
}

func isAgent2LarkCommand(command string) bool {
	return strings.Contains(command, agent2LarkMarker)
}

func inspectAgent(agent CliBridgeAgent, root map[string]any, events []string) CliHookStatus {
	hooks, _ := root["hooks"].(map[string]any)
	commands := make([]string, 0)
	for _, groups := range hooks {
		glist, ok := groups.([]any)
		if !ok {
			continue
		}
		for _, g := range glist {
			gm, ok := g.(map[string]any)
			if !ok {
				continue
			}
			hlist, ok := gm["hooks"].([]any)
			if !ok {
				continue
			}
			for _, h := range hlist {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				if cmd, ok := hm["command"].(string); ok {
					commands = append(commands, cmd)
				}
			}
		}
	}
	for _, c := range commands {
		if isAgent2LarkCommand(c) {
			return CliHookStatus{Agent: agent, Status: HookConflictAgent2Lark, Details: []string{"agent2lark hook command found"}}
		}
	}
	installed := make([]string, 0)
	for _, ev := range events {
		groups, ok := hooks[ev].([]any)
		if !ok {
			continue
		}
		found := false
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				continue
			}
			hlist, ok := gm["hooks"].([]any)
			if !ok {
				continue
			}
			for _, h := range hlist {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				if cmd, ok := hm["command"].(string); ok && isBridgeAgentCommand(cmd, agent) {
					found = true
				}
			}
		}
		if found {
			installed = append(installed, ev)
		}
	}
	if len(installed) == len(events) {
		return CliHookStatus{Agent: agent, Status: HookInstalled, Details: installed}
	}
	if len(installed) > 0 || hasAnyBridge(commands) {
		return CliHookStatus{Agent: agent, Status: HookNeedsRepair, Details: installed}
	}
	return CliHookStatus{Agent: agent, Status: HookNotInstalled, Details: []string{}}
}

func hasAnyBridge(commands []string) bool {
	for _, c := range commands {
		if isBridgeCommand(c) {
			return true
		}
	}
	return false
}

// InspectCliBridgeHooks 探测 Claude Code + Codex 安装状态。
func InspectCliBridgeHooks(opts InspectCliBridgeHooksOptions) (claude, codex CliHookStatus) {
	home := resolveHome(opts.HomeDir)
	claudeRoot, _ := readJSON(filepath.Join(home, ".claude", "settings.json"))
	codexRoot, _ := readJSON(filepath.Join(home, ".codex", "hooks.json"))
	cs := inspectAgent(AgentClaude, claudeRoot, strings.Split(claudeEvents, ","))
	xs := inspectAgent(AgentCodex, codexRoot, strings.Split(codexEvents, ","))
	// Codex 还需 [features] hooks=true，否则 agent 静默忽略 hook。
	if xs.Status == HookInstalled {
		tomlPath := filepath.Join(home, ".codex", "config.toml")
		if data, err := os.ReadFile(tomlPath); err == nil {
			if !hasCodexHooksFeature(string(data)) {
				xs = CliHookStatus{
					Agent:  AgentCodex,
					Status: HookNeedsRepair,
					Details: append(xs.Details, "config.toml [features] hooks=true missing"),
				}
			}
		}
	}
	return cs, xs
}

func removeHookGroups(hooks map[string]any, shouldRemove func(string) bool) map[string]any {
	out := map[string]any{}
	for ev, groups := range hooks {
		glist, ok := groups.([]any)
		if !ok {
			out[ev] = groups
			continue
		}
		kept := make([]any, 0, len(glist))
		for _, g := range glist {
			gm, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			hlist, ok := gm["hooks"].([]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			keptHooks := make([]any, 0, len(hlist))
			for _, h := range hlist {
				hm, ok := h.(map[string]any)
				if !ok {
					keptHooks = append(keptHooks, h)
					continue
				}
				if cmd, ok := hm["command"].(string); ok && shouldRemove(cmd) {
					continue
				}
				keptHooks = append(keptHooks, h)
			}
			if len(keptHooks) > 0 {
				ng := map[string]any{}
				for k, v := range gm {
					ng[k] = v
				}
				ng["hooks"] = keptHooks
				kept = append(kept, ng)
			}
		}
		if len(kept) > 0 {
			out[ev] = kept
		}
	}
	return out
}

func installAgentGroups(hooks map[string]any, agent CliBridgeAgent, events []string, command string) map[string]any {
	out := removeHookGroups(hooks, func(cmd string) bool {
		return isBridgeCommand(cmd) || isAgent2LarkCommand(cmd)
	})
	for _, ev := range events {
		groups, _ := out[ev].([]any)
		groups = append(groups, map[string]any{
			"matcher": "*",
			// timeout ≥ IPC 等待上界（24h），否则 agent 会先于人点批准杀掉 hook。
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": command + " --agent " + agent,
					"timeout": 86400,
				},
			},
		})
		out[ev] = groups
	}
	return out
}

// InstallCliBridgeHooks 安装 Claude Code + Codex 的 hook。
func InstallCliBridgeHooks(opts InstallCliBridgeHooksOptions) error {
	home := resolveHome(opts.HomeDir)
	if opts.Agents.Claude {
		file := filepath.Join(home, ".claude", "settings.json")
		root, err := readJSON(file)
		if err != nil {
			return err
		}
		if _, ok := root["hooks"].(map[string]any); !ok {
			root["hooks"] = map[string]any{}
		}
		root["hooks"] = installAgentGroups(root["hooks"].(map[string]any), AgentClaude, strings.Split(claudeEvents, ","), opts.Command)
		if err := writeJSON(file, root); err != nil {
			return err
		}
	}
	if opts.Agents.Codex {
		file := filepath.Join(home, ".codex", "hooks.json")
		root, err := readJSON(file)
		if err != nil {
			return err
		}
		if _, ok := root["hooks"].(map[string]any); !ok {
			root["hooks"] = map[string]any{}
		}
		root["hooks"] = installAgentGroups(root["hooks"].(map[string]any), AgentCodex, strings.Split(codexEvents, ","), opts.Command)
		if err := writeJSON(file, root); err != nil {
			return err
		}
		tomlPath := filepath.Join(home, ".codex", "config.toml")
		existing, err := os.ReadFile(tomlPath)
		existingStr := ""
		if err == nil {
			existingStr = string(existing)
		}
		if err := os.MkdirAll(filepath.Dir(tomlPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(tomlPath, []byte(withCodexHooksFeature(existingStr)), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// UninstallCliBridgeHooks 移除 Claude Code + Codex 的 bridge hook（含 config.toml gate）。
func UninstallCliBridgeHooks(opts InspectCliBridgeHooksOptions) error {
	home := resolveHome(opts.HomeDir)
	for _, file := range []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "hooks.json"),
	} {
		root, err := readJSON(file)
		if err != nil {
			return err
		}
		if hooks, ok := root["hooks"].(map[string]any); ok {
			root["hooks"] = removeHookGroups(hooks, isBridgeCommand)
			if err := writeJSON(file, root); err != nil {
				return err
			}
		}
	}
	tomlPath := filepath.Join(home, ".codex", "config.toml")
	if existing, err := os.ReadFile(tomlPath); err == nil {
		next := withoutCodexHooksFeature(string(existing))
		if next != string(existing) {
			if err := os.WriteFile(tomlPath, []byte(next), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// ── config.toml [features] hooks 处理 ─────────────────────────

func hasCodexHooksFeature(text string) bool {
	inFeatures := false
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if regexpMatch(`^\[features\]$`, trimmed) {
			inFeatures = true
			continue
		}
		if inFeatures && regexpMatch(`^\[.+\]$`, trimmed) {
			inFeatures = false
			continue
		}
		if inFeatures && regexpMatch(`^hooks\s*=\s*true\b`, trimmed) {
			return true
		}
	}
	return false
}

func withCodexHooksFeature(text string) string {
	lines := strings.Split(text, "\n")
	// 去掉尾随空行计数。
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	featuresIndex := -1
	nextSectionIndex := len(lines)
	codexHooksIndex := -1
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if regexpMatch(`^\[features\]$`, trimmed) {
			featuresIndex = i
			nextSectionIndex = len(lines)
			continue
		}
		if featuresIndex >= 0 && i > featuresIndex && regexpMatch(`^\[.+\]$`, trimmed) {
			if i < nextSectionIndex {
				nextSectionIndex = i
			}
		}
		if featuresIndex >= 0 && i > featuresIndex && i < nextSectionIndex && regexpMatch(`^hooks\s*=`, trimmed) {
			codexHooksIndex = i
		}
	}
	if featuresIndex < 0 {
		prefix := lines
		if len(prefix) > 0 {
			prefix = append(prefix, "")
		}
		return strings.Join(append(prefix, "[features]", "hooks = true", ""), "\n")
	}
	if codexHooksIndex >= 0 {
		lines[codexHooksIndex] = "hooks = true"
		return strings.Join(append(lines, ""), "\n")
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:featuresIndex+1]...)
	out = append(out, "hooks = true")
	out = append(out, lines[featuresIndex+1:]...)
	return strings.Join(append(out, ""), "\n")
}

func withoutCodexHooksFeature(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	featuresHeader := -1
	featuresHasKeys := false
	closeFeatures := func() {
		if featuresHeader >= 0 && !featuresHasKeys {
			out = append(out[:featuresHeader], out[featuresHeader+1:]...)
		}
		featuresHeader = -1
		featuresHasKeys = false
	}
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if regexpMatch(`^\[features\]$`, trimmed) {
			closeFeatures()
			featuresHeader = len(out)
			out = append(out, raw)
			continue
		}
		if featuresHeader >= 0 && regexpMatch(`^\[.+\]$`, trimmed) {
			closeFeatures()
			out = append(out, raw)
			continue
		}
		if featuresHeader >= 0 && regexpMatch(`^hooks\s*=`, trimmed) {
			continue
		}
		if featuresHeader >= 0 && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			featuresHasKeys = true
		}
		out = append(out, raw)
	}
	closeFeatures()
	return strings.Join(out, "\n")
}

func shellQuote(value string) string {
	return `"` + strings.NewReplacer(`"`, `\"`, `\`, `\\`, `$`, `\$`, "`", "\\`").Replace(value) + `"`
}
