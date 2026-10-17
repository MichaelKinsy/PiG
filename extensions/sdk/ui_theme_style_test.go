package sdk

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Theme.appearance, Theme.colors and Theme.style over the host's palette (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:311-367). The vectors are the escape sequences the host's own tui.ForegroundAnsi and BackgroundAnsi produce for each color in each mode; a test in test/extension-conformance compares every SDK with the host for the same colors.

const styleTestPalette = `{"name":"dark","appearance":"light",
"foregrounds":{"success":"\u001b[38;2;1;2;3m","dimmed":"\u001b[38;2;9;9;9m\u001b[2m","accent":"\u001b[38;5;4m"},
"backgrounds":{"toolSuccessBg":"\u001b[48;2;4;5;6m","userMessageBg":"\u001b[49m"},
"colors":{"success":{"kind":"rgb","r":1,"g":2,"b":3},"accent":{"kind":"oklch","l":0.62,"c":0.1,"h":200},"toolSuccessBg":{"kind":"indexed","index":5}},
"modifiers":true,"mode":"MODE"}`

func styleTheme(t *testing.T, mode string) UITheme {
	t.Helper()
	theme, ok := decodeUITheme(json.RawMessage(strings.Replace(styleTestPalette, "MODE", mode, 1)))
	if !ok {
		t.Fatal("palette did not decode")
	}
	return theme
}

func TestUIThemeAppearanceAndColorsComeFromThePalette(t *testing.T) {
	theme := styleTheme(t, "truecolor")
	if got := theme.Appearance(); got != "light" {
		t.Fatalf("Appearance = %q, want the palette's light", got)
	}
	want := map[string]Color{
		"success":       RgbColorValue{R: 1, G: 2, B: 3},
		"accent":        OklchColorValue{L: 0.62, C: 0.1, H: 200},
		"toolSuccessBg": IndexedColor{Index: 5},
	}
	if got := theme.Colors(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Colors = %#v, want %#v", got, want)
	}
	// The map is a copy: a caller cannot change the replicated palette.
	theme.Colors()["success"] = IndexedColor{Index: 9}
	if got := theme.Colors()["success"]; got != (RgbColorValue{R: 1, G: 2, B: 3}) {
		t.Fatalf("Colors shares its map: success = %#v", got)
	}
	if got := (UITheme{}).Appearance(); got != "" {
		t.Fatalf("a theme before any palette has Appearance %q", got)
	}
}

func TestUIThemeStyle(t *testing.T) {
	theme := styleTheme(t, "truecolor")
	const esc = "\x1b["
	for _, tc := range []struct {
		name  string
		style ThemeStyle
		want  string
	}{
		{"tokens and bold", ThemeStyle{FgToken: "success", BgToken: "toolSuccessBg", TextAttributes: TextAttributes{Bold: true}}, esc + "38;2;1;2;3m" + esc + "48;2;4;5;6m" + esc + "1mx" + esc + "22m" + esc + "49m" + esc + "39m"},
		{"no style", ThemeStyle{}, "x"},
		{"every attribute", ThemeStyle{TextAttributes: TextAttributes{Bold: true, Dim: true, Italic: true, Underline: true, Inverse: true, Strikethrough: true}}, esc + "1m" + esc + "2m" + esc + "3m" + esc + "4m" + esc + "7m" + esc + "9mx" + esc + "29m" + esc + "27m" + esc + "24m" + esc + "23m" + esc + "22m"},
		{"faint token adds dim and closes with 22", ThemeStyle{FgToken: "dimmed"}, esc + "38;2;9;9;9m" + esc + "2mx" + esc + "22m" + esc + "39m"},
		{"rgb in truecolor", ThemeStyle{Fg: RgbColorValue{R: 10, G: 20, B: 30}}, esc + "38;2;10;20;30mx" + esc + "39m"},
		{"fractional rgb rounds half up", ThemeStyle{Fg: RgbColorValue{R: 10.5, G: 20.4, B: 29.6}}, esc + "38;2;11;20;30mx" + esc + "39m"},
		{"oklch is mapped into sRGB", ThemeStyle{Fg: OklchColorValue{L: 0.62, C: 0.1, H: 200}}, esc + "38;2;28;152;158mx" + esc + "39m"},
		{"an out-of-gamut oklch keeps its hue by losing chroma", ThemeStyle{Bg: OklchColorValue{L: 1, C: 0.3, H: 150}}, esc + "48;2;255;255;255mx" + esc + "49m"},
		{"indexed", ThemeStyle{Fg: IndexedColor{Index: 5}}, esc + "38;5;5mx" + esc + "39m"},
	} {
		got, err := theme.Style("x", tc.style)
		if err != nil || got != tc.want {
			t.Errorf("%s: Style = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	// A color in 256-color mode is the nearest palette entry (theme.ts:342-367 foregroundAnsi).
	palette := styleTheme(t, "256color")
	for _, tc := range []struct {
		color Color
		want  string
	}{
		{RgbColorValue{R: 10, G: 20, B: 30}, esc + "38;5;16mx" + esc + "39m"},
		{RgbColorValue{R: 128, G: 128, B: 130}, esc + "38;5;244mx" + esc + "39m"},
		{RgbColorValue{R: 250, G: 100, B: 50}, esc + "38;5;203mx" + esc + "39m"},
		{OklchColorValue{L: 0.62, C: 0.1, H: 200}, esc + "38;5;31mx" + esc + "39m"},
		{IndexedColor{Index: 5}, esc + "38;5;5mx" + esc + "39m"},
	} {
		if got, err := palette.Style("x", ThemeStyle{Fg: tc.color}); err != nil || got != tc.want {
			t.Errorf("256color %#v: Style = %q, %v; want %q", tc.color, got, err, tc.want)
		}
	}
}

func TestUIThemeStyleRejectsBadTokens(t *testing.T) {
	theme := styleTheme(t, "truecolor")
	// theme-style.test.ts:43-48: an unknown token, and a background token in the foreground slot.
	for _, tc := range []struct {
		style ThemeStyle
		want  string
	}{
		{ThemeStyle{FgToken: "notAToken"}, "Unknown theme color: notAToken"},
		{ThemeStyle{FgToken: "toolSuccessBg"}, "Unknown theme color: toolSuccessBg"},
		{ThemeStyle{BgToken: "success"}, "Unknown theme color: success"},
		{ThemeStyle{FgToken: "success", Fg: IndexedColor{Index: 1}}, "theme style sets both a token and a color for one slot"},
		{ThemeStyle{BgToken: "toolSuccessBg", Bg: IndexedColor{Index: 1}}, "theme style sets both a token and a color for one slot"},
	} {
		if got, err := theme.Style("x", tc.style); err == nil || err.Error() != tc.want || got != "" {
			t.Errorf("Style(%+v) = %q, %v; want error %q", tc.style, got, err, tc.want)
		}
	}
}

func TestUIThemeReplacesColorsWithEveryPalette(t *testing.T) {
	first, _ := decodeUITheme(json.RawMessage(`{"appearance":"dark","colors":{"a":{"kind":"indexed","index":1}}}`))
	second, _ := decodeUITheme(json.RawMessage(`{"colors":{"b":{"kind":"rgb","r":1,"g":2,"b":3},"bad":{"kind":"nope"},"worse":"x"}}`))
	if first.Appearance() != "dark" || !reflect.DeepEqual(first.Colors(), map[string]Color{"a": IndexedColor{Index: 1}}) {
		t.Fatalf("first = %q %#v", first.Appearance(), first.Colors())
	}
	// A color of an unknown kind is not a color; it is dropped, as a token without an escape sequence is.
	if second.Appearance() != "" || !reflect.DeepEqual(second.Colors(), map[string]Color{"b": RgbColorValue{R: 1, G: 2, B: 3}}) {
		t.Fatalf("second = %q %#v", second.Appearance(), second.Colors())
	}
}

// An out-of-gamut OKLCH color that is not white or black keeps its hue and loses chroma until it fits sRGB (oklab.ts oklabToLinearSrgb; colors.ts oklchToRgb). The vectors are the host's tui.ForegroundAnsi for the same colors.
func TestUIThemeStyleMapsOutOfGamutOklchByLosingChroma(t *testing.T) {
	const esc = "\x1b["
	for _, tc := range []struct {
		mode  string
		color OklchColorValue
		want  string
	}{
		{"truecolor", OklchColorValue{L: 0.7, C: 0.3, H: 150}, esc + "38;2;0;190;88mx" + esc + "39m"},
		{"truecolor", OklchColorValue{L: 0.5, C: 0.4, H: 30}, esc + "38;2;187;12;0mx" + esc + "39m"},
		{"256color", OklchColorValue{L: 0.7, C: 0.3, H: 150}, esc + "38;5;35mx" + esc + "39m"},
		{"256color", OklchColorValue{L: 0.5, C: 0.4, H: 30}, esc + "38;5;124mx" + esc + "39m"},
	} {
		if got, err := styleTheme(t, tc.mode).Style("x", ThemeStyle{Fg: tc.color}); err != nil || got != tc.want {
			t.Errorf("%s %+v: Style = %q, %v; want %q", tc.mode, tc.color, got, err, tc.want)
		}
	}
}

// theme.ts:361-376: fg and bg throw `Unknown theme color: <token>` for a token the palette lacks, a token of the other slot included. Before any palette arrives the zero value leaves text unstyled.
func TestUIThemeFgAndBgPanicForAnUnknownToken(t *testing.T) {
	theme := styleTheme(t, "truecolor")
	for name, call := range map[string]func(){
		"Fg unknown":           func() { _ = theme.Fg("notAToken", "x") },
		"Fg with a background": func() { _ = theme.Fg("toolSuccessBg", "x") },
		"Bg unknown":           func() { _ = theme.Bg("notAToken", "x") },
		"Bg with a foreground": func() { _ = theme.Bg("success", "x") },
	} {
		func() {
			defer func() {
				err, _ := recover().(error)
				if err == nil || !strings.HasPrefix(err.Error(), "Unknown theme color: ") {
					t.Errorf("%s recovered %v, want an \"Unknown theme color: <token>\" error", name, err)
				}
			}()
			call()
		}()
	}
	if got := (UITheme{}).Fg("accent", "x"); got != "x" {
		t.Errorf("zero-value Fg = %q, want unstyled text", got)
	}
	if _, err := theme.GetBgAnsi("notAToken"); err == nil || err.Error() != "Unknown theme color: notAToken" {
		t.Errorf("GetBgAnsi error = %v, want theme.ts:374's message", err)
	}
}
