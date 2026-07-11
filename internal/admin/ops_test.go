package admin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// mockBackend 最小 AgentBackend 实现（仅供 admin/ops 测试）。
type mockBackend struct {
	id, name string
	modes    []agent.PermissionMode
	probe    agent.BackendProbe
}

func (m *mockBackend) ID() string                                            { return m.id }
func (m *mockBackend) DisplayName() string                                   { return m.name }
func (m *mockBackend) Capabilities() agent.AgentCapabilities                 { return agent.AllCapabilities() }
func (m *mockBackend) SupportedModes() []agent.PermissionMode                { return m.modes }
func (m *mockBackend) IsAvailable(context.Context) bool                      { return m.probe.Ok }
func (m *mockBackend) Doctor(context.Context, bool) agent.BackendProbe       { return m.probe }
func (m *mockBackend) ListModels(context.Context) ([]agent.ModelInfo, error) { return nil, nil }
func (m *mockBackend) ListThreads(context.Context, string, int) ([]agent.ThreadSummary, error) {
	return nil, nil
}
func (m *mockBackend) ReadHistory(context.Context, string, string, int) (agent.ThreadHistory, error) {
	return agent.ThreadHistory{}, nil
}
func (m *mockBackend) StartThread(context.Context, agent.StartThreadOptions) (agent.AgentThread, error) {
	return nil, errors.New("not impl in mock")
}
func (m *mockBackend) ResumeThread(context.Context, agent.ResumeThreadOptions) (agent.AgentThread, error) {
	return nil, errors.New("not impl in mock")
}

func newAdminStore(t *testing.T) *project.Store {
	t.Helper()
	return project.NewStore(filepath.Join(t.TempDir(), "projects.json"))
}

// ── ValidateBackendSwitch ───────────────────────────────────────

func TestValidateBackendSwitch_UnknownBackend(t *testing.T) {
	reason := ValidateBackendSwitch("ghost", []string{"codex-appserver"}, project.Project{}, nil, &agent.BackendProbe{Ok: true})
	if reason == "" {
		t.Fatal("unknown backend should give reason")
	}
}

func TestValidateBackendSwitch_ProbeFail(t *testing.T) {
	reason := ValidateBackendSwitch("codex-appserver", []string{"codex-appserver"}, project.Project{}, nil, &agent.BackendProbe{Ok: false, Hint: "未装"})
	if reason == "" || reason == "后端「codex-appserver」当前不可用：" {
		t.Fatalf("probe fail reason wrong: %q", reason)
	}
}

func TestValidateBackendSwitch_ProbeNil(t *testing.T) {
	reason := ValidateBackendSwitch("codex-appserver", []string{"codex-appserver"}, project.Project{}, nil, nil)
	if reason == "" {
		t.Fatal("nil probe should reject")
	}
}

func TestValidateBackendSwitch_UnsupportedTier(t *testing.T) {
	// 后端只支持 qa，项目是 full → 拒。
	reason := ValidateBackendSwitch("claude", []string{"claude"}, project.Project{Mode: agent.PermissionFull},
		[]agent.PermissionMode{agent.PermissionQA}, &agent.BackendProbe{Ok: true})
	if reason == "" {
		t.Fatal("unsupported tier should reject")
	}
}

func TestValidateBackendSwitch_AllPass(t *testing.T) {
	reason := ValidateBackendSwitch("codex-appserver", []string{"codex-appserver"}, project.Project{Mode: agent.PermissionQA}, nil, &agent.BackendProbe{Ok: true})
	if reason != "" {
		t.Fatalf("all-pass should return empty, got %q", reason)
	}
}

// ── PerformSetNoMention / AutoCompact / ModelDefault ────────────

func TestPerformSetNoMention(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1"})
	out := PerformSetNoMention(s, "p1", true)
	if !out.Ok || out.Project == nil || out.Project.NoMention == nil || !*out.Project.NoMention {
		t.Fatalf("setNoMention wrong: %+v", out)
	}
	got, _ := s.GetByName("p1")
	if got.NoMention == nil || !*got.NoMention {
		t.Fatal("noMention not persisted")
	}
}

func TestPerformSetNoMention_MissingProject(t *testing.T) {
	s := newAdminStore(t)
	out := PerformSetNoMention(s, "nope", true)
	if out.Ok {
		t.Fatal("missing project should not be ok")
	}
}

func TestPerformSetAutoCompact_Evicts(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1"})
	evicted := ""
	out := PerformSetAutoCompact(s, "p1", false, func(chatID string) { evicted = chatID })
	if !out.Ok || evicted != "oc_1" {
		t.Fatalf("autoCompact should evict oc_1: %+v evicted=%q", out, evicted)
	}
}

func TestPerformSetModelDefault(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1"})
	out := PerformSetModelDefault(s, "p1", "gpt-5", agent.EffortHigh)
	if !out.Ok || out.Project.DefaultModel != "gpt-5" || out.Project.DefaultEffort != agent.EffortHigh {
		t.Fatalf("modelDefault wrong: %+v", out)
	}
}

// ── PerformSetPermissionMode ────────────────────────────────────

func TestPerformSetPermissionMode_InvalidTier(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1"})
	out := PerformSetPermissionMode(s, "p1", "bogus", "", nil, nil)
	if out.Ok {
		t.Fatal("bogus tier should be rejected")
	}
}

func TestPerformSetPermissionMode_OkAndEvicts(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1"})
	evicted := ""
	out := PerformSetPermissionMode(s, "p1", agent.PermissionQA, "", nil, func(c string) { evicted = c })
	if !out.Ok || out.Project.Mode != agent.PermissionQA || evicted != "oc_1" {
		t.Fatalf("setPermissionMode wrong: %+v evicted=%q", out, evicted)
	}
}

// ── PerformBackendSwitch ────────────────────────────────────────

func TestPerformBackendSwitch_LegacyLanding(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1", Backend: ""}) // legacy 无 backend
	mk := func(id string) (agent.AgentBackend, error) {
		return &mockBackend{id: id, name: "Codex", modes: agent.AllPermissionModes, probe: agent.BackendProbe{Ok: true}}, nil
	}
	out := PerformBackendSwitch(context.Background(), s, "p1", "codex-appserver", mk)
	if !out.Ok || out.Project.Backend != "codex-appserver" {
		t.Fatalf("legacy landing wrong: %+v", out)
	}
}

func TestPerformBackendSwitch_RejectsRuntimeSwitch(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1", Backend: "codex-appserver"})
	mk := func(id string) (agent.AgentBackend, error) {
		return &mockBackend{id: id, modes: agent.AllPermissionModes, probe: agent.BackendProbe{Ok: true}}, nil
	}
	out := PerformBackendSwitch(context.Background(), s, "p1", "claude-agent", mk)
	if out.Ok {
		t.Fatal("runtime switch should be rejected (backend fixed at creation)")
	}
}

func TestPerformBackendSwitch_ProbeFailRejects(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1", Backend: ""})
	mk := func(id string) (agent.AgentBackend, error) {
		return &mockBackend{id: id, modes: agent.AllPermissionModes, probe: agent.BackendProbe{Ok: false, Hint: "未装"}}, nil
	}
	out := PerformBackendSwitch(context.Background(), s, "p1", "claude-agent", mk)
	if out.Ok {
		t.Fatal("probe fail should reject switch")
	}
}

// ── RunWriteOp 分发 ─────────────────────────────────────────────

func TestRunWriteOp_Dispatch(t *testing.T) {
	s := newAdminStore(t)
	s.Add(project.Project{Name: "p1", ChatID: "oc_1"})
	deps := Deps{Store: s, BackendFor: func(string) (agent.AgentBackend, error) {
		return &mockBackend{modes: agent.AllPermissionModes, probe: agent.BackendProbe{Ok: true}}, nil
	}}
	out := RunWriteOp(context.Background(), WriteOp{Kind: OpSetNoMention, Project: "p1", On: false}, deps)
	if !out.Ok {
		t.Fatalf("dispatch setNoMention wrong: %+v", out)
	}
}

func TestTierLabel(t *testing.T) {
	if TierLabel(agent.PermissionQA) != "项目内只读" {
		t.Fatal("qa label")
	}
	if TierLabel(agent.PermissionFull) != "完全访问" {
		t.Fatal("full label")
	}
}
