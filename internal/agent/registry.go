package agent

// registry.go —— 后端工厂注册表（对齐 TS agent/index.ts 的 REGISTRY）。
//
// catalog 是后端元数据单一真源；本表只载工厂函数。BackendIDs() 从 catalog 派生，
// 保证 catalog 与 registry 配对（漏一处即不一致）。
//
// Phase 1：codex-appserver 实例化（其实现见 internal/agent/codex）。
// 二期：claude-agent（CLI stream-json）。这里用工厂注入，避免 agent 包循环依赖 codex 子包。

// BackendFactory 构造一个 AgentBackend 实例。
type BackendFactory func() AgentBackend

var registry = map[string]BackendFactory{}

// RegisterBackend 注册一个后端工厂（init 时调用）。
func RegisterBackend(id string, f BackendFactory) {
	if f == nil {
		return
	}
	registry[id] = f
}

// CreateBackend 按 id 构造后端；id 空 → 默认 codex。
func CreateBackend(id string) (AgentBackend, error) {
	if id == "" {
		id = DEFAULT_BACKEND_ID
	}
	f, ok := registry[id]
	if !ok {
		return nil, &UnknownBackendError{ID: id}
	}
	return f(), nil
}

// UnknownBackendError 未知后端 id。
type UnknownBackendError struct{ ID string }

func (e *UnknownBackendError) Error() string {
	return "未知 agent 后端「" + e.ID + "」"
}
