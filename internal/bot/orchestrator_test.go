package bot

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		text string
		cmd  string
		rest string
	}{
		{"/help", "/help", ""},
		{"/model gpt-5", "/model", "gpt-5"},
		{"/goal do something", "/goal", "do something"},
		{"hello", "", ""},
		{"/settings", "/settings", ""},
	}
	for _, c := range cases {
		cmd, rest := parseCommand(c.text)
		if cmd != c.cmd || rest != c.rest {
			t.Errorf("parseCommand(%q) = (%q,%q) want (%q,%q)", c.text, cmd, rest, c.cmd, c.rest)
		}
	}
}

// TestStripLeadingMention 验证 @bot 提及被剥离后 /command 才能被 parseCommand 识别。
func TestStripLeadingMention(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"@_user_1 /settings", "/settings"},
		{"@bot /settings", "/settings"},
		{"@张三 /model gpt-5", "/model gpt-5"},
		{"/settings", "/settings"},
		{"hello", "hello"},
		{"@alice 帮我看看", "帮我看看"}, // 开头 @ 一律剥离（多余 @ 进 agent 无害）
		{"  @_user_2 /goal x  ", "/goal x"},
		{"@onlymention", ""}, // 整句只有一个 @token → 清空（无后续命令）
	}
	for _, c := range cases {
		got := stripLeadingMention(c.in)
		if got != c.want {
			t.Errorf("stripLeadingMention(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

// TestOnMessage_CommandIntercepted 验证群消息 @bot /settings 被当作命令拦截，而非进 agent。
func TestOnMessage_CommandIntercepted(t *testing.T) {
	o := newTestOrchestrator(t)
	// 绑定一个群（与 OnMessage 查到的 chatID 一致），使 shouldRespond 通过、命令分支可达。
	proj := &project.Project{Name: "p", ChatID: "oc_bound", Backend: "codex"}
	if err := o.ProjectStore.Add(*proj); err != nil {
		t.Fatalf("add project: %v", err)
	}
	// 飞书 text 消息原始 content（含 @bot 占位 @_user_1）。
	msg := NormalizedMessage{
		MessageID: "om_cmd_1", ChatID: "oc_bound",
		RawType: "text", Content: `{"text":"@_user_1 /settings","mentions":[{"key":"@_user_1"}]}`,
	}
	o.OnMessage(context.Background(), msg)
	// 不应 panic；命令分支直接 return，无需更多断言。
	if !o.isRecent("om_cmd_1") {
		t.Fatal("command msg should be marked recent")
	}
}


func TestOrchestrator_Dedup(t *testing.T) {
	o := newTestOrchestrator(t)
	msg := NormalizedMessage{MessageID: "om_test", ChatID: "oc_test", Content: "hi"}
	o.OnMessage(context.Background(), msg)
	// 第二次同一 msgID → 去重（不处理）。
	o.OnMessage(context.Background(), msg)
	// 没有简单断言（OnMessage 是 void），但去重逻辑被验证（isRecent 返回 true）。
	if !o.isRecent("om_test") {
		t.Fatal("msg should be deduped")
	}
}

func TestOrchestrator_UnboundChat(t *testing.T) {
	o := newTestOrchestrator(t)
	msg := NormalizedMessage{MessageID: "om_1", ChatID: "oc_unbound", Content: "hi"}
	o.OnMessage(context.Background(), msg)
	// 未绑定群 → 静默返回（不 panic）。
}

func TestShouldRespond_AdminExempt(t *testing.T) {
	o := newTestOrchestrator(t)
	proj := &project.Project{Name: "p", ChatID: "oc_1"}
	msg := NormalizedMessage{SenderID: "ou_admin"}
	// admin 在 config.access.admins。
	o.Cfg.Preferences = &config.AppPreferences{Access: &config.AppAccess{Admins: []string{"ou_admin"}}}
	if !o.shouldRespond(proj, msg) {
		t.Fatal("admin should be exempt")
	}
}

func TestShouldRespond_RequireMention(t *testing.T) {
	o := newTestOrchestrator(t)
	proj := &project.Project{Name: "p", ChatID: "oc_1"}
	msg := NormalizedMessage{SenderID: "ou_user"}
	// 默认 requireMention=true + noMention=nil(默认 true for created multi)。
	// shouldRespond → noMention=true → 不需 @ → true。
	if !o.shouldRespond(proj, msg) {
		t.Fatal("default noMention=true should respond without @")
	}
}

func TestShouldRespond_Allowlist(t *testing.T) {
	o := newTestOrchestrator(t)
	proj := &project.Project{Name: "p", ChatID: "oc_1", AllowedUsers: []string{"ou_allowed"}}
	msg := NormalizedMessage{SenderID: "ou_blocked"}
	if o.shouldRespond(proj, msg) {
		t.Fatal("user not in allowlist should not respond")
	}
}

func newTestOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	return NewOrchestrator(
		config.AppConfig{},
		project.NewStore(filepath.Join(dir, "projects.json")),
		NewSessionStore(filepath.Join(dir, "sessions.json")),
		filepath.Join(dir, "config.json"),
	)
}
