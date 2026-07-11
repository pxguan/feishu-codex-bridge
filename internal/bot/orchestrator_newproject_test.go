package bot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/feishu"
)

// fakeCreator 实现 ChatCreator（建群返回固定 chat_id）。
type fakeCreator struct {
	createdName  string
	createdOwner string
	chatID       string
}

func (f *fakeCreator) CreateChat(ctx context.Context, name, ownerOpenID string) (string, error) {
	f.createdName = name
	f.createdOwner = ownerOpenID
	if f.chatID == "" {
		f.chatID = "oc_test_" + name
	}
	return f.chatID, nil
}

func (f *fakeCreator) AddManagers(ctx context.Context, chatID string, managerIDs []string) error {
	return nil
}

func (f *fakeCreator) GetChatMembers(ctx context.Context, chatID string) ([]feishu.ChatMemberInfo, error) {
	return nil, nil
}

func (f *fakeCreator) TransferOwner(ctx context.Context, chatID, openID string) error {
	return nil
}

func (f *fakeCreator) LeaveChat(ctx context.Context, chatID string) error {
	return nil
}

func TestHandleNewProjectSubmit_OK(t *testing.T) {
	o := newTestOrchestrator(t)
	fc := &fakeCreator{}
	o.Channel = fc

	var sent []struct {
		chatID string
		json    string
	}
	o.SendCardFunc = func(ctx context.Context, chatID string, jsonBytes []byte) (string, error) {
		sent = append(sent, struct {
			chatID string
			json    string
		}{chatID, string(jsonBytes)})
		return "m_test", nil
	}

	cca := card.CardActionContext{
		Ctx: context.Background(),
		Evt: &card.CardActionEvent{
			ChatID:   "oc_dm",
			Operator: struct{ OpenID string `json:"openId"` }{OpenID: "ou_owner"},
		},
		FormValue: map[string]any{
			"name": "my-project",
			"cwd":  "",
			"mode": "write",
		},
	}

	wait := armNewProjectDone(t)
	if err := o.handleNewProjectSubmit(cca); err != nil {
		t.Fatalf("handleNewProjectSubmit err: %v", err)
	}
	// 建群逻辑已异步执行，等待 goroutine 完成（含发卡 + 存盘）。
	wait()

	// 1. 建群参数正确（owner = 提交人）。
	if fc.createdName != "my-project" || fc.createdOwner != "ou_owner" {
		t.Fatalf("CreateChat 参数错：name=%q owner=%q", fc.createdName, fc.createdOwner)
	}
	// 2. 存盘正确（chatId / mode / cwd 都落了）。
	got, err := o.ProjectStore.GetByName("my-project")
	if err != nil || got == nil {
		t.Fatalf("项目未存盘：err=%v got=%v", err, got)
	}
	if got.ChatID != "oc_test_my-project" || got.Mode != agent.PermissionWrite || got.Cwd == "" {
		t.Fatalf("存盘字段错：%+v", got)
	}
	// 3. 发了两张卡：DM 成功卡 + 新群欢迎卡。
	if len(sent) != 2 {
		t.Fatalf("预期 2 张卡，实际 %d", len(sent))
	}
	if sent[0].chatID != "oc_dm" {
		t.Fatalf("成功卡应发到 DM(oc_dm)，实际 %s", sent[0].chatID)
	}
	// 卡片 JSON 有效（可被反序列化）。
	var doc map[string]any
	if err := json.Unmarshal([]byte(sent[0].json), &doc); err != nil {
		t.Fatalf("DM 卡 JSON 解析失败：%v", err)
	}
}

func TestHandleNewProjectSubmit_DupName(t *testing.T) {
	o := newTestOrchestrator(t)
	o.Channel = &fakeCreator{}

	var sent []struct {
		chatID string
		json    string
	}
	o.SendCardFunc = func(ctx context.Context, chatID string, jsonBytes []byte) (string, error) {
		sent = append(sent, struct {
			chatID string
			json    string
		}{chatID, string(jsonBytes)})
		return "m", nil
	}

	buildCca := func() card.CardActionContext {
		return card.CardActionContext{
			Ctx: context.Background(),
			Evt: &card.CardActionEvent{
				ChatID:   "oc_dm",
				Operator: struct{ OpenID string `json:"openId"` }{OpenID: "ou_owner"},
			},
			FormValue: map[string]any{"name": "dup", "cwd": "", "mode": "full"},
		}
	}

	// 先成功建一个。
	wait := armNewProjectDone(t)
	if err := o.handleNewProjectSubmit(buildCca()); err != nil {
		t.Fatalf("first submit err: %v", err)
	}
	wait() // 等首个异步建群完成（发成功卡 + 欢迎卡 = 2 张）
	// 再建同名 → 应只发失败卡（不发群、不存盘，同步返回）。
	if err := o.handleNewProjectSubmit(buildCca()); err != nil {
		t.Fatalf("second submit err: %v", err)
	}
	// 第二次只发 1 张失败卡 → 累计 3 张（2 + 1）。
	if len(sent) != 3 {
		t.Fatalf("重名应只发失败卡，累计应为 3，实际 %d", len(sent))
	}
	if sent[2].chatID != "oc_dm" {
		t.Fatalf("失败卡应发到 DM，实际 %s", sent[2].chatID)
	}
}

// TestNewProjectSubmit_ViaDispatcher 验证 DMNewProjectSubmit 真的注册在 dispatcher 上，
// 且经 Dispatcher.Handle 端到端能建群（防 action id 写错导致注册落空）。
func TestNewProjectSubmit_ViaDispatcher(t *testing.T) {
	o := newTestOrchestrator(t)
	o.Channel = &fakeCreator{}

	var sent []struct {
		chatID string
		json    string
	}
	o.SendCardFunc = func(ctx context.Context, chatID string, jsonBytes []byte) (string, error) {
		sent = append(sent, struct {
			chatID string
			json    string
		}{chatID, string(jsonBytes)})
		return "m", nil
	}

	evt := &card.CardActionEvent{}
	evt.Action.Value = map[string]any{"a": card.DMNewProjectSubmit}
	evt.ChatID = "oc_dm"
	evt.Operator.OpenID = "ou_owner"
	evt.Raw.Action.FormValue = map[string]any{
		"name": "via-disp",
		"cwd":  "",
		"mode": "full",
	}

	wait := armNewProjectDone(t)
	o.Dispatcher.Handle(context.Background(), evt)
	wait() // 经 dispatcher 异步建群，等待完成

	got, err := o.ProjectStore.GetByName("via-disp")
	if err != nil || got == nil {
		t.Fatalf("经 dispatcher 未建群：err=%v got=%v", err, got)
	}
	if len(sent) < 1 || sent[0].chatID != "oc_dm" {
		t.Fatalf("经 dispatcher 未回成功卡到 DM：sent=%d first=%s", len(sent), safeChat(sent))
	}
}

func safeChat(sent []struct {
	chatID string
	json    string
}) string {
	if len(sent) == 0 {
		return "<none>"
	}
	return sent[0].chatID
}

// armNewProjectDone 在建群 goroutine 启动【前】装好钩子，返回等待函数。
// 必须在调用 handleNewProjectSubmit / Dispatcher.Handle 之前调用，否则极快的 goroutine
// 可能在钩子就绪前就已触发，导致等待超时。
func armNewProjectDone(t *testing.T) func() {
	t.Helper()
	done := make(chan struct{}, 1)
	old := testHookAfterNewProject
	testHookAfterNewProject = func() { done <- struct{}{} }
	return func() {
		defer func() { testHookAfterNewProject = old }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("等待异步建群完成超时（可能 handler 未真正异步执行）")
		}
	}
}
