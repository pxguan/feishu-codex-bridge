package claude

// backend.go —— claude 后端（ClaudeBackend）实现 agent.AgentBackend。
// Doctor 探 claude CLI；ListModels 用静态目录；ListThreads/ReadHistory 读 ~/.claude/projects；
// StartThread/ResumeThread 构造 ClaudeThread（首个 turn 捕获 sessionId）。

import (
	"context"
	"errors"
	"sync"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// ClaudeBackend claude CLI 后端。
type ClaudeBackend struct {
	modelMu    sync.Mutex
	modelCache []agent.ModelInfo
}

func (b *ClaudeBackend) ID() string { return "claude-agent" }

func (b *ClaudeBackend) DisplayName() string { return "Claude" }

// capabilities 与 TS 一致：goal/compact/resume 支持；steer/approvals 不支持。
func (b *ClaudeBackend) Capabilities() agent.AgentCapabilities {
	return agent.AgentCapabilities{Goal: true, Steer: false, Compact: true, Resume: true, Approvals: false}
}

func (b *ClaudeBackend) SupportedModes() []agent.PermissionMode {
	return []agent.PermissionMode{agent.PermissionQA, agent.PermissionWrite, agent.PermissionFull}
}

func (b *ClaudeBackend) IsAvailable(ctx context.Context) bool {
	return b.Doctor(ctx, false).Ok
}

// Doctor 探测 claude 运行时（二进制 + 版本）；绝不抛错。
func (b *ClaudeBackend) Doctor(_ context.Context, force bool) agent.BackendProbe {
	bin := ResolveClaudeBin(force)
	if bin == "" {
		return agent.BackendProbe{
			Installable: true,
			DepState:    "not-installed",
			Hint:        "未找到 claude CLI（安装 Claude Code：`npm i -g @anthropic-ai/claude-code`，或下载 Claude Code 后登录）",
		}
	}
	version := ClaudeVersion(bin, force)
	if version == "" {
		return agent.BackendProbe{Location: bin, Hint: "claude --version 执行失败（" + bin + "）"}
	}
	return agent.BackendProbe{
		Ok:       true,
		Version:  version,
		Location: bin,
		DepState: "installed",
		Hint:     "复用本机 Claude 登录态（未登录请先 `claude` 登录，或设置 ANTHROPIC_API_KEY）",
	}
}

// ListModels 静态模型目录（claude CLI 接受这些 id）。
func (b *ClaudeBackend) ListModels(ctx context.Context) ([]agent.ModelInfo, error) {
	return StaticModels, nil
}

// ListThreads 最近会话（newest first），读 ~/.claude/projects/<cwd-hash> 的 JSONL 存储——
// 与 `claude -r` 同源，故能列出本机用 `claude` 手开的会话。绝不抛错（契约）。
func (b *ClaudeBackend) ListThreads(ctx context.Context, cwd string, limit int) ([]agent.ThreadSummary, error) {
	if limit <= 0 {
		limit = 15
	}
	out, err := listClaudeSessions(cwd, limit)
	if err != nil {
		return nil, nil
	}
	return out, nil
}

// ReadHistory 某会话的转写摘要（resume 历史卡）——读 JSONL 折叠成 turns，不起会话、无 token 成本。
// 绝不抛错（返回空）。
func (b *ClaudeBackend) ReadHistory(ctx context.Context, cwd, sessionID string, maxTurns int) (agent.ThreadHistory, error) {
	if maxTurns <= 0 {
		maxTurns = 10
	}
	h, err := readClaudeHistory(cwd, sessionID, maxTurns)
	if err != nil {
		return agent.ThreadHistory{}, nil
	}
	return h, nil
}

// StartThread 启动新会话（首轮捕获 sessionId）。
func (b *ClaudeBackend) StartThread(ctx context.Context, opts agent.StartThreadOptions) (agent.AgentThread, error) {
	bin := ResolveClaudeBin(false)
	if bin == "" {
		return nil, errors.New("claude CLI not found (install @anthropic-ai/claude-code and run `claude`)")
	}
	perms := permissionFlags(opts.Mode, opts.Network)
	return NewClaudeThread(bin, opts.Cwd, opts.Model, opts.Effort, perms, BridgeDeveloperInstructions), nil
}

// ResumeThread resume 会话（直接带上 sessionId）。
func (b *ClaudeBackend) ResumeThread(ctx context.Context, opts agent.ResumeThreadOptions) (agent.AgentThread, error) {
	bin := ResolveClaudeBin(false)
	if bin == "" {
		return nil, errors.New("claude CLI not found (install @anthropic-ai/claude-code and run `claude`)")
	}
	perms := permissionFlags(opts.Mode, opts.Network)
	th := NewClaudeThread(bin, opts.Cwd, opts.Model, opts.Effort, perms, BridgeDeveloperInstructions)
	th.setSessionID(opts.SessionID)
	return th, nil
}
