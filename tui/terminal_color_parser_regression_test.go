package tui

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestColorFgBgUsesJavaScriptWhitespace(t *testing.T) {
	// theme.ts detectColorFgBgTheme trims with JavaScript String.trim before matching digits; BOM is whitespace, NEXT LINE is not.
	for _, tc := range []struct {
		value string
		theme TerminalTheme
	}{
		{"15", "light"},
		{"\ufeff15\ufeff", "light"},
		{"\u008515\u0085", ""},
		{"", ""},
	} {
		got := DetectColorFgBgTheme(map[string]string{"COLORFGBG": tc.value})
		if got != tc.theme {
			t.Errorf("COLORFGBG=%q: %q, want %q", tc.value, got, tc.theme)
		}
	}
}

func TestOsc11ChannelNumberAndWhitespaceSemantics(t *testing.T) {
	// terminal-colors.ts uses JS Number arithmetic and String.trim, not a signed 64-bit integer or Go's Unicode space class.
	for _, n := range []int{16, 17, 64, 255, 256, 257} {
		got := parseOsc11BackgroundColor("\x1b]11;rgb:" + strings.Repeat("f", n) + "/0/0\x07")
		if got == nil {
			t.Errorf("%d-digit channel rejected", n)
			continue
		}
		if n < 256 && float64(got.R) != 255 {
			t.Errorf("%d-digit red=%v", n, got.R)
		}
		if n >= 256 && !math.IsNaN(float64(got.R)) {
			t.Errorf("%d-digit red=%v, want NaN", n, got.R)
		}
		if got.G != 0 || got.B != 0 {
			t.Errorf("other channels=%v", got)
		}
	}
	for _, tc := range []struct {
		space string
		valid bool
	}{{"\ufeff", true}, {"\u0085", false}} {
		got := parseOsc11BackgroundColor("\x1b]11;" + tc.space + "#ffffff" + tc.space + "\x07")
		if (got != nil) != tc.valid {
			t.Errorf("space=%q color=%v, valid=%v", tc.space, got, tc.valid)
		}
	}
}

func TestOsc11RejectsSignedHexPairs(t *testing.T) {
	for _, input := range []string{"\x1b]11;#+1+2+3\x07", "\x1b]11;#-1-2-3\x07"} {
		if got := parseOsc11BackgroundColor(input); got != nil {
			t.Errorf("invalid hex %q parsed as %v", input, got)
		}
	}
}

func TestOsc11BackgroundIgnoresAdditionalChannels(t *testing.T) {
	// Pi terminal-colors.ts destructures only the first three split fields, including rgba responses; it does not require exactly three channels.
	for _, input := range []string{"\x1b]11;rgba:0000/8000/ffff/0000\x07", "\x1b]11;rgb:0000/8000/ffff/ignored\x07"} {
		if got := parseOsc11BackgroundColor(input); !reflect.DeepEqual(got, &RgbColor{0, 128, 255}) {
			t.Errorf("input=%q color=%v, want {0 128 255}", input, got)
		}
	}
}

// parseOsc11BackgroundColor reads the RGB of an OSC 11 reply through the OSC color parser upstream 0.99.1 uses for every color query reply.
func parseOsc11BackgroundColor(data string) *RgbColor {
	reply, ok := ParseOscColorResponse(data)
	if !ok || reply.Target != OscColorTargetBackground {
		return nil
	}
	return reply.RGB
}
