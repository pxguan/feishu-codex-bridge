package admin

// ops.go —— 管理面写操作层（对齐 TS admin/ops）。
// DM 卡片回调与 Web 控制台（未来）都只走这里写 projects.json——同一套校验/落盘/驱逐。
// 依赖注入：backendFor 创建后端、evict 驱逐活跃会话（orchestrator 进程内状态，必须在 bot 进程执行）。

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// OpKind 写操作类型。
type OpKind string

const (
	OpSwitchBackend     OpKind = "switchBackend"
	OpSetPermissionMode OpKind = "setPermissionMode"
	OpSetNoMention      OpKind = "setNoMention"
	OpSetAutoCompact    OpKind = "setAutoCompact"
)

// WriteOp Web/IPC 写操作的序列化形态。
type WriteOp struct {
	Kind      OpKind               `json:"kind"`
	Project   string               `json:"project"`
	Backend   string               `json:"backend,omitempty"`   // switchBackend
	Mode      agent.PermissionMode `json:"mode,omitempty"`      // setPermissionMode
	GuestMode agent.PermissionMode `json:"guestMode,omitempty"` // setPermissionMode
	Network   *bool                `json:"network,omitempty"`   // setPermissionMode
	On        bool                 `json:"on,omitempty"`        // setNoMention/setAutoCompact
}

// WriteOutcome perform* 的统一返回：ok 带写后回读的项目；!ok 带中文拒因。不抛错。
type WriteOutcome struct {
	Ok      bool
	Project *project.Project
	Reason  string
}

// WriteError 写操作被校验拒绝（HTTP 409 / IPC code 还原）。
type WriteError struct{ Reason string }

func (e *WriteError) Error() string { return e.Reason }

// BackendProbeRow 后端检测行（probeBackends 返回）。
type BackendProbeRow struct {
	ID             string
	Name           string
	Probe          *agent.BackendProbe
	SupportedModes []agent.PermissionMode
}

var allTiers = []agent.PermissionMode{agent.PermissionQA, agent.PermissionWrite, agent.PermissionFull}

// TierLabel 权限档中文标签（qa→项目内只读 / write→项目内读写 / full→完全访问）。
func TierLabel(m agent.PermissionMode) string {
	switch m {
	case agent.PermissionQA:
		return "项目内只读"
	case agent.PermissionWrite:
		return "项目内读写"
	case agent.PermissionFull:
		return "完全访问"
	}
	return string(m)
}

// ValidateBackendSwitch 项目后端切换的纯校验：
// ① 目标 id 在注册表；② doctor 探测通过；③ 项目两档权限都在目标后端支持面内。
// 全过返回空；否则返回中文原因。supportedModes nil = 全支持（codex）。
func ValidateBackendSwitch(target string, registered []string, proj project.Project, supportedModes []agent.PermissionMode, probe *agent.BackendProbe) string {
	found := false
	for _, id := range registered {
		if id == target {
			found = true
			break
		}
	}
	if !found {
		return fmt.Sprintf("未知后端「%s」（可用：%s）", target, joinIDs(registered))
	}
	if probe == nil || !probe.Ok {
		hint := "环境探测失败（未安装或未登录）"
		if probe != nil && probe.Hint != "" {
			hint = probe.Hint
		}
		return fmt.Sprintf("后端「%s」当前不可用：%s", target, hint)
	}
	if supportedModes != nil {
		mode := project.EffectiveMode(proj)
		guest := project.EffectiveGuestMode(proj)
		tiers := uniqueTiers(mode, guest)
		for _, t := range tiers {
			if !tierIn(supportedModes, t) {
				return fmt.Sprintf("该后端仅支持 %s 权限档，本项目当前为 %s —— 请先在「🔐 权限」把两档都调整到支持的档位再切换。",
					joinTiers(supportedModes), joinTiers(tiers))
			}
		}
	}
	return ""
}

// ProbeBackends 并行 doctor 探测全部后端（单超时，超时/抛错归一成 probe=nil）。
func ProbeBackends(ctx context.Context, backends []agent.AgentBackend, timeout time.Duration) []BackendProbeRow {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	rows := make([]BackendProbeRow, len(backends))
	var wg sync.WaitGroup
	for i, be := range backends {
		i, be := i, be
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			probe := be.Doctor(pctx, true)
			rows[i] = BackendProbeRow{ID: be.ID(), Name: be.DisplayName(), Probe: &probe, SupportedModes: be.SupportedModes()}
		}()
	}
	wg.Wait()
	return rows
}

// Deps 写操作的进程内依赖（注入）。
type Deps struct {
	Store                    *project.Store
	BackendFor               func(id string) (agent.AgentBackend, error)
	EvictLiveSessionsForChat func(chatID string)
}

// RunWriteOp AdminWriteOp 分发 → 对应 perform*。
func RunWriteOp(ctx context.Context, op WriteOp, deps Deps) WriteOutcome {
	switch op.Kind {
	case OpSetNoMention:
		return PerformSetNoMention(deps.Store, op.Project, op.On)
	case OpSetAutoCompact:
		return PerformSetAutoCompact(deps.Store, op.Project, op.On, deps.EvictLiveSessionsForChat)
	case OpSetPermissionMode:
		return PerformSetPermissionMode(deps.Store, op.Project, op.Mode, op.GuestMode, op.Network, deps.EvictLiveSessionsForChat)
	case OpSwitchBackend:
		return PerformBackendSwitch(ctx, deps.Store, op.Project, op.Backend, deps.BackendFor)
	}
	return WriteOutcome{Ok: false, Reason: "未知写操作类型：" + string(op.Kind)}
}

// CreateWriteExecutor Web/IPC 入口：op → outcome，!ok 抛 WriteError。
func CreateWriteExecutor(deps Deps) func(context.Context, WriteOp) error {
	return func(ctx context.Context, op WriteOp) error {
		outcome := RunWriteOp(ctx, op, deps)
		if !outcome.Ok {
			return &WriteError{Reason: outcome.Reason}
		}
		return nil
	}
}

// PerformSetNoMention ✋ 免@ 开关（即时生效，无需驱逐）。
func PerformSetNoMention(store *project.Store, projectName string, on bool) WriteOutcome {
	p, err := store.GetByName(projectName)
	if err != nil || p == nil {
		return WriteOutcome{Ok: false, Reason: "项目「" + projectName + "」不存在"}
	}
	if err := store.Update(projectName, func(proj *project.Project) { proj.NoMention = boolPtr(on) }); err != nil {
		return WriteOutcome{Ok: false, Reason: err.Error()}
	}
	updated := *p
	updated.NoMention = boolPtr(on)
	return WriteOutcome{Ok: true, Project: &updated}
}

// PerformSetAutoCompact 🗜️ 自动压缩开关（落盘后驱逐让下一条消息重绑）。
func PerformSetAutoCompact(store *project.Store, projectName string, on bool, evict func(string)) WriteOutcome {
	p, err := store.GetByName(projectName)
	if err != nil || p == nil {
		return WriteOutcome{Ok: false, Reason: "项目「" + projectName + "」不存在"}
	}
	if err := store.Update(projectName, func(proj *project.Project) { proj.AutoCompact = boolPtr(on) }); err != nil {
		return WriteOutcome{Ok: false, Reason: err.Error()}
	}
	if evict != nil {
		evict(p.ChatID)
	}
	updated := *p
	updated.AutoCompact = boolPtr(on)
	return WriteOutcome{Ok: true, Project: &updated}
}

// PerformSetModelDefault 🤖 设置新话题默认模型/推理强度（不驱逐，只管新会话）。
func PerformSetModelDefault(store *project.Store, projectName, model string, effort agent.ReasoningEffort) WriteOutcome {
	p, err := store.GetByName(projectName)
	if err != nil || p == nil {
		return WriteOutcome{Ok: false, Reason: "项目「" + projectName + "」不存在"}
	}
	if err := store.Update(projectName, func(proj *project.Project) {
		proj.DefaultModel = model
		proj.DefaultEffort = effort
	}); err != nil {
		return WriteOutcome{Ok: false, Reason: err.Error()}
	}
	updated := *p
	updated.DefaultModel = model
	updated.DefaultEffort = effort
	return WriteOutcome{Ok: true, Project: &updated}
}

// PerformSetPermissionMode 🔐 权限档（落盘 mode/guestMode/network + 驱逐活跃会话）。
func PerformSetPermissionMode(store *project.Store, projectName string, mode, guestMode agent.PermissionMode, network *bool, evict func(string)) WriteOutcome {
	for _, v := range []agent.PermissionMode{mode, guestMode} {
		if v != "" && !tierIn(allTiers, v) {
			return WriteOutcome{Ok: false, Reason: "未知权限档「" + string(v) + "」"}
		}
	}
	p, err := store.GetByName(projectName)
	if err != nil || p == nil {
		return WriteOutcome{Ok: false, Reason: "项目「" + projectName + "」不存在"}
	}
	// 后端档位兼容守门：后端创建时固定，但权限档可改；改到该后端 supportedModes 外会卡死。
	if entry, ok := agent.CatalogByID(p.Backend); ok && len(entry.SupportedModes) > 0 {
		resMode := mode
		if resMode == "" {
			resMode = project.EffectiveMode(*p)
		}
		resGuest := guestMode
		if resGuest == "" {
			resGuest = project.EffectiveGuestMode(*p)
		}
		for _, t := range uniqueTiers(resMode, resGuest) {
			if !tierIn(entry.SupportedModes, t) {
				return WriteOutcome{Ok: false, Reason: fmt.Sprintf("项目的后端「%s」仅支持 %s 权限档，无法改到「%s」。", entry.DisplayName, joinTiers(entry.SupportedModes), TierLabel(t))}
			}
		}
	}
	if err := store.Update(projectName, func(proj *project.Project) {
		if mode != "" {
			proj.Mode = mode
		}
		if guestMode != "" {
			proj.GuestMode = guestMode
		}
		if network != nil {
			proj.Network = network
		}
	}); err != nil {
		return WriteOutcome{Ok: false, Reason: err.Error()}
	}
	if evict != nil {
		evict(p.ChatID)
	}
	updated := *p
	if mode != "" {
		updated.Mode = mode
	}
	if guestMode != "" {
		updated.GuestMode = guestMode
	}
	if network != nil {
		updated.Network = network
	}
	return WriteOutcome{Ok: true, Project: &updated}
}

// PerformBackendSwitch 🧠 切换项目后端（写盘前再 doctor force 探一次；不驱逐活跃会话）。
func PerformBackendSwitch(ctx context.Context, store *project.Store, projectName, target string, backendFor func(string) (agent.AgentBackend, error)) WriteOutcome {
	p, err := store.GetByName(projectName)
	if err != nil || p == nil {
		return WriteOutcome{Ok: false, Reason: "项目「" + projectName + "」不存在"}
	}
	// 后端运行时固定（仅 legacy 一次性落地 + 同值 no-op）。
	if p.Backend != "" && p.Backend != target {
		return WriteOutcome{Ok: false, Reason: "该项目的后端已在创建时选定，运行时固定、不支持切换。如需更改，请删除该项目后用新后端重新创建。"}
	}
	registered := agent.CatalogBackendIDs()
	be, err := backendFor(target)
	if err != nil {
		return WriteOutcome{Ok: false, Reason: ValidateBackendSwitch(target, registered, *p, nil, nil)}
	}
	probe := be.Doctor(ctx, true)
	supported := be.SupportedModes()
	if reason := ValidateBackendSwitch(target, registered, *p, supported, &probe); reason != "" {
		return WriteOutcome{Ok: false, Reason: reason}
	}
	if err := store.Update(projectName, func(proj *project.Project) { proj.Backend = target }); err != nil {
		return WriteOutcome{Ok: false, Reason: err.Error()}
	}
	updated := *p
	updated.Backend = target
	return WriteOutcome{Ok: true, Project: &updated}
}

// ── 小工具 ───────────────────────────────────────────────────────

func boolPtr(b bool) *bool { return &b }

func tierIn(modes []agent.PermissionMode, m agent.PermissionMode) bool {
	for _, x := range modes {
		if x == m {
			return true
		}
	}
	return false
}

func uniqueTiers(tiers ...agent.PermissionMode) []agent.PermissionMode {
	seen := map[agent.PermissionMode]bool{}
	out := []agent.PermissionMode{}
	for _, t := range tiers {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func joinTiers(tiers []agent.PermissionMode) string {
	labels := make([]string, 0, len(tiers))
	for _, t := range tiers {
		labels = append(labels, TierLabel(t))
	}
	return joinStr(labels, " / ")
}

func joinIDs(ids []string) string { return joinStr(ids, "、") }

func joinStr(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += sep + s
	}
	return out
}
