package bot

import (
	"context"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/feishu"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// TestHandleBotDeleted_Unbinds 验证机器人被移出群时自动解绑对应项目。
func TestHandleBotDeleted_Unbinds(t *testing.T) {
	o := newTestOrchestrator(t)
	o.Channel = &fakeCreator{}
	if err := o.ProjectStore.Add(project.Project{Name: "bound", ChatID: "oc_x", Backend: "codex"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := o.HandleBotDeleted(context.Background(), "oc_x", "ou_op"); err != nil {
		t.Fatalf("HandleBotDeleted err: %v", err)
	}
	if p, _ := o.ProjectStore.GetByChatID("oc_x"); p != nil {
		t.Fatalf("项目应已解绑，但仍在：%s", p.Name)
	}
}

// TestHandleBotAdded_AlreadyBound 验证已绑定群不重复弹绑定表单（走群内提示，不 panic）。
func TestHandleBotAdded_AlreadyBound(t *testing.T) {
	o := newTestOrchestrator(t)
	o.Channel = &fakeCreator{}
	if err := o.ProjectStore.Add(project.Project{Name: "bound", ChatID: "oc_y", Backend: "codex"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// SendCardFunc/SendDMCardFunc 均未注入 → 两个分支都静默降级，验证不 panic。
	if err := o.HandleBotAdded(context.Background(), "oc_y", "ou_op", "群Y"); err != nil {
		t.Fatalf("HandleBotAdded err: %v", err)
	}
}

// TestMemberInputsForProject_FiltersNonHuman 验证只保留 open_id 成员。
func TestMemberInputsForProject_FiltersNonHuman(t *testing.T) {
	o := newTestOrchestrator(t)
	fc := &fakeCreatorWithMembers{members: []feishu.ChatMemberInfo{
		{MemberID: "ou_alice", Name: "Alice"},
		{MemberID: "ou_bob", Name: "Bob"},
		{MemberID: "app_abc", Name: "Bot"}, // 机器人自身，应被过滤
	}}
	o.Channel = fc
	if err := o.ProjectStore.Add(project.Project{Name: "p", ChatID: "oc_m", Backend: "codex"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	got := o.memberInputsForProject(context.Background(), "p")
	if len(got) != 2 {
		t.Fatalf("期望 2 个人类成员，实际 %d: %+v", len(got), got)
	}
	if got[0].OpenID != "ou_alice" || got[1].OpenID != "ou_bob" {
		t.Fatalf("成员顺序/内容不符：%+v", got)
	}
}

// fakeCreatorWithMembers 返回预置成员列表的 ChatCreator（测试用）。
type fakeCreatorWithMembers struct {
	fakeCreator
	members []feishu.ChatMemberInfo
}

func (f *fakeCreatorWithMembers) GetChatMembers(ctx context.Context, chatID string) ([]feishu.ChatMemberInfo, error) {
	return f.members, nil
}
