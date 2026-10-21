package tui

import (
	"sync/atomic"
	"testing"
)

// Not an upstream test: theme.ts reports a theme change to the one onThemeChange callback (theme.ts:669-731, 780), and `ctx.ui.theme` is the global theme, read at every use (interactive-mode.ts:2665). An owner that hands a copy of the palette to another process observes each replacement of the active theme and each change of the terminal colors the active theme resolves its colors from, and a stopped observer hears nothing.
func TestObserveActiveThemeReportsEveryReplacementUntilStopped(t *testing.T) {
	previous := ActiveTheme()
	t.Cleanup(func() { SetThemeInstance(previous); SetTerminalColors(TerminalColors{}); SetTerminalColorScheme("") })
	var reports atomic.Int64
	stop := ObserveActiveTheme(func() { reports.Add(1) })
	expect := func(step string, want int64) {
		t.Helper()
		if got := reports.Load(); got != want {
			t.Fatalf("%s: %d reports, want %d", step, got, want)
		}
	}
	SetTheme("light")
	expect("a built-in theme selected by SetTheme", 1)
	SetThemeByName("dark")
	expect("a theme selected by name", 2)
	SetThemeInstance(builtinLightTheme())
	expect("a theme instance", 3)
	RefreshActiveThemeColorMode()
	expect("a color-mode refresh", 4)
	black, white := RgbColor{}, RgbColor{R: 255, G: 255, B: 255}
	SetTerminalColors(TerminalColors{Foreground: &black, Background: &white})
	expect("the terminal's reported colors", 5)
	SetTerminalColorScheme("dark")
	expect("the terminal's light/dark report", 6)
	stop()
	stop()
	SetTheme("dark")
	SetTerminalColors(TerminalColors{})
	expect("a stopped observer", 6)
}

// Reporting a theme replacement allocates nothing, with or without an observer, so the owner's UI loop pays an atomic load and a call.
func TestActiveThemeObserversReportWithoutAllocating(t *testing.T) {
	stop := ObserveActiveTheme(func() {})
	t.Cleanup(stop)
	if allocs := testing.AllocsPerRun(1000, activeThemeObservers.Notify); allocs != 0 {
		t.Fatalf("Notify allocated %v times", allocs)
	}
}
