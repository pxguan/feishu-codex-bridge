package card

import (
	"strings"
	"testing"
)

func TestConnLabel(t *testing.T) {
	if ConnLabel("connected") != "✅ 已连接" {
		t.Fatal("connected")
	}
	if ConnLabel("disconnected") != "❌ 已断开" {
		t.Fatal("disconnected")
	}
	if ConnLabel("unknown") != "unknown" {
		t.Fatal("unknown → raw")
	}
}

func TestScopeStatusText(t *testing.T) {
	if ScopeStatusText(nil) != "未能自动检查（凭证失效或网络问题）" {
		t.Fatal("nil")
	}
	scopes := []string{}
	if ScopeStatusText(scopes) != "必需权限齐全" {
		t.Fatal("empty → ok")
	}
	scopes = []string{"im:resource", "im:chat:create"}
	got := ScopeStatusText(scopes)
	if !strings.Contains(got, "缺失 2 项") {
		t.Fatalf("missing: %q", got)
	}
}

func TestCodexDiagnosePrompt(t *testing.T) {
	i := DoctorInfo{
		CodexOK: true, CodexVer: "0.140.0", Conn: "✅ 已连接",
		BridgeVer: "0.6.3", Platform: "darwin-arm64",
		LogStdout: "/tmp/log.out", LogStderr: "/tmp/log.err",
		MissingScopes: []string{},
	}
	prompt := CodexDiagnosePrompt(i)
	for _, want := range []string{"feishu-codex-bridge", "0.140.0", "darwin-arm64", "/tmp/log.out", "codex 可用：是"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildDoctorCard_CodexOK(t *testing.T) {
	i := DoctorInfo{
		CodexOK: true, CodexVer: "0.140.0", Conn: "connected",
		MissingScopes: []string{}, BridgeVer: "0.6.3",
		ScopeGrantURL: "https://example.com/grant",
	}
	c := BuildDoctorCard(i)
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	joined := joinMd(els)
	// 对齐 TS dm-cards.ts: `- Codex：✅ 可用（0.140.0）`。
	if !strings.Contains(joined, "✅ 可用") {
		t.Fatalf("codex OK should show 可用: %q", joined)
	}
	if !strings.Contains(joined, "0.140.0") {
		t.Fatalf("codex OK should show version: %q", joined)
	}
	if !strings.Contains(joined, "必需权限已全部开通") {
		t.Fatalf("scopes ok: %q", joined)
	}
}

func TestBuildDoctorCard_CodexMissing(t *testing.T) {
	i := DoctorInfo{
		CodexOK: false, Conn: "disconnected",
		MissingScopes: []string{"im:resource"},
		ScopeGrantURL: "https://x",
	}
	c := BuildDoctorCard(i)
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	// 对齐 TS dm-cards.ts: `❌ 不可用（检查 CODEX_BIN / PATH）`。
	if !strings.Contains(joined, "❌ 不可用") {
		t.Fatalf("codex missing: %q", joined)
	}
	if !strings.Contains(joined, "缺 1 项") {
		t.Fatalf("missing scope: %q", joined)
	}
}

func TestBuildDoctorCard_ScopesUnknown(t *testing.T) {
	i := DoctorInfo{CodexOK: true, CodexVer: "x", Conn: "connected", MissingScopes: nil}
	c := BuildDoctorCard(i)
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !strings.Contains(joined, "无法自动检查") {
		t.Fatalf("nil scopes: %q", joined)
	}
}
