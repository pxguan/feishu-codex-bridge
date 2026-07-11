package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

func newTestStore(t *testing.T) *project.Store {
	t.Helper()
	dir := t.TempDir()
	st := project.NewStore(filepath.Join(dir, "projects.json"))
	if err := st.Add(project.Project{Name: "alpha", Cwd: "/tmp/alpha", ChatID: "oc_alpha"}); err != nil {
		t.Fatalf("add project: %v", err)
	}
	return st
}

func TestListProjects(t *testing.T) {
	st := newTestStore(t)
	s := &Server{Token: "tok", Deps: &Deps{Projects: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/projects?token=tok", nil)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list []project.Project
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Name != "alpha" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestProjectSettingsUpdate(t *testing.T) {
	st := newTestStore(t)
	s := &Server{Token: "tok", Deps: &Deps{Projects: st}}
	body, _ := json.Marshal(map[string]any{
		"noMention":    true,
		"autoCompact":  false,
		"defaultModel": "gpt-4o",
		"defaultEffort": "high",
		"sourceUrl":    "https://feishu.cn/docx/AbCdEf",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/alpha/settings?token=tok", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, err := st.GetByName("alpha")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.NoMention == nil || !*p.NoMention {
		t.Fatalf("NoMention 未更新: %v", p.NoMention)
	}
	if p.AutoCompact == nil || *p.AutoCompact {
		t.Fatalf("AutoCompact 未更新: %v", p.AutoCompact)
	}
	if p.DefaultModel != "gpt-4o" {
		t.Fatalf("DefaultModel 未更新: %q", p.DefaultModel)
	}
	if p.DefaultEffort != agent.ReasoningEffort("high") {
		t.Fatalf("DefaultEffort 未更新: %q", p.DefaultEffort)
	}
	if p.SourceURL != "https://feishu.cn/docx/AbCdEf" {
		t.Fatalf("SourceURL 未更新: %q", p.SourceURL)
	}
}

func TestWriteEndpoints_ReadOnlyNoDeps(t *testing.T) {
	s := &Server{Token: "tok"} // Deps=nil
	req := httptest.NewRequest(http.MethodPost, "/api/bot/register?token=tok", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("只读模式应返回 501，实际 %d", rec.Code)
	}
}

func TestLogsTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "service.log")
	content := ""
	for i := 0; i < 500; i++ {
		content += "line " + itoa(i) + "\n"
	}
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{Token: "tok", Deps: &Deps{Projects: newTestStore(t), LogFile: logPath}}
	req := httptest.NewRequest(http.MethodGet, "/api/logs?lines=3&token=tok", nil)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	out := rec.Body.String()
	// 末尾三行应是 line 497/498/499。
	if !contains(out, "line 499") || !contains(out, "line 497") {
		t.Fatalf("logs tail 错误：\n%s", out)
	}
	if contains(out, "line 0") {
		t.Fatalf("logs tail 包含了过早的行：\n%s", out)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
