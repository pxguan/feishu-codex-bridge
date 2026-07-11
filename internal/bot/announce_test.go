package bot

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/utils"
)

// withAnnounceStubs 临时把 diagnoseEvents/pollEvents 换成测试桩，返回注入器。
func withAnnounceStubs(t *testing.T) (setDiag func(utils.EventDiagnosis), setPoll func(*utils.EventDiagnosis)) {
	t.Helper()
	origD, origP := diagnoseEvents, pollEvents
	t.Cleanup(func() { diagnoseEvents, pollEvents = origD, origP })
	var mu sync.Mutex
	var diag utils.EventDiagnosis
	var poll *utils.EventDiagnosis
	diagnoseEvents = func(ctx context.Context, appID, appSecret string, tenant config.TenantBrand, hc *http.Client) utils.EventDiagnosis {
		mu.Lock()
		defer mu.Unlock()
		return diag
	}
	pollEvents = func(ctx context.Context, appID, appSecret string, tenant config.TenantBrand, hc *http.Client, interval, timeout time.Duration) *utils.EventDiagnosis {
		mu.Lock()
		defer mu.Unlock()
		return poll
	}
	return func(d utils.EventDiagnosis) { mu.Lock(); diag = d; mu.Unlock() },
		func(p *utils.EventDiagnosis) { mu.Lock(); poll = p; mu.Unlock() }
}

func announceOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	o := newTestOrchestrator(t)
	o.Cfg.Accounts.App = config.AppCredentials{ID: "cli_test", Secret: config.PlainSecret("x"), Tenant: config.TenantLark}
	o.Cfg.Preferences = &config.AppPreferences{
		Access: &config.AppAccess{OwnerOpenID: "ou_owner", Admins: []string{"ou_admin1", "ou_admin2"}},
	}
	return o
}

func TestAnnounceWhenLive_OK(t *testing.T) {
	setDiag, _ := withAnnounceStubs(t)
	o := announceOrchestrator(t)
	var mu sync.Mutex
	var sent []string
	o.SendDMCardFunc = func(ctx context.Context, openID string, card []byte) (string, error) {
		mu.Lock()
		sent = append(sent, openID)
		mu.Unlock()
		return "", nil
	}
	setDiag(utils.EventDiagnosis{State: utils.EventDiagnosisOK, Version: "1.2.3", Events: []string{"im.message.receive_v1"}})

	o.AnnounceWhenLive(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// owner + 2 admins，去重后 3 张卡。
	if len(sent) != 3 {
		t.Fatalf("期望 3 张播报卡（owner+2 admin），实际 %d: %v", len(sent), sent)
	}
	want := map[string]bool{"ou_owner": true, "ou_admin1": true, "ou_admin2": true}
	for _, id := range sent {
		if !want[id] {
			t.Fatalf("意外接收者 %s", id)
		}
	}
}

func TestAnnounceWhenLive_MissingThenPolledOK(t *testing.T) {
	setDiag, setPoll := withAnnounceStubs(t)
	o := announceOrchestrator(t)
	var mu sync.Mutex
	var sent []string
	done := make(chan struct{}, 1)
	o.SendDMCardFunc = func(ctx context.Context, openID string, card []byte) (string, error) {
		mu.Lock()
		sent = append(sent, openID)
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return "", nil
	}
	setDiag(utils.EventDiagnosis{State: utils.EventDiagnosisMissing, Version: "1.0.0", MissingRequired: []string{"im.message.receive_v1"}})
	setPoll(&utils.EventDiagnosis{State: utils.EventDiagnosisOK, Version: "1.0.1"})

	o.AnnounceWhenLive(context.Background())

	// 后台轮询成功 → 应播报一次（缺失态先不播报，等轮询确认）。
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("轮询确认后未收到事件生效播报")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 3 {
		t.Fatalf("轮询确认后期望 3 张卡，实际 %d: %v", len(sent), sent)
	}
}

func TestAnnounceWhenLive_Unchecked_NoDM(t *testing.T) {
	setDiag, _ := withAnnounceStubs(t)
	o := announceOrchestrator(t)
	var called bool
	o.SendDMCardFunc = func(ctx context.Context, openID string, card []byte) (string, error) {
		called = true
		return "", nil
	}
	setDiag(utils.EventDiagnosis{State: utils.EventDiagnosisUnchecked, Reason: "网络错误"})
	o.AnnounceWhenLive(context.Background())
	if called {
		t.Fatal("unchecked 状态下不应发播报卡（仅记日志）")
	}
}

func TestAnnounceEventLiveCard_GuidanceOnlyWhenNotOK(t *testing.T) {
	o := announceOrchestrator(t)
	var gotOK, gotMissing string
	o.SendDMCardFunc = func(ctx context.Context, openID string, card []byte) (string, error) {
		// 按 header template 区分 ok(green) / 非 ok(orange)，避免 body 文案干扰。
		s := string(card)
		if strings.Contains(s, "\"template\":\"green\"") {
			gotOK = s
		} else {
			gotMissing = s
		}
		return "", nil
	}
	o.announceEventLiveCard(context.Background(), utils.EventDiagnosis{State: utils.EventDiagnosisOK, Version: "v1"}, false)
	o.announceEventLiveCard(context.Background(), utils.EventDiagnosis{State: utils.EventDiagnosisMissing, Version: "v1", MissingRequired: []string{"im.message.receive_v1"}}, false)
	if strings.Contains(gotOK, "打开事件配置页") {
		t.Fatal("ok 卡不应带事件配置页按钮")
	}
	if !strings.Contains(gotMissing, "打开事件配置页") {
		t.Fatal("missing 卡应带事件配置页按钮")
	}
}
