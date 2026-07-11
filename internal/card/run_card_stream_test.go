package card

import (
	"testing"
	"time"
)

func TestChatPacer_WaitSpaces(t *testing.T) {
	p := &ChatPacer{nextAt: 1000}
	var slept int64
	p.Wait(1000, func(ms int64) { slept = ms })
	if slept != 0 {
		t.Fatalf("first wait should not sleep: slept=%d", slept)
	}
	// 第二次：nextAt=1250，now=1100 → sleep 150。
	p.Wait(1100, func(ms int64) { slept = ms })
	if slept != 150 {
		t.Fatalf("second wait should sleep 150ms: slept=%d", slept)
	}
}

func TestChatPacer_Penalize(t *testing.T) {
	p := &ChatPacer{nextAt: 1000}
	p.Penalize(1200) // penalty=2200 > 1000 → nextAt=2200
	if p.nextAt != 2200 {
		t.Fatalf("penalize should push nextAt to 2200: got %d", p.nextAt)
	}
	// penalty 更小（2100 < 2200）→ 不缩。
	p.Penalize(1100)
	if p.nextAt != 2200 {
		t.Fatalf("penalize should not shrink nextAt: got %d", p.nextAt)
	}
}

func TestChatPacer_Idle(t *testing.T) {
	p := &ChatPacer{nextAt: 1000}
	if !p.Idle(70000) { // 70s > 1000+60s
		t.Fatal("should be idle after 60s")
	}
	if p.Idle(50000) {
		t.Fatal("should not be idle within 60s")
	}
}

func TestPacerMap_DedupPerChat(t *testing.T) {
	m := newPacerMap(func() int64 { return 1000 })
	p1 := m.pacerFor("chat_1")
	p2 := m.pacerFor("chat_1")
	if p1 != p2 {
		t.Fatal("same chat should share pacer")
	}
	p3 := m.pacerFor("chat_2")
	if p1 == p3 {
		t.Fatal("different chat should have different pacer")
	}
}

func TestIsRateLimited(t *testing.T) {
	if IsRateLimited(nil) {
		t.Fatal("nil err")
	}
	if !IsRateLimited(&CardkitError{Code: 99991400}) {
		t.Fatal("99991400 → rate limited")
	}
	if IsRateLimited(&CardkitError{Code: 230099}) {
		t.Fatal("230099 not rate limited")
	}
	if IsRateLimited(&CardkitError{Code: 0, Message: "other"}) {
		t.Fatal("other err not rate limited")
	}
}

func TestCardkitErrCode(t *testing.T) {
	if CardkitErrCode(&CardkitError{Code: 300309}) != 300309 {
		t.Fatal("should extract code")
	}
	if CardkitErrCode(nil) != 0 {
		t.Fatal("nil → 0")
	}
}

func TestAnswerContent(t *testing.T) {
	card := CardObject{
		"body": CardObject{"elements": []CardElement{
			{"tag": "markdown", "element_id": "answer", "content": "hello"},
			{"tag": "hr"},
		}},
	}
	got := AnswerContent(card, "answer")
	if got == nil || *got != "hello" {
		t.Fatalf("answerContent wrong: %v", got)
	}
	if AnswerContent(card, "nope") != nil {
		t.Fatal("missing eid → nil")
	}
}

func TestStructureSig_StableOnAnswerGrowth(t *testing.T) {
	mkCard := func(answer string) CardObject {
		return CardObject{
			"body": CardObject{"elements": []CardElement{
				{"tag": "markdown", "element_id": "answer", "content": answer},
			}},
		}
	}
	sig1 := StructureSig(mkCard("hel"), "answer")
	sig2 := StructureSig(mkCard("hello"), "answer")
	if sig1 != sig2 {
		t.Fatal("structureSig should be stable when only answer grows")
	}
}

func TestStructureSig_ChangesOnStructureChange(t *testing.T) {
	mkCard := func(extra CardElement) CardObject {
		els := []CardElement{{"tag": "markdown", "element_id": "answer", "content": "x"}}
		els = append(els, extra)
		return CardObject{"body": CardObject{"elements": els}}
	}
	sig1 := StructureSig(mkCard(CardElement{"tag": "hr"}), "answer")
	sig2 := StructureSig(mkCard(CardElement{"tag": "markdown", "content": "tool"}), "answer")
	if sig1 == sig2 {
		t.Fatal("structureSig should differ when structure changes")
	}
}

// 保证 time 包被引用（未来 ChatPacer 集成用）。
var _ = time.Second
