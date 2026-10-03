package tui

import (
	"reflect"
	"testing"
)

func TestUpstreamOsc11BackgroundColorParser(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:94.
	t.Run("parses 16-bit OSC 11 rgb responses", func(t *testing.T) {
		if got := parseOsc11BackgroundColor("\x1b]11;rgb:0000/8000/ffff\x07"); !reflect.DeepEqual(got, &RgbColor{0, 128, 255}) {
			t.Fatalf("color=%v", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:102.
	t.Run("parses OSC 11 hex responses", func(t *testing.T) {
		for _, tc := range []struct {
			input string
			want  RgbColor
		}{{"\x1b]11;#ffffff\x1b\\", RgbColor{255, 255, 255}}, {"\x1b]11;#000000\x07", RgbColor{0, 0, 0}}} {
			if got := parseOsc11BackgroundColor(tc.input); !reflect.DeepEqual(got, &tc.want) {
				t.Fatalf("input=%q color=%v, want %v", tc.input, got, tc.want)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:107.
	t.Run("rejects non-strict OSC 11 responses", func(t *testing.T) {
		for _, input := range []string{"x\x1b]11;#ffffff\x07", "\x1b]10;#ffffff\x07", "\x1b]11;#ffffff\x07x"} {
			if got := parseOsc11BackgroundColor(input); got != nil {
				t.Fatalf("input=%q color=%v", input, got)
			}
		}
	})
}

func TestUpstreamTerminalColorSchemeParser(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal-colors.test.ts:115.
	t.Run("parses color scheme reports", func(t *testing.T) {
		for _, tc := range []struct {
			input string
			want  TerminalColorScheme
		}{
			{"\x1b[?997;1n", "dark"},
			{"\x1b[?997;2n", "light"},
			{"\x1b[?997;2n\x1b[?997;1n\x1b[?997;1n", "dark"},
			{"\x1b[?997;1n\x1b[?997;2n\x1b[?997;2n", "light"},
			{"\x1b[?997;3n", ""},
			{"\x1b[?996n", ""},
			{"x\x1b[?997;1n", ""},
		} {
			if got := ParseTerminalColorSchemeReport(tc.input); got != tc.want {
				t.Errorf("input=%q scheme=%q, want %q", tc.input, got, tc.want)
			}
		}
	})
}
