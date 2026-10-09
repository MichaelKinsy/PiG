package tui

import (
	"regexp"
	"strings"
	"testing"
)

func parseColorOK(t *testing.T, value string) Color {
	t.Helper()
	color, err := ParseColor(value)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

func oklchOK(t *testing.T, l, c, h float64) OklchColorValue {
	t.Helper()
	color, err := NewOklchColor(l, c, h)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

func okhslOK(t *testing.T, h, s, l float64) RgbColorValue {
	t.Helper()
	color, err := NewOkhslColor(h, s, l)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

func rgbOK(t *testing.T, r, g, b float64) RgbColorValue {
	t.Helper()
	color, err := NewRgbColor(r, g, b)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

func indexedOK(t *testing.T, index int) IndexedColor {
	t.Helper()
	color, err := NewIndexedColor(index)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

func wantColorError(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error %v, want match for %s", err, pattern)
	}
}

// .upstream/v0.99.2/packages/tui/test/colors.test.ts:15.
// Pi source: packages/tui/src/colors.ts
// mutation-checked: dropping the reads and writes of TextStyle.Bg, TextStyle.Fg fails it
func TestUpstreamColors(t *testing.T) {
	// colors.test.ts:16.
	t.Run("parses hex and OKLCH colors and rejects everything else", func(t *testing.T) {
		if got := parseColorOK(t, "#abc"); got != (RgbColorValue{R: 170, G: 187, B: 204}) {
			t.Fatalf("#abc = %#v", got)
		}
		if got := parseColorOK(t, "oklch(62% 0.1 200)"); got != (OklchColorValue{L: 0.62, C: 0.1, H: 200}) {
			t.Fatalf("oklch = %#v", got)
		}
		_, err := ParseColor("")
		wantColorError(t, err, "Invalid color value")
		_, err = ParseColor("red")
		wantColorError(t, err, "Invalid color value")
	})

	// colors.test.ts:23.
	t.Run("gamut-maps OKLCH to sRGB, including the lightness limits", func(t *testing.T) {
		for _, tc := range []struct {
			l, c, h float64
			want    RgbColor
		}{
			{0.627955, 0.257683, 29.2339, RgbColor{R: 255, G: 0, B: 0}},
			{1, 0.3, 150, RgbColor{R: 255, G: 255, B: 255}},
			{0, 0.3, 150, RgbColor{R: 0, G: 0, B: 0}},
		} {
			if got := ColorToRgb(oklchOK(t, tc.l, tc.c, tc.h)); got != tc.want {
				t.Errorf("oklch(%v %v %v) = %#v, want %#v", tc.l, tc.c, tc.h, got, tc.want)
			}
		}
	})

	// colors.test.ts:29.
	t.Run("parses OKHSL colors and round-trips them", func(t *testing.T) {
		// Full saturation at the red cusp is pure sRGB red.
		if got, want := parseColorOK(t, "okhsl(29.23 100% 56.8%)"), Color(rgbOK(t, 255, 0, 0)); got != want {
			t.Fatalf("red cusp = %#v, want %#v", got, want)
		}
		if got, want := parseColorOK(t, "OKHSL(250deg 60% 55%)"), Color(okhslOK(t, 250, 0.6, 0.55)); got != want {
			t.Fatalf("OKHSL(250deg 60% 55%) = %#v, want %#v", got, want)
		}
		_, err := ParseColor("okhsl(250 160% 55%)")
		wantColorError(t, err, "s must be between 0 and 1")
		for _, hex := range []string{"#4f8eb3", "#20242a", "#f8f9fa"} {
			channels := ColorToOkhsl(parseColorOK(t, hex))
			if got := ColorToHex(okhslOK(t, channels.H, channels.S, channels.L)); got != hex {
				t.Errorf("round trip %s = %s", hex, got)
			}
		}
	})

	// colors.test.ts:40.
	t.Run("styles text and closes sequences in reverse order", func(t *testing.T) {
		got := StyleText("Ready", TextStyle{
			Fg:             rgbOK(t, 18, 52, 86),
			Bg:             indexedOK(t, 9),
			TextAttributes: TextAttributes{Bold: true, Italic: true},
		}, TerminalColorModeTrueColor)
		if want := "\x1b[38;2;18;52;86m\x1b[48;5;9m\x1b[1m\x1b[3mReady\x1b[23m\x1b[22m\x1b[49m\x1b[39m"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		got = StyleText("Ready", TextStyle{Fg: rgbOK(t, 18, 52, 86)}, TerminalColorMode256)
		if !regexp.MustCompile(`^\x1b\[38;5;\d+mReady\x1b\[39m$`).MatchString(got) || strings.Contains(got, ";2;") {
			t.Fatalf("256-color style = %q", got)
		}
	})
}

// colors.ts discriminates its Color union on `kind`: indexedColor, rgbColor and oklchColor build "indexed", "rgb" and "oklch".
func TestColorKindDiscriminator(t *testing.T) {
	for _, tc := range []struct {
		color Color
		want  string
	}{
		{IndexedColor{Index: 3}, "indexed"},
		{RgbColorValue{R: 1, G: 2, B: 3}, "rgb"},
		{OklchColorValue{L: 0.5, C: 0.1, H: 20}, "oklch"},
	} {
		if got := tc.color.Kind(); got != tc.want {
			t.Errorf("%T.Kind() = %q, want %q", tc.color, got, tc.want)
		}
	}
	// parseColor yields each variant with its own kind.
	for input, want := range map[string]string{"#ff0000": "rgb", "oklch(0.5 0.1 20)": "oklch"} {
		c, err := ParseColor(input)
		if err != nil || c.Kind() != want {
			t.Errorf("ParseColor(%q) = %#v, %v; want kind %q", input, c, err, want)
		}
	}
	if c, err := ParseColor(7); err != nil || c.Kind() != "indexed" {
		t.Errorf("ParseColor(7) = %#v, %v; want kind indexed", c, err)
	}
}
