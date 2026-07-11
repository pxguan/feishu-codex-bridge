package agent

// catalog.go —— 后端元数据单一真源（对齐 TS agent/catalog）。
//
// 与运行时工厂表（REGISTRY）职责正交：catalog 管「Web/DM 怎么展示、怎么装、怎么探」，
// REGISTRY 管「怎么构造能跑的实例」。靠 id 配对（Phase 1 codex 实现，claude 二期）。

// AgentFamily 底层 agent 家族（picker 分组用）。
type AgentFamily string

const (
	FamilyCodex  AgentFamily = "codex"
	FamilyClaude AgentFamily = "claude"
)

// BackendAccess 接入方式（仅描述用）。
type BackendAccess string

const (
	AccessAppServer BackendAccess = "app-server"
	AccessSDK       BackendAccess = "sdk"
	AccessACP       BackendAccess = "acp"
)

// DepKind 依赖类型。
type DepKind string

const (
	DepExternalCLI DepKind = "external-cli"
	DepNpmOnDemand DepKind = "npm-ondemand"
	DepNpmExternal DepKind = "npm-external"
)

// BackendDep 后端依赖描述。
type BackendDep struct {
	Kind         DepKind
	Pkg          string
	BinName      string
	Version      string
	ApproxSizeMB int
	DetectHint   string
	InstallCmd   string
}

// BackendCatalogEntry catalog 条目。
type BackendCatalogEntry struct {
	ID             string
	AgentFamily    AgentFamily
	DisplayName    string
	Access         BackendAccess
	Dep            BackendDep
	SupportedModes []PermissionMode // nil=全档
	Blurb          string
	Hidden         bool
}

// BackendCatalog 全部后端元数据。
var BackendCatalog = []BackendCatalogEntry{
	{
		ID:          DEFAULT_BACKEND_ID,
		AgentFamily: FamilyCodex,
		DisplayName: "Codex",
		Access:      AccessAppServer,
		Dep: BackendDep{
			Kind:       DepExternalCLI,
			Pkg:        "codex",
			DetectHint: "未找到 codex CLI（设 CODEX_BIN、装 Codex.app，或 npm i -g @openai/codex）",
			InstallCmd: "npm i -g @openai/codex（或装 Codex.app / 设 CODEX_BIN），然后 codex login",
		},
		Blurb: "能力最全（goal/steer/compact/resume + 真沙箱只读档）",
	},
	{
		ID:          "claude-agent",
		AgentFamily: FamilyClaude,
		DisplayName: "Claude",
		Access:      AccessACP, // Go 侧经 `claude` CLI 的 stream-json 子进程（等价 ACP）
		Dep: BackendDep{
			Kind:       DepExternalCLI,
			Pkg:        "claude",
			BinName:    "claude",
			DetectHint: "未找到 claude CLI（安装 Claude Code：`npm i -g @anthropic-ai/claude-code`，或下载 Claude Code）",
			InstallCmd: "npm i -g @anthropic-ai/claude-code，然后 claude 登录",
		},
		SupportedModes: []PermissionMode{PermissionQA, PermissionWrite, PermissionFull},
		Blurb:          "Claude Code（复用本机登录；qa/write 走 Claude 沙箱，能力较 Codex 精简）",
	},
}

// IsInstallable 是否可一键按需下载（仅 npm-ondemand）。
func IsInstallable(e BackendCatalogEntry) bool { return e.Dep.Kind == DepNpmOnDemand }

// VisibleCatalog 用户可见 catalog（滤掉 Hidden）。
func VisibleCatalog() []BackendCatalogEntry {
	return visibleFromList(BackendCatalog)
}

func visibleFromList(list []BackendCatalogEntry) []BackendCatalogEntry {
	out := make([]BackendCatalogEntry, 0, len(list))
	for _, e := range list {
		if !e.Hidden {
			out = append(out, e)
		}
	}
	return out
}

// CatalogByID 按 id 取条目。
func CatalogByID(id string) (BackendCatalogEntry, bool) {
	for _, e := range BackendCatalog {
		if e.ID == id {
			return e, true
		}
	}
	return BackendCatalogEntry{}, false
}

// CatalogByFamily 按家族取条目。
func CatalogByFamily(family AgentFamily) []BackendCatalogEntry {
	var out []BackendCatalogEntry
	for _, e := range BackendCatalog {
		if e.AgentFamily == family {
			out = append(out, e)
		}
	}
	return out
}

// CatalogBackendIDs 全部后端 id。
func CatalogBackendIDs() []string {
	out := make([]string, 0, len(BackendCatalog))
	for _, e := range BackendCatalog {
		out = append(out, e.ID)
	}
	return out
}

// ProjectCreatableBackends 新建项目可选后端。
//   - codex（DEFAULT_BACKEND_ID）恒可选（external-cli 基线）；
//   - 其余已装才列（isInstalled 注入，本函数不碰文件系统）；
//   - 按项目权限档过滤；
//   - Hidden 不进 picker。
func ProjectCreatableBackends(mode PermissionMode, isInstalled func(BackendCatalogEntry) bool) []BackendCatalogEntry {
	return projectCreatableFromList(BackendCatalog, mode, isInstalled)
}

func projectCreatableFromList(list []BackendCatalogEntry, mode PermissionMode, isInstalled func(BackendCatalogEntry) bool) []BackendCatalogEntry {
	var out []BackendCatalogEntry
	for _, e := range list {
		if e.Hidden {
			continue
		}
		installed := e.ID == DEFAULT_BACKEND_ID || isInstalled(e)
		if !installed {
			continue
		}
		if len(e.SupportedModes) > 0 && !modeIn(e.SupportedModes, mode) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func modeIn(modes []PermissionMode, m PermissionMode) bool {
	for _, x := range modes {
		if x == m {
			return true
		}
	}
	return false
}
