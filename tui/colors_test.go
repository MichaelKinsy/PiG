package tui

import (
	"math"
	"testing"
)

func nanValue() float64 { return math.NaN() }

// Error text and edge parses follow colors.ts; each expectation was checked against upstream 0.99.1 running under Node 24.
func TestColorErrorsAndEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  func() error
		want string
	}{
		{"indexed high", func() error { _, err := NewIndexedColor(256); return err }, "ANSI color index must be an integer from 0 to 255: 256"},
		{"indexed low", func() error { _, err := NewIndexedColor(-1); return err }, "ANSI color index must be an integer from 0 to 255: -1"},
		{"rgb range", func() error { _, err := NewRgbColor(0, 256.5, 0); return err }, "g must be between 0 and 255: 256.5"},
		{"rgb finite", func() error { _, err := NewRgbColor(0, 0, nanValue()); return err }, "b must be finite"},
		{"oklch lightness", func() error { _, err := NewOklchColor(1.5, 0, 0); return err }, "l must be between 0 and 1: 1.5"},
		{"oklch chroma", func() error { _, err := NewOklchColor(0.5, -0.1, 0); return err }, "c must not be negative: -0.1"},
		{"okhsl lightness", func() error { _, err := NewOkhslColor(0, 0.5, 2); return err }, "l must be between 0 and 1: 2"},
		{"mix amount", func() error { _, err := MixColors(IndexedColor{}, IndexedColor{}, 1.5, ColorMixSpaceOklch); return err }, "amount must be between 0 and 1: 1.5"},
		{"parse overflow", func() error { _, err := ParseColor("oklch(1e999 0 0)"); return err }, "l must be finite"},
		{"parse empty hex", func() error { _, err := ParseColor("#12"); return err }, "Invalid color value: #12"},
		{"parse trailing space", func() error { _, err := ParseColor("#abc "); return err }, "Invalid color value: #abc "},
		{"parse kelvin sign", func() error { _, err := ParseColor("o\u212Alch(50% 0.1 20)"); return err }, "Invalid color value: o\u212Alch(50% 0.1 20)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.err(); err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		input string
		want  Color
	}{
		{"#ABC", RgbColorValue{R: 170, G: 187, B: 204}},
		{"#1a2B3c", RgbColorValue{R: 26, G: 43, B: 60}},
		{"OKLCH( 50% .1 400DEG )", OklchColorValue{L: 0.5, C: 0.1, H: 40}},
		{"oklch(0.5 1e-1 -30)", OklchColorValue{L: 0.5, C: 0.1, H: 330}},
		{"oklch(50%\u00a00.1\u20030 )", OklchColorValue{L: 0.5, C: 0.1, H: 0}},
	} {
		got, err := ParseColor(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("ParseColor(%q) = %#v, %v; want %#v", tc.input, got, err, tc.want)
		}
	}
}

func TestColorMixEndpointsAndHueWrap(t *testing.T) {
	red, blue := parseColorOK(t, "#ff0000"), parseColorOK(t, "#0000ff")
	for _, tc := range []struct {
		amount float64
		want   string
	}{{0, "#ff0000"}, {1, "#0000ff"}} {
		mixed, err := MixColors(red, blue, tc.amount, ColorMixSpaceOklch)
		if err != nil || ColorToHex(mixed) != tc.want {
			t.Errorf("mix at %v = %v, %v; want %s", tc.amount, mixed, err, tc.want)
		}
	}
	// A gray has no hue, so mixing from it keeps the other color's hue.
	gray := parseColorOK(t, "#808080")
	mixed, err := MixColors(gray, red, 0.5, ColorMixSpaceOklch)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ColorToOklch(mixed).H, ColorToOklch(red).H; !near(got, want) {
		t.Errorf("hue = %v, want %v", got, want)
	}
}

func TestStyleTextAttributeOrder(t *testing.T) {
	got := StyleText("x", TextStyle{TextAttributes: TextAttributes{Bold: true, Dim: true, Underline: true, Inverse: true, Strikethrough: true}}, TerminalColorModeTrueColor)
	if want := "\x1b[1m\x1b[2m\x1b[4m\x1b[7m\x1b[9mx\x1b[29m\x1b[27m\x1b[24m\x1b[22m"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := StyleText("x", TextStyle{}, TerminalColorMode256); got != "x" {
		t.Fatalf("unstyled = %q", got)
	}
}
