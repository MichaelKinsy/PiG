package tui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Port of oauth-selector.test.ts "shows the brand style of a preferred partner only on its selected row".
func TestOAuthSelectorShowsTheBrandStyleOnlyOnTheSelectedPartnerRow(t *testing.T) {
	th := ActiveTheme()
	providers := []OAuthProvider{
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
	}
	selector := NewOAuthSelectorComponentAnimated(func() {}, "login", providers, func(string, string) {}, func() {}, "")
	defer selector.Dispose()

	output := strings.Join(selector.Render(120), "\n")
	if strings.Contains(output, th.Fg("accent", "Radius")) {
		t.Errorf("selected Radius row kept the accent style: %q", output)
	}
	if !strings.Contains(output, th.Fg("text", "OpenRouter")) {
		t.Errorf("unselected OpenRouter row lost the text style: %q", output)
	}
	if !strings.Contains(stripANSI(output), "→ Radius") {
		t.Errorf("selected row missing: %q", stripANSI(output))
	}

	selector.HandleInput("\x1b[B")
	moved := strings.Join(selector.Render(120), "\n")
	if !strings.Contains(moved, th.Fg("text", "Radius")) {
		t.Errorf("unselected Radius row lost the text style: %q", moved)
	}
	if strings.Contains(moved, th.Fg("accent", "OpenRouter")) {
		t.Errorf("selected OpenRouter row kept the accent style: %q", moved)
	}
	if !strings.Contains(stripANSI(moved), "→ OpenRouter") {
		t.Errorf("selected row missing: %q", stripANSI(moved))
	}
}

// Port of oauth-selector.test.ts "keeps the accent style without a TUI to animate in".
func TestOAuthSelectorKeepsTheAccentStyleWithoutATUI(t *testing.T) {
	selector := NewOAuthSelectorComponent("login", []OAuthProvider{{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"}}, func(string, string) {}, func() {})
	if output := strings.Join(selector.Render(120), "\n"); !strings.Contains(output, ActiveTheme().Fg("accent", "OpenRouter")) {
		t.Fatalf("render = %q", output)
	}
}

// Port of oauth-selector.test.ts "marks preferred partners with a star".
func TestOAuthSelectorMarksPreferredPartnersWithAStar(t *testing.T) {
	selector := NewOAuthSelectorComponent("login", []OAuthProvider{
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
		{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
	}, func(string, string) {}, func() {})
	output := stripANSI(strings.Join(selector.Render(120), "\n"))
	for _, want := range []string{"→ Radius ✦ [subscription]", "  OpenRouter ✦ [API key]", "  Anthropic [API key]"} {
		if !strings.Contains(output, want) {
			t.Errorf("render missing %q:\n%s", want, output)
		}
	}
}

// oauth-selector.ts constructor: with a tui it starts the brand animation, which requests a render each frame while a
// partner row is selected, and dispose stops it (startBrandAnimation): no frame is requested once dispose has returned.
func TestOAuthSelectorAnimatesAPartnerRowUntilDisposed(t *testing.T) {
	var disposed atomic.Bool
	var requests atomic.Int64
	selector := NewOAuthSelectorComponentAnimated(func() {
		requests.Add(1)
		if disposed.Load() {
			t.Error("a render was requested after Dispose returned")
		}
	}, "login", []OAuthProvider{
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
	}, func(string, string) {}, func() {}, "")
	selector.Render(120)
	deadline := time.Now().Add(5 * time.Second)
	for requests.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no render was requested while the Radius row was selected")
		}
		time.Sleep(time.Millisecond)
	}
	selector.Dispose()
	disposed.Store(true)
	selector.Dispose()
}
