package agent

// detect.go —— 后端探测 + 智能默认（对齐 TS agent/detect）。
// DetectAgents（探 codex）→ PickDefaultBackend → EffectiveDefaultBackend（缓存）→ BackendForProject。
// 依赖 catalog + codex.ResolveCodexBin/CodexVersion（通过 import codex 触发注册）。

import "sync"

// AgentID 底层 agent id（codex / 未来扩展）。
type AgentID = string

// BackendAvailability 一个后端在本机的可用性。
type BackendAvailability struct {
	BackendID      string
	Available      bool
	Reason         string // 不可用原因
	Version        string
	SupportedModes []PermissionMode
	Installable    bool
}

// AgentRuntime 一个底层 agent 的探测结果 + 衍生后端可用性。
type AgentRuntime struct {
	ID          AgentID
	DisplayName string
	Installed   bool
	Version     string
	Backends    []BackendAvailability
	InstallHint string
}

// ProbeFunc 探测单个后端（注入，避免 agent 包依赖 codex 子包）。
type ProbeFunc func() (bin string, version string, ok bool, hint string)

// DetectAgentsFns 可注入的 detect 实现列表（各后端包 init 通过 RegisterDetect 追加）。
// 单变量会被后初始化的包覆盖，故用 slice 累积，支持同时探测多个后端（如 codex + claude）。
var DetectAgentsFns []func() []AgentRuntime

// RegisterDetect 追加一个后端探测函数（幂等由调用方保证）。
func RegisterDetect(fn func() []AgentRuntime) {
	DetectAgentsFns = append(DetectAgentsFns, fn)
}

// DetectAgents 探全部已注册 agent（codex / claude 等，经各自 init 注入）。
func DetectAgents() []AgentRuntime {
	out := []AgentRuntime{}
	for _, fn := range DetectAgentsFns {
		out = append(out, fn()...)
	}
	return out
}

// PickDefaultBackend 从 detectAgents 结果挑默认（有 codex→codex；无→占位）。
func PickDefaultBackend(agents []AgentRuntime) string {
	find := func(id string) *BackendAvailability {
		for _, a := range agents {
			for i := range a.Backends {
				if a.Backends[i].BackendID == id {
					return &a.Backends[i]
				}
			}
		}
		return nil
	}
	pickable := func(id string) bool {
		entry, ok := CatalogByID(id)
		if !ok || entry.Hidden {
			return false
		}
		ba := find(id)
		return ba != nil && ba.Available
	}
	if pickable(DEFAULT_BACKEND_ID) {
		return DEFAULT_BACKEND_ID
	}
	// codex 不可用 → 退到任一其它可用后端（如 claude），让只有 claude 的机器也能跑。
	for _, e := range BackendCatalog {
		if e.ID != DEFAULT_BACKEND_ID && pickable(e.ID) {
			return e.ID
		}
	}
	return DEFAULT_BACKEND_ID // 无可用后端 → codex 占位（doctor 报需装）
}

var (
	defaultCacheMu sync.Mutex
	defaultCache   string
	defaultCached  bool
)

// EffectiveDefaultBackend 有效默认（缓存 + force 绕过）。
func EffectiveDefaultBackend(force bool) string {
	defaultCacheMu.Lock()
	defer defaultCacheMu.Unlock()
	if !force && defaultCached {
		return defaultCache
	}
	agents := DetectAgents()
	defaultCache = PickDefaultBackend(agents)
	defaultCached = true
	return defaultCache
}

// BackendForProject 项目实际后端：显式选择优先（必须 catalog 注册），否则有效默认。
func BackendForProject(backend string, force bool) string {
	if backend != "" {
		if _, ok := CatalogByID(backend); ok {
			return backend
		}
	}
	return EffectiveDefaultBackend(force)
}

// AvailabilityToProbe 后端可用性 → BackendProbe 归一。
func AvailabilityToProbe(a BackendAvailability) BackendProbe {
	depState := "external-missing"
	if a.Available {
		depState = "installed"
	} else if a.Installable {
		depState = "not-installed"
	}
	hint := ""
	if !a.Available {
		hint = a.Reason
	}
	installable := a.Installable
	return BackendProbe{
		Ok: a.Available, Version: a.Version, Hint: hint,
		Installable: installable, DepState: depState,
	}
}

// FamilyOf 按 agent family 取 runtime（family 与 agent id 同名）。
func FamilyOf(agents []AgentRuntime, family AgentFamily) *AgentRuntime {
	for i := range agents {
		if agents[i].ID == string(family) {
			return &agents[i]
		}
	}
	return nil
}
