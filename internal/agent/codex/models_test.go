package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMapModel_Fallbacks(t *testing.T) {
	m := rawModel{ID: "gpt-x"}
	out := MapModel(m)
	if out.DisplayName != "gpt-x" {
		t.Fatalf("displayName should fall back to id: %q", out.DisplayName)
	}
	if out.DefaultEffort != "medium" {
		t.Fatalf("defaultEffort should fall back to medium: %q", out.DefaultEffort)
	}
	if len(out.SupportedEfforts) != 0 {
		t.Fatalf("empty efforts: %v", out.SupportedEfforts)
	}
}

func TestMapModel_PassesThrough(t *testing.T) {
	raw := `{"id":"gpt-5","displayName":"GPT-5","description":"flagship","hidden":true,"isDefault":true,"supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}],"defaultReasoningEffort":"high"}`
	var m rawModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	out := MapModel(m)
	if out.DisplayName != "GPT-5" || out.Description != "flagship" || !out.Hidden || !out.IsDefault {
		t.Fatalf("passthrough wrong: %+v", out)
	}
	if out.DefaultEffort != "high" || len(out.SupportedEfforts) != 2 || out.SupportedEfforts[1] != "high" {
		t.Fatalf("efforts wrong: %+v", out.SupportedEfforts)
	}
}

func TestStaticModels_HasDefault(t *testing.T) {
	if len(StaticModels) == 0 {
		t.Fatal("static models should not be empty")
	}
	if !StaticModels[0].IsDefault {
		t.Fatal("first static model should be default")
	}
}

func TestBridgeDeveloperInstructions_ContainsKeyPhrases(t *testing.T) {
	s := BridgeDeveloperInstructions
	for _, want := range []string{"飞书桥", "feishu-card", "真实存在", "Markdown"} {
		if !strings.Contains(s, want) {
			t.Errorf("instructions missing key phrase %q", want)
		}
	}
}
