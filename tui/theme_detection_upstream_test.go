package tui

import (
	"regexp"
	"testing"
)

// Ports packages/coding-agent/test/theme-detection.test.ts (upstream 0.99.1).

// theme-detection.test.ts:15
func TestDetectColorFgBgThemeUpstream(t *testing.T) {
	t.Run("classifies the last field by palette index like Vim", func(t *testing.T) {
		for _, tc := range []struct {
			name, colorfgbg string
			set             bool
			want            TerminalTheme
		}{
			{"dark background index 0", "15;0", true, "dark"},
			{"the last field is the background", "0;7;15", true, "light"},
			// Solarized Dark's background is bright black.
			{"bright black is dark", "12;8", true, "dark"},
			// rxvt writes "default" when the background is not a palette color.
			{"default is unknown", "15;default", true, ""},
			{"no variable", "", false, ""},
		} {
			env := map[string]string{}
			if tc.set {
				env["COLORFGBG"] = tc.colorfgbg
			}
			if got := DetectColorFgBgTheme(env); got != tc.want {
				t.Errorf("%s: DetectColorFgBgTheme(%q) = %q, want %q", tc.name, tc.colorfgbg, got, tc.want)
			}
		}
	})
}

// theme-detection.test.ts:27
func TestDetectTerminalThemeUpstream(t *testing.T) {
	t.Run("prefers the background, then the reported scheme, then COLORFGBG, then dark", func(t *testing.T) {
		env := map[string]string{"COLORFGBG": "0;15"}
		if got := DetectTerminalTheme(TerminalColors{Background: &RgbColor{R: 8, G: 8, B: 8}}, "light", env); got != "dark" {
			t.Errorf("background = %q, want dark", got)
		}
		if got := DetectTerminalTheme(TerminalColors{}, "dark", env); got != "dark" {
			t.Errorf("reported scheme = %q, want dark", got)
		}
		if got := DetectTerminalTheme(TerminalColors{}, "", env); got != "light" {
			t.Errorf("COLORFGBG = %q, want light", got)
		}
		if got := DetectTerminalTheme(TerminalColors{}, "", map[string]string{}); got != "dark" {
			t.Errorf("fallback = %q, want dark", got)
		}
	})

	t.Run("follows the foreground when text is readable that way", func(t *testing.T) {
		background := RgbColor{R: 118, G: 118, B: 118}
		if got := DetectTerminalTheme(TerminalColors{Background: &background}, "", nil); got != "light" {
			t.Errorf("background only = %q, want light", got)
		}
		if got := DetectTerminalTheme(TerminalColors{Background: &background, Foreground: &RgbColor{R: 255, G: 255, B: 255}}, "", nil); got != "dark" {
			t.Errorf("white foreground = %q, want dark", got)
		}
		// White text cannot reach 4.5:1 on mid-gray.
		midGray := RgbColor{R: 128, G: 128, B: 128}
		if got := DetectTerminalTheme(TerminalColors{Background: &midGray, Foreground: &RgbColor{R: 255, G: 255, B: 255}}, "", nil); got != "light" {
			t.Errorf("mid-gray = %q, want light", got)
		}
	})
}

// theme-detection.test.ts:46
func TestThemeColorModeUsesTerminalCapabilitiesUpstream(t *testing.T) {
	previous := ActiveTheme()
	t.Cleanup(func() { ResetCapabilitiesCache(); activeTheme.Store(previous) })

	SetCapabilities(TerminalCapabilities{TrueColor: false})
	SetTheme("dark")
	ansi256 := ActiveTheme()
	if ansi256.ColorMode() != TerminalColorMode256 || !regexp.MustCompile(`^\x1b\[38;5;\d+m$`).MatchString(ansi256.Fg("accent")) {
		t.Fatalf("256color theme: mode=%s accent=%q", ansi256.ColorMode(), ansi256.Fg("accent"))
	}
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	truecolor := ActiveTheme()
	if truecolor.ColorMode() != TerminalColorModeTrueColor || !regexp.MustCompile(`^\x1b\[38;2;\d+;\d+;\d+m$`).MatchString(truecolor.Fg("accent")) {
		t.Fatalf("truecolor theme: mode=%s accent=%q", truecolor.ColorMode(), truecolor.Fg("accent"))
	}
}

// theme-detection.test.ts:62 "parses and resolves automatic theme settings".
func TestThemeSettingHelpersUpstream(t *testing.T) {
	if light, dark, ok := ParseAutoThemeSetting("light/dark"); !ok || light != "light" || dark != "dark" {
		t.Fatalf("parse=%q,%q,%v", light, dark, ok)
	}
	for _, tc := range []struct {
		setting  string
		terminal TerminalTheme
		want     string
		ok       bool
	}{
		{"dark", "light", "dark", true}, {"light/dark", "light", "light", true}, {"light/dark", "dark", "dark", true}, {"light/dark/extra", "dark", "", false},
	} {
		if got, ok := ResolveThemeSetting(tc.setting, tc.terminal); got != tc.want || ok != tc.ok {
			t.Fatalf("resolve(%q,%q)=%q,%v want %q,%v", tc.setting, tc.terminal, got, ok, tc.want, tc.ok)
		}
	}
}
