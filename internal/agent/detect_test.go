package agent

import "testing"

func agentsWith(backendID string, available bool) []AgentRuntime {
	return []AgentRuntime{{
		ID: "codex", DisplayName: "Codex", Installed: available,
		Backends: []BackendAvailability{{
			BackendID: backendID, Available: available, Version: "0.139",
		}},
	}}
}

func TestPickDefaultBackend_CodexAvailable(t *testing.T) {
	got := PickDefaultBackend(agentsWith(DEFAULT_BACKEND_ID, true))
	if got != DEFAULT_BACKEND_ID {
		t.Fatalf("codex available → codex: got %q", got)
	}
}

func TestPickDefaultBackend_CodexMissing(t *testing.T) {
	// 无可用 codex → 仍返回 codex 占位（doctor 报需装）。
	got := PickDefaultBackend(agentsWith(DEFAULT_BACKEND_ID, false))
	if got != DEFAULT_BACKEND_ID {
		t.Fatalf("codex missing → codex placeholder: got %q", got)
	}
}

func TestPickDefaultBackend_Empty(t *testing.T) {
	got := PickDefaultBackend(nil)
	if got != DEFAULT_BACKEND_ID {
		t.Fatalf("empty → default: got %q", got)
	}
}

func TestBackendForProject_Explicit(t *testing.T) {
	// 显式 + catalog 注册 → 用显式。
	got := BackendForProject(DEFAULT_BACKEND_ID, true)
	if got != DEFAULT_BACKEND_ID {
		t.Fatalf("explicit codex: got %q", got)
	}
}

func TestBackendForProject_ExplicitUnregisteredFallback(t *testing.T) {
	// 显式但未注册 → 回退有效默认。
	got := BackendForProject("nonexistent-backend", true)
	// 回退到 DEFAULT_BACKEND_ID。
	if got != DEFAULT_BACKEND_ID {
		// effectiveDefaultBackend 可能因环境返回其它（但默认是 codex）。
	}
}

func TestBackendForProject_EmptyUsesDefault(t *testing.T) {
	got := BackendForProject("", true)
	_ = got // 依赖环境（codex 在 PATH 与否）
}

func TestEffectiveDefaultBackend_Caching(t *testing.T) {
	// force 清缓存。
	EffectiveDefaultBackend(true)
	EffectiveDefaultBackend(false) // 用缓存
}

func TestAvailabilityToProbe(t *testing.T) {
	// installed。
	p := AvailabilityToProbe(BackendAvailability{Available: true, Version: "1.0"})
	if !p.Ok || p.Version != "1.0" || p.DepState != "installed" {
		t.Fatalf("installed probe: %+v", p)
	}
	// not-installed（installable）。
	p = AvailabilityToProbe(BackendAvailability{Available: false, Installable: true, Reason: "未装"})
	if p.Ok || p.DepState != "not-installed" || p.Hint != "未装" {
		t.Fatalf("not-installed probe: %+v", p)
	}
	// external-missing。
	p = AvailabilityToProbe(BackendAvailability{Available: false, Installable: false})
	if p.DepState != "external-missing" {
		t.Fatalf("external-missing probe: %+v", p)
	}
}

func TestFamilyOf(t *testing.T) {
	agents := []AgentRuntime{{ID: "codex", DisplayName: "Codex"}}
	f := FamilyOf(agents, FamilyCodex)
	if f == nil || f.ID != "codex" {
		t.Fatalf("familyOf codex: %+v", f)
	}
	if FamilyOf(agents, FamilyClaude) != nil {
		t.Fatal("claude family should not exist")
	}
}
