package card

import (
	"context"
	"testing"
)

func TestManagedRegistry_SendManagedCard(t *testing.T) {
	mc := &mockCardKitClient{}
	r := NewManagedRegistry(mc, nil)
	card := cardOne(Button("OK", ActionValue{"a": "x"}, ButtonDefault))
	res, err := r.SendManagedCard(context.Background(), "chat_1", card, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.MessageID == "" || res.CardID == "" {
		t.Fatal("send should return messageID + cardID")
	}
	if !r.IsManaged(res.MessageID) {
		t.Fatal("should register messageId→cardId mapping")
	}
}

func TestManagedRegistry_SendRetriesCardIdNotReady(t *testing.T) {
	mc := &mockCardKitClient{failCreate: &CardkitError{Code: 230099}}
	r := NewManagedRegistry(mc, nil)
	_, err := r.SendManagedCard(context.Background(), "chat_1", cardOne(Md("hi")), "", false)
	if err != nil {
		t.Fatal("230099 should retry + succeed")
	}
}

func TestManagedRegistry_UpdateManagedCard(t *testing.T) {
	mc := &mockCardKitClient{}
	r := NewManagedRegistry(mc, nil)
	res, _ := r.SendManagedCard(context.Background(), "chat_1", cardOne(Md("v1")), "", false)
	mc.updates = nil
	ok := r.UpdateManagedCard(context.Background(), res.MessageID, cardOne(Md("v2")))
	if !ok {
		t.Fatal("update should succeed for known messageId")
	}
	if len(mc.updates) != 1 {
		t.Fatalf("should push 1 update: %d", len(mc.updates))
	}
}

func TestManagedRegistry_UpdateUnknownReturnsFalse(t *testing.T) {
	mc := &mockCardKitClient{}
	r := NewManagedRegistry(mc, nil)
	if r.UpdateManagedCard(context.Background(), "unknown", cardOne(Md("x"))) {
		t.Fatal("unknown messageId should return false")
	}
}

func TestManagedRegistry_Forget(t *testing.T) {
	mc := &mockCardKitClient{}
	r := NewManagedRegistry(mc, nil)
	res, _ := r.SendManagedCard(context.Background(), "chat_1", cardOne(Md("x")), "", false)
	r.Forget(res.MessageID)
	if r.IsManaged(res.MessageID) {
		t.Fatal("forget should drop mapping")
	}
}

func TestStampRenderToken(t *testing.T) {
	mc := &mockCardKitClient{}
	r := NewManagedRegistry(mc, nil)
	// 卡片含一个 callback button。
	card := CardObject{
		"body": CardObject{"elements": []CardElement{
			Button("OK", ActionValue{"a": "test"}, ButtonDefault),
		}},
	}
	r.stampRenderToken(card)
	// button 的 behaviors[0].value 应含 __r。
	body := card["body"].(CardObject)
	els := body["elements"].([]CardElement)
	btn := els[0]
	behaviors := btn["behaviors"].([]CardElement)
	val := behaviors[0]["value"].(ActionValue)
	if val["__r"] == nil || val["__r"] == "" {
		t.Fatal("stampRenderToken should set __r on callback value")
	}
	// 原始 payload 字段保留。
	if val["a"] != "test" {
		t.Fatal("original payload should be preserved")
	}
}

func TestStampRenderToken_IncrementsPerCall(t *testing.T) {
	r := NewManagedRegistry(&mockCardKitClient{}, nil)
	card1 := CardObject{"body": CardObject{"elements": []CardElement{Button("x", ActionValue{"a": "1"}, ButtonDefault)}}}
	card2 := CardObject{"body": CardObject{"elements": []CardElement{Button("y", ActionValue{"a": "2"}, ButtonDefault)}}}
	r.stampRenderToken(card1)
	r.stampRenderToken(card2)
	v1 := card1["body"].(CardObject)["elements"].([]CardElement)[0]["behaviors"].([]CardElement)[0]["value"].(ActionValue)["__r"]
	v2 := card2["body"].(CardObject)["elements"].([]CardElement)[0]["behaviors"].([]CardElement)[0]["value"].(ActionValue)["__r"]
	if v1 == v2 {
		t.Fatal("render token should increment per call")
	}
}

func TestEncodeBase36(t *testing.T) {
	if encodeBase36(1) != "1" {
		t.Fatal("1 → 1")
	}
	if encodeBase36(36) != "10" {
		t.Fatal("36 → 10")
	}
	if encodeBase36(0) != "0" {
		t.Fatal("0 → 0")
	}
}
