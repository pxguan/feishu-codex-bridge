package agent

import "testing"

func entryIDs(list []BackendCatalogEntry) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}
func containsID(list []BackendCatalogEntry, id string) bool {
	for _, e := range list {
		if e.ID == id {
			return true
		}
	}
	return false
}

func TestCatalogBackendIDs(t *testing.T) {
	ids := CatalogBackendIDs()
	if !containsID(catalogEntriesFromIDs(ids), DEFAULT_BACKEND_ID) {
		t.Errorf("missing %s in %v", DEFAULT_BACKEND_ID, ids)
	}
	found := false
	for _, id := range ids {
		if id == "claude-agent" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing claude-agent in %v", ids)
	}
}

// 辅助：ids → entries 仅用于 containsID 复用（id 集合校验）。
func catalogEntriesFromIDs(ids []string) []BackendCatalogEntry {
	out := make([]BackendCatalogEntry, len(ids))
	for i, id := range ids {
		out[i] = BackendCatalogEntry{ID: id}
	}
	return out
}

func TestVisibleCatalog_AllVisible(t *testing.T) {
	vis := VisibleCatalog()
	if len(vis) != len(BackendCatalog) {
		t.Fatalf("no hidden entries expected: visible=%d catalog=%d", len(vis), len(BackendCatalog))
	}
}

func TestVisibleFromList_FiltersHidden(t *testing.T) {
	list := []BackendCatalogEntry{{ID: "a"}, {ID: "b", Hidden: true}}
	vis := visibleFromList(list)
	if len(vis) != 1 || vis[0].ID != "a" {
		t.Fatalf("hidden filter wrong: %+v", vis)
	}
}

func TestCatalogByID(t *testing.T) {
	if _, ok := CatalogByID(DEFAULT_BACKEND_ID); !ok {
		t.Fatal("codex should exist")
	}
	if _, ok := CatalogByID("nope"); ok {
		t.Fatal("unknown id should miss")
	}
}

func TestCatalogByFamily(t *testing.T) {
	codex := CatalogByFamily(FamilyCodex)
	if len(codex) != 1 || codex[0].ID != DEFAULT_BACKEND_ID {
		t.Fatalf("codex family wrong: %+v", codex)
	}
	claude := CatalogByFamily(FamilyClaude)
	if len(claude) != 1 || claude[0].ID != "claude-agent" {
		t.Fatalf("claude family wrong: %+v", claude)
	}
}

func TestIsInstallable(t *testing.T) {
	codexEntry, _ := CatalogByID(DEFAULT_BACKEND_ID)
	if IsInstallable(codexEntry) {
		t.Fatal("external-cli should NOT be installable")
	}
	// Go 端 claude 走外部 `claude` CLI（DepExternalCLI），与 codex 同属 external-cli，
	// 不再由桥按需 npm 安装（TS 时代的 @anthropic-ai/claude-agent-sdk 模式已弃用）。
	claudeEntry, _ := CatalogByID("claude-agent")
	if claudeEntry.Dep.Kind != DepExternalCLI {
		t.Fatalf("claude should be external-cli, got %q", claudeEntry.Dep.Kind)
	}
	if IsInstallable(claudeEntry) {
		t.Fatal("external-cli claude should NOT be installable")
	}
}

func TestProjectCreatable_CodexAlwaysSelectable(t *testing.T) {
	listed := ProjectCreatableBackends(PermissionFull, func(BackendCatalogEntry) bool { return false })
	if !containsID(listed, DEFAULT_BACKEND_ID) {
		t.Fatal("codex must always be selectable even when isInstalled=false")
	}
	if containsID(listed, "claude-agent") {
		t.Fatal("claude should NOT list when not installed")
	}
}

func TestProjectCreatable_ClaudeWhenInstalled(t *testing.T) {
	listed := ProjectCreatableBackends(PermissionFull, func(e BackendCatalogEntry) bool {
		return e.ID == "claude-agent"
	})
	if !containsID(listed, DEFAULT_BACKEND_ID) || !containsID(listed, "claude-agent") {
		t.Fatalf("want codex+claude, got %v", entryIDs(listed))
	}
}

func TestProjectCreatable_ModeFilter(t *testing.T) {
	list := []BackendCatalogEntry{
		{ID: "codex-appserver", Dep: BackendDep{Kind: DepExternalCLI}},
		{ID: "qa-only", SupportedModes: []PermissionMode{PermissionQA}, Dep: BackendDep{Kind: DepNpmOnDemand}},
	}
	listed := projectCreatableFromList(list, PermissionFull, func(BackendCatalogEntry) bool { return true })
	if containsID(listed, "qa-only") {
		t.Fatal("qa-only should be filtered out under full mode")
	}
	if !containsID(listed, "codex-appserver") {
		t.Fatal("codex should remain under full mode")
	}
}

func TestProjectCreatable_HiddenFiltered(t *testing.T) {
	list := []BackendCatalogEntry{
		{ID: "codex-appserver"},
		{ID: "secret", Hidden: true},
	}
	listed := projectCreatableFromList(list, PermissionFull, func(BackendCatalogEntry) bool { return true })
	if containsID(listed, "secret") {
		t.Fatal("hidden backend must be filtered from picker")
	}
}
