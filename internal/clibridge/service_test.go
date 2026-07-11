package clibridge

// service_test.go —— cli-bridge Service 编排层单测（纯逻辑，注入 deps，无真实飞书/ioreg）。
// 验证：审批放行 / 拒绝、功能关闭回退、本机活跃不弹卡、AskUserQuestion 表单提交。

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

func boolPtr(b bool) *bool { return &b }

// newTestService 构造一个注入 deps 的服务；presence 默认「离开→路由飞书」，
// localActivity 默认「非本机活跃」（不会自动本地回归）。
func newTestService(owner string, cliEnabled bool, presence func() (CliPresenceRoute, error)) (*Service, *[]card.CardObject) {
	sent := &[]card.CardObject{}
	deps := ServiceDeps{
		Cfg: config.AppConfig{
			Preferences: &config.AppPreferences{
				Access:    &config.AppAccess{OwnerOpenID: owner},
				CliBridge: &config.CliBridgePreferences{Enabled: boolPtr(cliEnabled)},
			},
		},
		SocketPath: "/tmp/clibridge-test.sock",
		SendOwnerCard: func(_ context.Context, c card.CardObject) (string, error) {
			*sent = append(*sent, c)
			return "msg-" + strconv.Itoa(len(*sent)), nil
		},
		UpdateOwnerCard: func(_ context.Context, _ string, _ card.CardObject) bool { return true },
		SendGroupTopic:  func(_ context.Context, _ string, _ string, _ bool) error { return nil },
		AddTypingReaction:    func(_ context.Context, _ string) (string, error) { return "rid", nil },
		RemoveTypingReaction: func(_ context.Context, _ string, _ string) error { return nil },
		IsBoundProject:       func(_ string) bool { return true },
		FindProjectByCwd:     func(_ string) (*ProjectRef, error) { return nil, nil },
		CreateProjectForCwd:  func(_ string, _ string) (*ProjectRef, error) { return nil, nil },
		Presence:             presence,
		LocalActivity:        func() (bool, error) { return false, nil },
	}
	return CreateCliBridgeService(deps), sent
}

// waitForPending 轮询 package store，等某类 pending 出现并拷贝出来。
func waitForPending(t *testing.T, kind PendingCliKind) *PendingCliInteraction {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		for _, item := range store.pending {
			if item.Kind == kind {
				cp := *item
				store.mu.Unlock()
				return &cp
			}
		}
		store.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s pending", kind)
	return nil
}

// handleAsync 在 goroutine 里跑会阻塞的 HandleMessage，返回结果 channel。
func handleAsync(svc *Service, msg CliHookMessage) <-chan struct {
	resp CliHookResponse
	err  error
} {
	ch := make(chan struct {
		resp CliHookResponse
		err  error
	}, 1)
	go func() {
		r, e := svc.HandleMessage(msg)
		ch <- struct {
			resp CliHookResponse
			err  error
		}{r, e}
	}()
	return ch
}

func awayPresence() (CliPresenceRoute, error) {
	return CliPresenceRoute{RouteToFeishu: true, Reason: "away"}, nil
}

func TestServiceApprovalApproveFlow(t *testing.T) {
	svc, sent := newTestService("ou_owner", true, awayPresence)
	msg := CliHookMessage{
		Type:          MsgTypePermissionRequest,
		Source:        AgentClaude,
		SessionID:     "sess-1",
		Cwd:           "/proj",
		ToolName:      "Bash",
		ToolInput:     map[string]any{"command": "rm -rf /tmp/x"},
		HookEventName: "PreToolUse",
	}
	resCh := handleAsync(svc, msg)
	pending := waitForPending(t, PendingPermission)
	if !svc.resolveAction(CLI.ApproveOnce, pending.ID) {
		t.Fatal("resolveAction(ApproveOnce) returned false")
	}
	res := <-resCh
	if res.err != nil {
		t.Fatalf("HandleMessage error: %v", res.err)
	}
	if res.resp.Decision != DecisionAllow {
		t.Fatalf("expected allow, got %q (reason=%q)", res.resp.Decision, res.resp.Reason)
	}
	// 至少发了审批卡（离开态还会先发一条 away notice）。
	if len(*sent) == 0 {
		t.Fatal("expected at least one owner card to be sent")
	}
}

func TestServiceApprovalDenyFlow(t *testing.T) {
	svc, _ := newTestService("ou_owner", true, awayPresence)
	msg := CliHookMessage{
		Type:          MsgTypePermissionRequest,
		Source:        AgentClaude,
		SessionID:     "sess-2",
		Cwd:           "/proj",
		ToolName:      "Bash",
		ToolInput:     map[string]any{"command": "git push --force"},
		HookEventName: "PreToolUse",
	}
	resCh := handleAsync(svc, msg)
	pending := waitForPending(t, PendingPermission)
	if !svc.resolveAction(CLI.Deny, pending.ID) {
		t.Fatal("resolveAction(Deny) returned false")
	}
	res := <-resCh
	if res.resp.Decision != DecisionDeny {
		t.Fatalf("expected deny, got %q", res.resp.Decision)
	}
	if !res.resp.Interrupt {
		t.Fatal("expected Interrupt=true on deny")
	}
	if res.resp.Reason != "Denied from Feishu" {
		t.Fatalf("unexpected reason: %q", res.resp.Reason)
	}
}

func TestServiceDisabledFallsBackLocal(t *testing.T) {
	// cliBridge 未开启：HandleMessage 应同步回退 local，不发任何卡。
	svc, sent := newTestService("ou_owner", false, awayPresence)
	msg := CliHookMessage{
		Type:      MsgTypePermissionRequest,
		Source:    AgentClaude,
		SessionID: "sess-3",
		Cwd:       "/proj",
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": "ls"},
	}
	resp, err := svc.HandleMessage(msg)
	if err != nil {
		t.Fatalf("HandleMessage error: %v", err)
	}
	if resp.Decision != DecisionFallbackLocal {
		t.Fatalf("expected fallback_local when disabled, got %q", resp.Decision)
	}
	if len(*sent) != 0 {
		t.Fatalf("disabled service must not send cards, sent %d", len(*sent))
	}
}

func TestServiceLocalActiveNoCard(t *testing.T) {
	// 人在本机活跃：直接回退 local，不应弹飞书卡。
	svc, sent := newTestService("ou_owner", true, func() (CliPresenceRoute, error) {
		return CliPresenceRoute{RouteToFeishu: false, Reason: "local_active"}, nil
	})
	msg := CliHookMessage{
		Type:      MsgTypePermissionRequest,
		Source:    AgentClaude,
		SessionID: "sess-4",
		Cwd:       "/proj",
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": "ls"},
	}
	resp, err := svc.HandleMessage(msg)
	if err != nil {
		t.Fatalf("HandleMessage error: %v", err)
	}
	if resp.Decision != DecisionFallbackLocal || resp.Reason != "local_active" {
		t.Fatalf("expected fallback_local/local_active, got %q/%q", resp.Decision, resp.Reason)
	}
	if len(*sent) != 0 {
		t.Fatalf("local_active must not send cards, sent %d", len(*sent))
	}
}

func TestServiceAskUserQuestionSubmit(t *testing.T) {
	svc, _ := newTestService("ou_owner", true, awayPresence)
	msg := CliHookMessage{
		Type:      MsgTypePermissionRequest,
		Source:    AgentClaude,
		SessionID: "sess-5",
		Cwd:       "/proj",
		ToolName:  "AskUserQuestion",
		ToolInput: map[string]any{
			"questions": []any{
				map[string]any{
					"question": "选哪个方案？",
					"header":   "方案",
					"multiSelect": false,
					"options": []any{
						map[string]any{"label": "A", "description": "方案A"},
						map[string]any{"label": "B", "description": "方案B"},
					},
				},
			},
		},
		HookEventName: "PreToolUse",
	}
	resCh := handleAsync(svc, msg)
	pending := waitForPending(t, PendingQuestion)
	if len(pending.Questions) != 1 {
		t.Fatalf("expected 1 parsed question, got %d", len(pending.Questions))
	}
	formValue := map[string]any{
		QuestionChoiceField(0): "A",
	}
	if !svc.resolveQuestionSubmit(pending.ID, formValue) {
		t.Fatal("resolveQuestionSubmit returned false")
	}
	res := <-resCh
	if res.err != nil {
		t.Fatalf("HandleMessage error: %v", res.err)
	}
	if res.resp.Decision != DecisionAllow {
		t.Fatalf("expected allow, got %q", res.resp.Decision)
	}
	answers, ok := res.resp.UpdatedInput["answers"].(map[string]string)
	if !ok {
		t.Fatalf("expected answers map in UpdatedInput, got %T", res.resp.UpdatedInput["answers"])
	}
	if answers["选哪个方案？"] != "A" {
		t.Fatalf("unexpected answer: %v", answers)
	}
}
