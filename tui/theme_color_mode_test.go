package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
)

// testdata/pi_rgb_to_256.txt holds "r g b index" rows produced by running
// Pi 0.87.1's own rgbTo256 (theme.ts, hexToRgb through hexTo256, copied
// verbatim) under Node: a 16-level RGB cube sweep plus a near-gray sweep that
// exercises the spread < 10 grayscale branch and its ties.
func TestThemeRGBTo256MatchesPi(t *testing.T) {
	file, err := os.Open("testdata/pi_rgb_to_256.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	rows := 0
	for scanner.Scan() {
		var r, g, b, want int
		if _, err := fmt.Sscan(scanner.Text(), &r, &g, &b, &want); err != nil {
			t.Fatalf("parse %q: %v", scanner.Text(), err)
		}
		rows++
		if got := themeRGBTo256(r, g, b); got != want {
			t.Errorf("themeRGBTo256(%d, %d, %d) = %d, Pi = %d", r, g, b, got, want)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows < 10000 {
		t.Fatalf("golden has %d rows", rows)
	}
}

// Mirrors theme.ts fgAnsi/bgAnsi in 256color mode.
func TestThemeANSIIn256ColorMode(t *testing.T) {
	cases := []struct {
		value  ThemeColorValue
		fg, bg string
	}{
		{ThemeColorValue{Text: "#8abeb7", isSet: true}, "\x1b[38;5;109m", "\x1b[48;5;109m"},
		{ThemeColorValue{Text: "#808080", isSet: true}, "\x1b[38;5;244m", "\x1b[48;5;244m"},
		{ThemeColorValue{Index: 42, IsIndex: true, isSet: true}, "\x1b[38;5;42m", "\x1b[48;5;42m"},
		{ThemeColorValue{Text: "", isSet: true}, "\x1b[39m", "\x1b[49m"},
		{ThemeColorValue{}, "", ""},
	}
	for _, tc := range cases {
		if got := themeFgANSI(tc.value, ColorMode256); got != tc.fg {
			t.Errorf("fg %+v = %q, want %q", tc.value, got, tc.fg)
		}
		if got := themeBgANSI(tc.value, ColorMode256); got != tc.bg {
			t.Errorf("bg %+v = %q, want %q", tc.value, got, tc.bg)
		}
	}
	if got := themeFgANSI(ThemeColorValue{Text: "#8abeb7", isSet: true}, ColorModeTrueColor); got != "\x1b[38;2;138;190;183m" {
		t.Errorf("truecolor fg = %q", got)
	}
}

func withTrueColor(t *testing.T, trueColor bool) {
	t.Helper()
	previous := ActiveTheme()
	SetCapabilities(TerminalCapabilities{TrueColor: trueColor})
	t.Cleanup(func() {
		ResetCapabilitiesCache()
		activeTheme.Store(previous)
	})
}

// Mirrors theme.ts createTheme: a theme loaded while truecolor is off uses
// 256color mode for every token, including the precomputed fields and
// dynamic Fg/Bg lookups.
func TestSetThemeFollowsTrueColorCapability(t *testing.T) {
	withTrueColor(t, false)
	for _, name := range []string{"dark", "light"} {
		SetTheme(name)
		th := ActiveTheme()
		if th.ColorMode() != ColorMode256 {
			t.Fatalf("%s mode = %q, want 256color", name, th.ColorMode())
		}
		for token, got := range map[string]string{"accent": th.Accent, "userMessageBg": th.UserMessageBg, "muted": th.Fg("muted"), "selectedBg": th.Bg("selectedBg")} {
			if strings.Contains(got, "38;2;") || strings.Contains(got, "48;2;") || got == "" {
				t.Errorf("%s %s = %q, want a 256-color escape", name, token, got)
			}
		}
	}
	SetThemeByName("dark")
	if ActiveTheme().ColorMode() != ColorMode256 || ActiveTheme().Accent != "\x1b[38;5;109m" {
		t.Fatalf("SetThemeByName dark = %q (%s)", ActiveTheme().Accent, ActiveTheme().ColorMode())
	}

	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	if ActiveTheme().ColorMode() != ColorModeTrueColor || ActiveTheme().Accent != "\x1b[38;2;138;190;183m" {
		t.Fatalf("truecolor dark accent = %q (%s)", ActiveTheme().Accent, ActiveTheme().ColorMode())
	}
}

// Components that paint fixed theme colors must follow the active theme's
// color mode rather than always emitting 24-bit escapes.
func TestFixedComponentColorsFollowColorMode(t *testing.T) {
	withTrueColor(t, false)
	SetTheme("dark")
	rendered := strings.Join([]string{
		ThemeHexFg("#b5bd68"),
		ThemeHexBg("#2d2838"),
		thinkingBorderSGR("low"),
		strings.Join(NewCompactionSummaryComponent("summary", 10).Render(40), "\n"),
	}, "\n")
	if strings.Contains(rendered, "38;2;") || strings.Contains(rendered, "48;2;") {
		t.Fatalf("256-color render still contains 24-bit escapes: %q", rendered)
	}
	if got := ThemeHexFg("#b5bd68"); got != "\x1b[38;5;143m" {
		t.Fatalf("ThemeHexFg = %q", got)
	}
}
