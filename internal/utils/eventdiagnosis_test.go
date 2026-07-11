package utils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagnose_OK(t *testing.T) {
	srv := diagnoseServer(true, 0, []map[string]any{
		{"version": "1.0.0", "status": 1, "events": []string{"im.message.receive_v1", "application.bot.menu_v6"}},
	})
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisOK {
		t.Fatalf("want ok got %s %+v", d.State, d)
	}
	if d.Version != "1.0.0" {
		t.Fatalf("version=%s", d.Version)
	}
	if len(d.MissingRequired) != 0 {
		t.Fatalf("should have no missing required: %v", d.MissingRequired)
	}
}

func TestDiagnose_Unpublished(t *testing.T) {
	srv := diagnoseServer(true, 0, []map[string]any{
		{"version": "0.9", "status": 0, "events": []string{}},
	})
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisUnpublished {
		t.Fatalf("want unpublished got %s", d.State)
	}
}

func TestDiagnose_Missing(t *testing.T) {
	srv := diagnoseServer(true, 0, []map[string]any{
		{"version": "1.0", "status": 1, "events": []string{"application.bot.menu_v6"}}, // 缺 required
	})
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisMissing {
		t.Fatalf("want missing got %s", d.State)
	}
	if !containsStr(d.MissingRequired, "im.message.receive_v1") {
		t.Fatalf("missing required wrong: %v", d.MissingRequired)
	}
}

func TestDiagnose_UncheckedTokenFail(t *testing.T) {
	srv := diagnoseServer(false, 0, nil)
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisUnchecked {
		t.Fatalf("want unchecked got %s", d.State)
	}
}

func TestDiagnose_UncheckedNonZeroCode(t *testing.T) {
	srv := diagnoseServer(true, 99991672, nil) // 版本 API code!=0（缺 scope）
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisUnchecked {
		t.Fatalf("want unchecked got %s", d.State)
	}
	if !strings.Contains(d.Reason, "99991672") {
		t.Fatalf("reason should mention code: %q", d.Reason)
	}
}

// TestDiagnose_PicksNewestStatus1 回归：飞书可能同时返回多个 status=1 版本，
// 且旧版本排在前面（order=0 升序）。旧版本缺事件、新版本已含事件时，
// 必须选「最新」已上架版本，否则会误报「缺事件」（用户已配置却报缺的真实场景）。
func TestDiagnose_PicksNewestStatus1(t *testing.T) {
	srv := diagnoseServer(true, 0, []map[string]any{
		// 旧版本（缺 required）+ 一个草稿，混在前面
		{"version": "1.0.3", "status": 1, "events": []string{"application.bot.menu_v6"}},
		{"version": "1.1.0", "status": 0, "events": []string{}},
		// 最新已上架版本，已含必需事件
		{"version": "1.0.5", "status": 1, "events": []string{"im.message.receive_v1", "application.bot.menu_v6"}},
	})
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisOK {
		t.Fatalf("want ok got %s %+v", d.State, d)
	}
	if d.Version != "1.0.5" {
		t.Fatalf("should pick newest status=1 (1.0.5), got %s", d.Version)
	}
}

// TestDiagnose_NewestStatus1Missing 最新已上架版本确实缺事件时仍应报 missing（且报的是最新版本号）。
func TestDiagnose_NewestStatus1Missing(t *testing.T) {
	srv := diagnoseServer(true, 0, []map[string]any{
		{"version": "1.0.3", "status": 1, "events": []string{"im.message.receive_v1"}},
		{"version": "2.0.0", "status": 1, "events": []string{"application.bot.menu_v6"}}, // 最新，缺 required
	})
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisMissing {
		t.Fatalf("want missing got %s", d.State)
	}
	if d.Version != "2.0.0" {
		t.Fatalf("should report newest version 2.0.0, got %s", d.Version)
	}
}

// TestDiagnose_EventsAreChineseNames 回归：飞书 app_versions 的 events 字段实为
// 中文展示名（"接收消息"），真实事件码在 event_infos[].event_type。若只比对
// events 会误判「缺 im.message.receive_v1」而事实上已订阅。本测试用真实形态校验
// 应判为 OK（对齐线上 2026-07-11 实测）。
func TestDiagnose_EventsAreChineseNames(t *testing.T) {
	srv := diagnoseServer(true, 0, []map[string]any{
		{
			"version": "1.0.5", "status": 1,
			"events": []string{"接收消息", "机器人进群", "机器人被移出群", "消息被reaction", "有新文档评论或回复通知"},
			"event_infos": []map[string]any{
				{"event_name": "接收消息", "event_type": "im.message.receive_v1"},
				{"event_name": "机器人进群", "event_type": "im.chat.member.bot.added_v1"},
				{"event_name": "机器人被移出群", "event_type": "im.chat.member.bot.deleted_v1"},
				{"event_name": "消息被reaction", "event_type": "im.message.reaction.created_v1"},
				{"event_name": "有新文档评论或回复通知", "event_type": "drive.notice.comment_add_v1"},
			},
		},
	})
	defer srv.Close()
	d := diagnoseAt(context.Background(), &http.Client{}, srv.URL, "a", "s")
	if d.State != EventDiagnosisOK {
		t.Fatalf("want ok (events present as chinese names + event_infos codes) got %s %+v", d.State, d)
	}
	if len(d.MissingRequired) != 0 {
		t.Fatalf("should have no missing required: %v", d.MissingRequired)
	}
}

func TestSummarize_AllStates(t *testing.T) {
	cases := []EventDiagnosis{
		{State: EventDiagnosisOK, Version: "1.0"},
		{State: EventDiagnosisMissing, Version: "1.0", MissingRequired: []string{"im.message.receive_v1"}},
		{State: EventDiagnosisUnpublished},
		{State: EventDiagnosisUnchecked, Reason: "缺 scope"},
	}
	for _, d := range cases {
		if s := SummarizeEventDiagnosis(d); s == "" {
			t.Fatalf("summarize empty for %+v", d)
		}
	}
}

func diagnoseServer(tokenOK bool, versionCode int, items []map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			if tokenOK {
				writeJSON(w, map[string]any{"code": 0, "tenant_access_token": "tok"})
			} else {
				writeJSON(w, map[string]any{"code": 99991663, "msg": "bad"})
			}
			return
		}
		if versionCode != 0 {
			writeJSON(w, map[string]any{"code": versionCode, "msg": "permission denied"})
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"items": items}})
	}))
}
