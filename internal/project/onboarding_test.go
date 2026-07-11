package project

import (
	"strings"
	"testing"
)

func TestOnboardingText(t *testing.T) {
	p := Project{Name: "test", Cwd: "/proj"}
	got := OnboardingText(p, "Codex")
	if !strings.Contains(got, "Codex Bridge") || !strings.Contains(got, "/proj") {
		t.Fatalf("onboarding text: %q", got)
	}
	if OnboardingText(p, "") == "" {
		t.Fatal("empty agentName → default Codex")
	}
}

func TestSidebarPcUrl(t *testing.T) {
	u := SidebarPcUrl("https://example.com")
	if !strings.Contains(u, "applink") || !strings.Contains(u, "sidebar-semi") {
		t.Fatalf("sidebarPcUrl: %q", u)
	}
	if SidebarPcUrl("") != "" {
		t.Fatal("empty → empty")
	}
}

func TestShouldOnboard(t *testing.T) {
	if !ShouldOnboard(Project{}) {
		t.Fatal("empty origin (default created) → true")
	}
	if !ShouldOnboard(Project{Origin: "created"}) {
		t.Fatal("created → true")
	}
	if ShouldOnboard(Project{Origin: "joined"}) {
		t.Fatal("joined → false")
	}
}
