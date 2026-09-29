package tui

import (
	"regexp"
	"testing"
)

// packages/coding-agent/test/theme-detection.test.ts:17-42 (detectTerminalBackgroundFromEnv).
func TestDetectTerminalBackgroundFromEnvUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, colorfgbg string
		theme           TerminalTheme
		source          string
		confidence      string
	}{
		{"uses the COLORFGBG background color index (light)", "0;15", "light", "COLORFGBG", "high"},
		{"uses the COLORFGBG background color index (dark)", "15;0", "dark", "COLORFGBG", "high"},
		{"uses the last COLORFGBG field as the background", "0;7;15", "light", "COLORFGBG", "high"},
		{"defaults to dark without terminal background hints", "", "dark", "fallback", "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			if tc.colorfgbg != "" {
				env["COLORFGBG"] = tc.colorfgbg
			}
			got := DetectTerminalBackground(TerminalThemeDetectionOptions{Env: env})
			if got.Theme != tc.theme || got.Source != tc.source || got.Confidence != tc.confidence {
				t.Fatalf("got %+v, want theme=%s source=%s confidence=%s", got, tc.theme, tc.source, tc.confidence)
			}
		})
	}
}

// packages/coding-agent/test/theme-detection.test.ts:143-157 "theme color mode uses terminal capabilities".
func TestThemeColorModeUsesTerminalCapabilitiesUpstream(t *testing.T) {
	previous := ActiveTheme()
	t.Cleanup(func() { ResetCapabilitiesCache(); activeTheme.Store(previous) })

	SetCapabilities(TerminalCapabilities{TrueColor: false})
	SetTheme("dark")
	ansi256 := ActiveTheme()
	if ansi256.ColorMode() != ColorMode256 || !regexp.MustCompile(`^\x1b\[38;5;\d+m$`).MatchString(ansi256.Fg("accent")) {
		t.Fatalf("256color theme: mode=%s accent=%q", ansi256.ColorMode(), ansi256.Fg("accent"))
	}
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	truecolor := ActiveTheme()
	if truecolor.ColorMode() != ColorModeTrueColor || !regexp.MustCompile(`^\x1b\[38;2;\d+;\d+;\d+m$`).MatchString(truecolor.Fg("accent")) {
		t.Fatalf("truecolor theme: mode=%s accent=%q", truecolor.ColorMode(), truecolor.Fg("accent"))
	}
}

// packages/coding-agent/test/theme-detection.test.ts:159-174 "classifies RGB colors by luminance" and "parses and resolves automatic theme settings".
func TestThemeDetectionRgbAndSettingHelpersUpstream(t *testing.T) {
	if got := GetThemeForRgbColor(RgbColor{R: 8, G: 8, B: 8}); got != "dark" {
		t.Fatalf("dark rgb=%q", got)
	}
	if got := GetThemeForRgbColor(RgbColor{R: 250, G: 250, B: 250}); got != "light" {
		t.Fatalf("light rgb=%q", got)
	}
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
