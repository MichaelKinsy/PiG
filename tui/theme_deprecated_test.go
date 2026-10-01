package tui

import "testing"

// The v0.2.0 theme-detection API keeps compiling callers and answers with the current terminal classification.
func TestDeprecatedThemeDetectionAliases(t *testing.T) {
	mode := ColorMode(TerminalColorMode256)
	if mode != ColorMode256 || ColorModeTrueColor != TerminalColorModeTrueColor {
		t.Fatalf("color mode aliases = %q, %q", mode, ColorModeTrueColor)
	}
	light := DetectTerminalBackground(TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "0;15"}})
	if light != (TerminalThemeDetection{Theme: "light", Source: "COLORFGBG", Detail: "background color index 15", Confidence: "high"}) {
		t.Fatalf("COLORFGBG 0;15 = %+v", light)
	}
	// theme.ts detectColorFgBgTheme classifies bright black (8) as dark, unlike a luminance test.
	if got := DetectTerminalBackground(TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "15;default;8"}}); got.Theme != "dark" || got.Detail != "background color index 8" {
		t.Fatalf("COLORFGBG 15;default;8 = %+v", got)
	}
	fallback := DetectTerminalBackground(TerminalThemeDetectionOptions{Env: map[string]string{}})
	if fallback != (TerminalThemeDetection{Theme: "dark", Source: "fallback", Detail: "no terminal background hint found", Confidence: "low"}) {
		t.Fatalf("no COLORFGBG = %+v", fallback)
	}
	if got := GetThemeForRgbColor(RgbColor{R: 250, G: 250, B: 250}); got != "light" {
		t.Fatalf("white background = %q", got)
	}
	if got := GetThemeForRgbColor(RgbColor{R: 10, G: 10, B: 10}); got != "dark" {
		t.Fatalf("black background = %q", got)
	}
	if got := ParseOsc11BackgroundColor("\x1b]11;rgb:ffff/8080/0000\x07"); got == nil || *got != (RgbColor{R: 255, G: 128, B: 0}) {
		t.Fatalf("OSC 11 reply = %+v", got)
	}
	if got := ParseOsc11BackgroundColor("\x1b]10;rgb:ffff/ffff/ffff\x07"); got != nil {
		t.Fatalf("OSC 10 reply parsed as background: %+v", got)
	}
}
