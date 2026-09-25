package tui

import "testing"

func TestParseOsc11BackgroundColor(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *RgbColor
	}{
		{"rgb 16-bit white ST-terminated", "\x1b]11;rgb:ffff/ffff/ffff\x1b\\", &RgbColor{255, 255, 255}},
		{"hex6 black BEL-terminated", "\x1b]11;#000000\x07", &RgbColor{0, 0, 0}},
		{"hex12 white", "\x1b]11;#ffffffffffff\x07", &RgbColor{255, 255, 255}},
		{"rgb 16-bit mid green", "\x1b]11;rgb:0000/8080/ffff\x07", &RgbColor{0, 128, 255}},
		{"not an osc11 response", "hello\x07", nil},
		{"garbage payload", "\x1b]11;not-a-color\x07", nil},
		{"missing terminator", "\x1b]11;#000000", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseOsc11BackgroundColor(c.in)
			switch {
			case c.want == nil && got != nil:
				t.Fatalf("expected nil, got %+v", *got)
			case c.want != nil && got == nil:
				t.Fatalf("expected %+v, got nil", *c.want)
			case c.want != nil && *got != *c.want:
				t.Fatalf("got %+v, want %+v", *got, *c.want)
			}
		})
	}
}

func TestDetectTerminalBackgroundFromColorFgBg(t *testing.T) {
	dark := DetectTerminalBackground(TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "15;0"}})
	if dark.Theme != "dark" || dark.Source != "COLORFGBG" {
		t.Fatalf("bg index 0 (black) should be dark/COLORFGBG, got %+v", dark)
	}
	light := DetectTerminalBackground(TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "0;15"}})
	if light.Theme != "light" {
		t.Fatalf("bg index 15 (white) should be light, got %+v", light)
	}
	fallback := DetectTerminalBackground(TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": ""}})
	if fallback.Theme != "dark" || fallback.Source != "fallback" {
		t.Fatalf("no COLORFGBG hint should fall back to dark, got %+v", fallback)
	}
}

func TestAnsi256ToHex(t *testing.T) {
	cases := map[int]string{
		0:   "#000000",
		15:  "#ffffff",
		16:  "#000000",
		21:  "#0000ff",
		231: "#ffffff",
		232: "#080808",
	}
	for idx, want := range cases {
		if got := ansi256ToHex(idx); got != want {
			t.Errorf("ansi256ToHex(%d) = %s, want %s", idx, got, want)
		}
	}
}

func TestGetThemeForRgbColor(t *testing.T) {
	if GetThemeForRgbColor(RgbColor{255, 255, 255}) != "light" {
		t.Error("white background should be light")
	}
	if GetThemeForRgbColor(RgbColor{0, 0, 0}) != "dark" {
		t.Error("black background should be dark")
	}
}
