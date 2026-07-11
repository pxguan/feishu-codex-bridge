package claude

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// 写一张仅含魔数的「图片」文件（sniffImageType 只看头）。
func writeMagic(t *testing.T, magic []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "img.bin")
	if err := os.WriteFile(p, magic, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSniffImageType(t *testing.T) {
	cases := []struct {
		magic []byte
		want   string
	}{
		{[]byte{0x89, 0x50, 0x4e, 0x47, 0, 0, 0, 0}, "image/png"},
		{[]byte{0xff, 0xd8, 0xff, 0, 0}, "image/jpeg"},
		{[]byte{0x47, 0x49, 0x46, 0x38, 0, 0}, "image/gif"},
		{[]byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp"},
		{[]byte("heicrubbish"), ""},
	}
	for _, c := range cases {
		if got := sniffImageType(c.magic); got != c.want {
			t.Fatalf("sniffImageType(%x) = %q, want %q", c.magic, got, c.want)
		}
	}
}

func TestBuildStreamUserMsg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only fixtures")
	}
	png := writeMagic(t, []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3, 4})
	bmp := writeMagic(t, []byte("BMxxxx")) // 不支持 → 跳过

	out, err := buildStreamUserMsg("看这张图", []string{png, bmp})
	if err != nil {
		t.Fatal(err)
	}
	var msg claudeUserMsg
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if msg.Type != "user" || msg.Message.Role != "user" {
		t.Fatalf("bad wrapper: %s", out)
	}
	// content = 1 文本块 + 1 图片块（bmp 被跳过）
	if len(msg.Message.Content) != 2 {
		t.Fatalf("content len = %d, want 2: %+v", len(msg.Message.Content), msg.Message.Content)
	}
	if msg.Message.Content[0].Type != "text" || msg.Message.Content[0].Text != "看这张图" {
		t.Fatalf("text block wrong: %+v", msg.Message.Content[0])
	}
	img := msg.Message.Content[1]
	if img.Type != "image" || img.Source == nil || img.Source.Type != "base64" || img.Source.MediaType != "image/png" {
		t.Fatalf("image block wrong: %+v", img)
	}
	// base64 应可解且等于原 png 字节（去掉魔数外的 4 个填充字节）
	dec, err := base64.StdEncoding.DecodeString(img.Source.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec[:8]) != string([]byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3, 4}) {
		t.Fatalf("decoded mismatch: %x", dec)
	}
}

// fakeStdinCaptureScript：把 stdin 原样落盘到 $FCB_STDIN_CAP，再吐一条成功 result。
const fakeStdinCaptureScript = `#!/bin/sh
cat > "$FCB_STDIN_CAP"
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-img","model":"claude-opus-4-8"}
{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":1,"output_tokens":1}}
EOF
`

// TestRunStreamed_ImageStdin：含图片时，prompt+image block 必须经由 stdin 的
// stream-json user message 喂给 claude（而非 argv），且文本模式不受影响。
func TestRunStreamed_ImageStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake claude is unix-only")
	}
	imgDir := t.TempDir()
	png := filepath.Join(imgDir, "a.png")
	if err := os.WriteFile(png, []byte{0x89, 0x50, 0x4e, 0x47, 9, 9, 9, 9}, 0o600); err != nil {
		t.Fatal(err)
	}
	capPath := filepath.Join(imgDir, "stdin.txt")
	script := writeFakeClaude(t, fakeStdinCaptureScript)

	th := NewClaudeThread(script, t.TempDir(), "", agent.EffortMedium,
		[]string{"--permission-mode", "bypassPermissions"}, "")
	th.env = map[string]string{"FCB_STDIN_CAP": capPath}

	run := th.RunStreamed(context.Background(), agent.AgentInput{Text: "描述这张图", Images: []string{png}}, nil)
	sawDone := false
	for ev := range run.Events {
		if ev.Type == agent.EvDone {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("missing done")
	}

	raw, err := os.ReadFile(capPath)
	if err != nil {
		t.Fatalf("stdin not captured: %v", err)
	}
	var msg claudeUserMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("captured stdin not valid json: %v\n%s", err, raw)
	}
	if len(msg.Message.Content) != 2 {
		t.Fatalf("stdin content len = %d, want 2 (text+image): %s", len(msg.Message.Content), raw)
	}
	if msg.Message.Content[1].Source == nil || msg.Message.Content[1].Source.MediaType != "image/png" {
		t.Fatalf("stdin image block missing: %s", raw)
	}
}
