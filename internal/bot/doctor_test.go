package bot

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/utils"
)

// fakeConnState 实现 ConnState() string，供 handleDoctor 取真实连接态。
type fakeConnState struct{ state string }

func (f fakeConnState) ConnState() string { return f.state }

func doctorOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	o := newTestOrchestrator(t)
	o.Cfg.Accounts.App = config.AppCredentials{ID: "cli_test", Secret: config.PlainSecret("x"), Tenant: config.TenantLark}
	o.Cfg.Preferences = &config.AppPreferences{
		Access: &config.AppAccess{OwnerOpenID: "ou_owner", Admins: []string{"ou_admin1"}},
	}
	o.Channel = fakeConnState{state: "connected"}
	return o
}

// withDoctorStubs 把 validateCreds / diagnoseEvents / detectAgents 换成测试桩。
func withDoctorStubs(t *testing.T) {
	t.Helper()
	origV, origD, origA := validateCreds, diagnoseEvents, detectAgents
	t.Cleanup(func() { validateCreds, diagnoseEvents, detectAgents = origV, origD, origA })

	validateCreds = func(ctx context.Context, appID, appSecret string, tenant config.TenantBrand, hc *http.Client) utils.ValidationResult {
		return utils.ValidationResult{BotOpenID: "ou_bot", MissingScopes: []string{"im:resource"}, MissingJoinScopes: nil}
	}
	_ = origV
	diagnoseEvents = func(ctx context.Context, appID, appSecret string, tenant config.TenantBrand, hc *http.Client) utils.EventDiagnosis {
		return utils.EventDiagnosis{State: utils.EventDiagnosisOK, Version: "9.9.9"}
	}
	detectAgents = func() []agent.AgentRuntime {
		return []agent.AgentRuntime{{ID: string(agent.FamilyCodex), Installed: true, Version: "1.2.3"}}
	}
}

func TestHandleDoctor_RealProbe(t *testing.T) {
	withDoctorStubs(t)
	o := doctorOrchestrator(t)

	var got []byte
	o.SendCardFunc = func(ctx context.Context, chatID string, cb []byte) (string, error) {
		got = cb
		return "", nil
	}

	cca := card.CardActionContext{Ctx: context.Background()}
	cca.Evt = &card.CardActionEvent{}
	cca.Evt.Operator.OpenID = "ou_admin1"
	cca.Evt.ChatID = "oc_test"

	o.handleDoctor(cca)

	if len(got) == 0 {
		t.Fatal("未发出诊断卡")
	}
	s := string(got)
	checks := map[string]string{
		"codex 版本 1.2.3":        "1.2.3",
		"真实连接态(已连接)":       "已连接",
		"bot open_id ou_bot":      "ou_bot",
		"事件诊断版本 9.9.9":       "9.9.9",
		"缺失 scope 提示 im:resource": "im:resource",
		"权限开通按钮":            "去开通",
	}
	for name, want := range checks {
		if !strings.Contains(s, want) {
			t.Fatalf("诊断卡缺少 %q（期望含 %q）：\n%s", name, want, s)
		}
	}
}

func TestHandleDoctor_UnknownConnDegrades(t *testing.T) {
	withDoctorStubs(t)
	o := doctorOrchestrator(t)
	o.Channel = fakeConnState{state: "unknown"} // Channel 返回未知态，不应崩溃

	var got []byte
	o.SendCardFunc = func(ctx context.Context, chatID string, cb []byte) (string, error) {
		got = cb
		return "", nil
	}
	cca := card.CardActionContext{Ctx: context.Background()}
	cca.Evt = &card.CardActionEvent{}
	cca.Evt.Operator.OpenID = "ou_admin1"
	cca.Evt.ChatID = "oc_test"

	o.handleDoctor(cca) // 必须不 panic
	if len(got) == 0 {
		t.Fatal("unknown 连接态下仍应发出诊断卡")
	}
}
