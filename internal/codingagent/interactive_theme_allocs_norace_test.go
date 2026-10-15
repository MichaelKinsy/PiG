//go:build !race

package codingagent

import "testing"

// Every terminal read passes the theme filter first (theme-controller.ts consumes color replies and appearance reports before
// any other listener). An ordinary key is neither, and the filter reads it through the mode's one presentationTheme, so it
// allocates nothing: building the theme's bound callbacks for each read cost 7 allocations per call, about 15 per key.
func TestConsumeTerminalThemeInputDoesNotAllocateForAKey(t *testing.T) {
	m, ctx := newThemeDispatchMode(t)
	m.opts.Settings.Theme = "dark"
	m.initTheme()
	m.applyThemeFromSettings(ctx)
	if allocs := testing.AllocsPerRun(100, func() {
		if m.consumeTerminalThemeInput("a") {
			t.Fatal("a key was consumed as a terminal theme report")
		}
	}); allocs != 0 {
		t.Fatalf("an ordinary key allocates %v times in the theme filter, want 0", allocs)
	}
}
