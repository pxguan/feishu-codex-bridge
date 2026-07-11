package card

import (
	"strings"
	"testing"
)

func TestBuildEventLiveCard_OK(t *testing.T) {
	c := BuildEventLiveCard(EventLiveCardOpts{
		State:   "ok",
		Version: "1.2.3",
		Events:  []string{"im.message.receive_v1", "card.action.trigger"},
		MissingOptional: []string{
			"application.bot.menu_v6",
			"im.chat.member.bot.added_v1",
			"im.message.reaction.created_v1",
			"drive.notice.comment_add_v1",
		},
	})
	h := c["header"].(CardElement)
	if h["template"] != "green" {
		t.Fatalf("ok → green header, got %v", h["template"])
	}
	if !strings.Contains(h["title"].(CardElement)["content"].(string), "事件已生效") {
		t.Fatalf("ok title should mention 事件已生效, got %v", h["title"])
	}
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	for _, want := range []string{"im.message.receive_v1", "已订阅事件", "静默关闭"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ok card missing %q", want)
		}
	}
}

func TestBuildEventLiveCard_OK_Polled(t *testing.T) {
	c := BuildEventLiveCard(EventLiveCardOpts{State: "ok", Polled: true})
	if !strings.Contains(c["header"].(CardElement)["title"].(CardElement)["content"].(string), "已确认") {
		t.Fatal("polled ok card should mention 已确认")
	}
}

func TestBuildEventLiveCard_Missing(t *testing.T) {
	c := BuildEventLiveCard(EventLiveCardOpts{
		State:           "missing",
		Version:         "1.0.0",
		MissingRequired: []string{"im.message.receive_v1"},
		GuidanceURL:     "https://example.com/event-config",
	})
	h := c["header"].(CardElement)
	if h["template"] != "orange" {
		t.Fatalf("missing → orange header, got %v", h["template"])
	}
	if !strings.Contains(h["title"].(CardElement)["content"].(string), "事件未生效") {
		t.Fatalf("missing title should mention 事件未生效, got %v", h["title"])
	}
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	for _, want := range []string{"v1.0.0", "im.message.receive_v1", "打开事件配置页"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing card missing %q", want)
		}
	}
}

func TestBuildEventLiveCard_Unpublished(t *testing.T) {
	c := BuildEventLiveCard(EventLiveCardOpts{State: "unpublished"})
	body := c["body"].(CardElement)
	joined := joinMd(body["elements"].([]CardElement))
	if !strings.Contains(joined, "从未发布过版本") {
		t.Fatal("unpublished card should mention 从未发布过版本")
	}
}

func TestBuildEventLiveCard_GuidanceOnlyWhenNotOK(t *testing.T) {
	// ok 状态不应带「打开事件配置页」按钮。
	ok := BuildEventLiveCard(EventLiveCardOpts{State: "ok", GuidanceURL: "https://x"})
	okBody := ok["body"].(CardElement)
	if strings.Contains(joinMd(okBody["elements"].([]CardElement)), "打开事件配置页") {
		t.Fatal("ok card should not show guidance button")
	}
	// missing 状态应带。
	missing := BuildEventLiveCard(EventLiveCardOpts{State: "missing", GuidanceURL: "https://x"})
	missBody := missing["body"].(CardElement)
	if !strings.Contains(joinMd(missBody["elements"].([]CardElement)), "打开事件配置页") {
		t.Fatal("missing card should show guidance button")
	}
}
