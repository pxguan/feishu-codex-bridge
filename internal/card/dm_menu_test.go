package card

import (
	"testing"
)

func TestBuildDmMenuCard(t *testing.T) {
	c := BuildDmMenuCard("", "0.6.3")
	if c["config"].(CardElement)["enable_forward"] != false {
		t.Fatal("DM menu card should disable forward")
	}
	h := c["header"].(CardElement)
	if h["title"].(CardElement)["content"] != "🤖 Codex Bridge 管理台" {
		t.Fatal("menu title wrong")
	}
	tags := h["text_tag_list"].([]CardElement)
	if len(tags) != 1 || tags[0]["text"].(CardElement)["content"] != "v0.6.3" {
		t.Fatalf("version badge wrong: %+v", tags)
	}
	// 应含两行 actionsFixed（6 按钮）。
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	buttonCount := 0
	for _, e := range els {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						buttonCount++
					}
				}
			}
		}
	}
	if buttonCount != 7 { // 3 + 4
		t.Fatalf("menu should have 7 buttons (3+4): got %d", buttonCount)
	}
}

func TestBuildDmMenuCard_WithWebConsole(t *testing.T) {
	c := BuildDmMenuCard("https://127.0.0.1:51847?token=x", "")
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	hasLink := false
	for _, e := range els {
		if e["tag"] == "column_set" {
			for _, col := range e["columns"].([]CardElement) {
				for _, sub := range col["elements"].([]CardElement) {
					if sub["tag"] == "button" {
						behaviors := sub["behaviors"].([]CardElement)
						if behaviors[0]["type"] == "open_url" {
							hasLink = true
						}
					}
				}
			}
		}
	}
	if !hasLink {
		t.Fatal("webConsoleUrl should add link button")
	}
}

func TestBuildDmMenuCard_NoVersionBadge(t *testing.T) {
	c := BuildDmMenuCard("", "")
	h := c["header"].(CardElement)
	if h["text_tag_list"] != nil {
		t.Fatal("no version → no text_tag_list")
	}
}

func TestBuildUpdateCard_Checking(t *testing.T) {
	c := BuildUpdateCard(UpdateCardState{Phase: UpdateChecking})
	if c["header"].(CardElement)["template"] != "turquoise" {
		t.Fatal("checking → turquoise")
	}
}

func TestBuildUpdateCard_CheckedHasUpdate(t *testing.T) {
	c := BuildUpdateCard(UpdateCardState{Phase: UpdateChecked, Current: "0.6.2", Latest: "0.6.3", HasUpdate: true})
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	// joinMd 不递归 Actions（按钮文案在 column_set 里）；只断言顶层 Md 的「发现新版本」。
	if !containsStr(joined, "发现新版本") {
		t.Fatalf("checked+hasUpdate should show 发现新版本: %q", joined)
	}
}

func TestBuildUpdateCard_CheckedNoUpdate(t *testing.T) {
	c := BuildUpdateCard(UpdateCardState{Phase: UpdateChecked, Current: "0.6.3", Latest: "0.6.3", HasUpdate: false})
	if c["header"].(CardElement)["template"] != "green" {
		t.Fatal("no update → green")
	}
}

func TestBuildUpdateCard_Done(t *testing.T) {
	c := BuildUpdateCard(UpdateCardState{Phase: UpdateDone, From: "0.6.2", To: "0.6.3", WillRestart: true})
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !containsStr(joined, "v0.6.2 → v0.6.3") || !containsStr(joined, "自动重启") {
		t.Fatalf("done should show version transition + restart: %q", joined)
	}
}

func TestBuildUpdateCard_Error(t *testing.T) {
	c := BuildUpdateCard(UpdateCardState{Phase: UpdateError, Message: "EACCES"})
	if c["header"].(CardElement)["template"] != "red" {
		t.Fatal("error → red")
	}
}

func TestBackToMenu(t *testing.T) {
	el := BackToMenu()
	if el["tag"] != "column_set" {
		t.Fatal("BackToMenu should be column_set")
	}
}

func TestOpenChatURL(t *testing.T) {
	u := OpenChatURL("oc_abc def")
	if u == "" {
		t.Fatal("openChatUrl should not be empty")
	}
}

func TestKindLabel(t *testing.T) {
	if KindLabel("single") != "💬 单会话群" {
		t.Fatal("single label")
	}
	if KindLabel("multi") != "👥 多话题群" {
		t.Fatal("multi label")
	}
	if KindLabel("") != "👥 多话题群" {
		t.Fatal("empty → multi default")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
