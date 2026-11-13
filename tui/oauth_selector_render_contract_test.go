package tui

import (
	"strings"
	"testing"
	"time"
)

// oauth-selector.ts render (`if (this.stopAnimation && this.getSelectedBrandStyle()) this.updateList(); return super.render(width)`):
// the list is built from styled text, so every frame rebuilds it while the selected row is a preferred partner's and the selector
// has a TUI to animate in. The selected row is then the accent arrow and the brand style at the time of that frame
// (`brandStyle(provider.name, performance.now() - this.animationStart)`), which TestRadiusShimmerMatchesPi compares with Pi.
// A selected row without a brand style, and a selector without a TUI, render the rows the last key built.
func TestOAuthSelectorRenderRebuildsTheSelectedPartnerRowForEachFrame(t *testing.T) {
	th := ActiveTheme()
	providers := []OAuthProvider{
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
	}
	elapsed := time.Duration(0)
	clock := func(selector *OAuthSelectorComponent) {
		selector.now = func() time.Time { return selector.animationStart.Add(elapsed) }
	}
	frame := func(selector *OAuthSelectorComponent, at time.Duration) string {
		elapsed = at
		return strings.Join(selector.Render(120), "\n")
	}
	animated := NewOAuthSelectorComponentAnimated("login", providers, func(string, string) {}, func() {}, "", renderFuncTUI{f: func() {}})
	defer animated.Dispose()
	clock(animated)
	steps := []time.Duration{0, 100 * time.Millisecond, 250 * time.Millisecond, 1900 * time.Millisecond}
	var previous string
	for _, at := range steps {
		got := frame(animated, at)
		want := th.Fg("accent", "→ ") + RadiusShimmer("Radius", at, th.GetColorMode())
		if !strings.Contains(got, want) {
			t.Errorf("frame at %v lacks the Radius row of that time %q:\n%q", at, want, got)
		}
		if got == previous {
			t.Errorf("frame at %v equals the frame before it: the selected partner row was not rebuilt", at)
		}
		previous = got
	}

	// Anthropic has no brand style: once it is selected a frame is the list the key built, whatever the time.
	animated.HandleInput("\x1b[B")
	moved := frame(animated, 2*time.Second)
	if later := frame(animated, 3*time.Second); later != moved {
		t.Errorf("frames with a row without a brand style selected differ:\n%q\n%q", moved, later)
	}
	if !strings.Contains(moved, th.Fg("accent", "→ ")+th.Fg("accent", "Anthropic")) || !strings.Contains(moved, "  "+th.Fg("text", "Radius")) {
		t.Errorf("after moving off the partner row the list is not the plain one: %q", moved)
	}

	// Back on the partner row the frames follow the clock again.
	animated.HandleInput("\x1b[A")
	if got, want := frame(animated, 4*time.Second), th.Fg("accent", "→ ")+RadiusShimmer("Radius", 4*time.Second, th.GetColorMode()); !strings.Contains(got, want) {
		t.Errorf("frame after returning to the partner row lacks %q:\n%q", want, got)
	}

	// Without a TUI nothing animates: the selected partner row keeps the accent style in every frame.
	still := NewOAuthSelectorComponent("login", providers, func(string, string) {}, func() {})
	clock(still)
	first := frame(still, 0)
	if later := frame(still, time.Second); later != first {
		t.Errorf("frames of a selector without a TUI differ:\n%q\n%q", first, later)
	}
	if !strings.Contains(first, th.Fg("accent", "→ ")+th.Fg("accent", "Radius")) {
		t.Errorf("a selector without a TUI does not show the selected partner row in the accent style: %q", first)
	}
}
