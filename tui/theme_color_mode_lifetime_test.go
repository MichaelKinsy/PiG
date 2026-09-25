package tui

import (
	"fmt"
	"runtime"
	"testing"
	"weak"
)

// Pi's theme.ts createTheme creates a new theme for the requested mode without retaining the previous instance.
func TestThemeWithColorModeReleasesSourceTheme(t *testing.T) {
	source, converted := func() (weak.Pointer[Theme], *Theme) {
		theme, err := LoadBuiltinTheme("dark")
		if err != nil {
			t.Fatal(err)
		}
		return weak.Make(theme), theme.WithColorMode(ColorMode256)
	}()
	if converted.ColorMode() != ColorMode256 || converted.Accent != "\x1b[38;5;109m" {
		t.Fatalf("converted theme = %q (%s)", converted.Accent, converted.ColorMode())
	}
	runtime.GC()
	if source.Value() != nil {
		t.Error("converted theme retains the obsolete source theme")
	}
	runtime.KeepAlive(converted)
}

// /reload applies capabilities and refreshes the active theme. Like Pi's createTheme/setGlobalTheme path, a refresh must not retain obsolete themes.
func TestRefreshActiveThemeColorModeReleasesObsoleteThemes(t *testing.T) {
	preserveCapabilityState(t)
	previous := ActiveTheme()
	t.Cleanup(func() { activeTheme.Store(previous) })
	for _, rounds := range []int{1, 100} {
		t.Run(fmt.Sprintf("round_trips_%d", rounds), func(t *testing.T) {
			obsolete := refreshThemeRoundTrips(t, rounds)
			runtime.GC()
			retained := 0
			for _, theme := range obsolete {
				if theme.Value() != nil {
					retained++
				}
			}
			if retained != 0 {
				t.Errorf("refresh retained %d of %d obsolete themes", retained, len(obsolete))
			}
		})
	}
}

func refreshThemeRoundTrips(t *testing.T, rounds int) []weak.Pointer[Theme] {
	t.Helper()
	theme, err := LoadBuiltinTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	activeTheme.Store(theme)
	var obsolete []weak.Pointer[Theme]
	for range rounds {
		for _, trueColor := range []bool{false, true} {
			obsolete = append(obsolete, weak.Make(ActiveTheme()))
			SetCapabilities(TerminalCapabilities{TrueColor: trueColor})
			RefreshActiveThemeColorMode()
			wantMode, wantAccent := ColorMode256, "\x1b[38;5;109m"
			if trueColor {
				wantMode, wantAccent = ColorModeTrueColor, "\x1b[38;2;138;190;183m"
			}
			if got := ActiveTheme(); got.ColorMode() != wantMode || got.Accent != wantAccent || got.Fg("accent") != wantAccent {
				t.Fatalf("refreshed theme = %q (%s), want %q (%s)", got.Accent, got.ColorMode(), wantAccent, wantMode)
			}
		}
	}
	return obsolete
}

func BenchmarkRefreshActiveThemeColorModeRoundTrip(b *testing.B) {
	preserveCapabilityState(b)
	previous := ActiveTheme()
	b.Cleanup(func() { activeTheme.Store(previous) })
	theme, err := LoadBuiltinTheme("dark")
	if err != nil {
		b.Fatal(err)
	}
	activeTheme.Store(theme)
	b.ReportAllocs()
	for b.Loop() {
		SetCapabilities(TerminalCapabilities{TrueColor: false})
		RefreshActiveThemeColorMode()
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		RefreshActiveThemeColorMode()
	}
}
