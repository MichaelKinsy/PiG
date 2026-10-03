package tui

import (
	"math"
	"regexp"
	"slices"
	"testing"
)

// Ports packages/coding-agent/test/system-theme.test.ts (upstream 0.99.1).

func systemThemeRgb(t *testing.T, hex string) RgbColor {
	t.Helper()
	color, err := ParseColor(hex)
	if err != nil {
		t.Fatal(err)
	}
	return ColorToRgb(color)
}

func systemThemeLightness(t *testing.T, rgb RgbColor) float64 {
	t.Helper()
	color, err := NewRgbColor(rgb.R, rgb.G, rgb.B)
	if err != nil {
		t.Fatal(err)
	}
	return ColorToOklch(color).L
}

func systemThemeHue(t *testing.T, rgb RgbColor) float64 {
	t.Helper()
	color, err := NewRgbColor(rgb.R, rgb.G, rgb.B)
	if err != nil {
		t.Fatal(err)
	}
	return ColorToOklch(color).H
}

type systemThemeFixture struct {
	name  string
	input SystemThemeInput
}

func systemThemeTerminals(t *testing.T) (dracula SystemThemeInput, terminals []systemThemeFixture) {
	t.Helper()
	rgb := func(hex string) RgbColor { return systemThemeRgb(t, hex) }
	var palette []RgbColor
	for _, hex := range []string{"#21222c", "#ff5555", "#50fa7b", "#f1fa8c", "#bd93f9", "#ff79c6", "#8be9fd", "#f8f8f2",
		"#6272a4", "#ff6e6e", "#69ff94", "#ffffa5", "#d6acff", "#ff92df", "#a4ffff", "#ffffff"} {
		palette = append(palette, rgb(hex))
	}
	dracula = SystemThemeInput{Background: new(rgb("#282a36")), Foreground: new(rgb("#f8f8f2")), Palette: palette}
	// Dark with a palette, light with an unreadable foreground, background only, and mid-gray.
	return dracula, []systemThemeFixture{
		{"dracula", dracula},
		{"solarizedLight", SystemThemeInput{Background: new(rgb("#fdf6e3")), Foreground: new(rgb("#657b83"))}},
		{"backgroundOnly", SystemThemeInput{Background: new(rgb("#1e1e1e"))}},
		{"midGray", SystemThemeInput{Background: new(rgb("#808080")), Foreground: new(rgb("#ffffff"))}},
	}
}

var systemThemePanels = []string{"userMessageBg", "toolPendingBg", "toolSuccessBg", "toolErrorBg", "selectedBg"}

// resolved is the test's `resolved`: "" is the terminal default, which is the foreground for text and the background for panels.
func systemThemeResolved(t *testing.T, input SystemThemeInput, token string) RgbColor {
	t.Helper()
	value := GenerateSystemThemeColors(input).Colors[token]
	if value.IsIndex {
		t.Fatalf("token %s resolved to palette index %d", token, value.Index)
	}
	if value.Text == "" {
		if slices.Contains(systemThemePanels, token) && input.Background != nil {
			return *input.Background
		}
		if !slices.Contains(systemThemePanels, token) && input.Foreground != nil {
			return *input.Foreground
		}
		t.Fatalf("token %s resolved to the terminal default, which the terminal did not report", token)
	}
	return systemThemeRgb(t, value.Text)
}

func TestGenerateSystemThemeColorsUpstream(t *testing.T) {
	// system-theme.test.ts:48
	t.Run("keeps body text readable (WCAG 4.5:1) on the background and its panels", func(t *testing.T) {
		_, terminals := systemThemeTerminals(t)
		for _, terminal := range terminals {
			text := systemThemeResolved(t, terminal.input, "text")
			for _, surface := range []RgbColor{*terminal.input.Background, systemThemeResolved(t, terminal.input, "selectedBg")} {
				if got := WcagContrast(text, surface); !(got >= 4.5) {
					t.Errorf("%s: text contrast %v < 4.5", terminal.name, got)
				}
			}
			if got := WcagContrast(systemThemeResolved(t, terminal.input, "toolTitle"), systemThemeResolved(t, terminal.input, "toolErrorBg")); !(got >= 4.5) {
				t.Errorf("%s: toolTitle contrast %v < 4.5", terminal.name, got)
			}
		}
	})

	// system-theme.test.ts:61
	t.Run("orders foreground roles by contrast and keeps panels close to the background", func(t *testing.T) {
		_, terminals := systemThemeTerminals(t)
		for _, terminal := range terminals {
			background := systemThemeLightness(t, *terminal.input.Background)
			offset := func(token string) float64 {
				return systemThemeLightness(t, systemThemeResolved(t, terminal.input, token)) - background
			}
			// On mid-gray the levels collapse to the strongest reachable color.
			if terminal.name != "midGray" {
				if !(math.Abs(offset("text")) > math.Abs(offset("muted"))) {
					t.Errorf("%s: |text| <= |muted|", terminal.name)
				}
				if !(math.Abs(offset("muted")) > math.Abs(offset("dim"))) {
					t.Errorf("%s: |muted| <= |dim|", terminal.name)
				}
			}
			lighter := GenerateSystemThemeColors(terminal.input).Appearance == "dark"
			for _, panel := range systemThemePanels {
				if got := WcagContrast(systemThemeResolved(t, terminal.input, panel), *terminal.input.Background); !(got < 2) {
					t.Errorf("%s %s: contrast %v >= 2", terminal.name, panel, got)
				}
				if (offset(panel) > 0) != lighter {
					t.Errorf("%s %s: offset > 0 is %v, want %v", terminal.name, panel, offset(panel) > 0, lighter)
				}
			}
		}
	})

	// system-theme.test.ts:78
	t.Run("uses the terminal foreground and palette hues", func(t *testing.T) {
		dracula, terminals := systemThemeTerminals(t)
		if got := GenerateSystemThemeColors(dracula).Colors["text"]; got != (ThemeColorValue{Text: "", isSet: true}) {
			t.Errorf("dracula text = %+v, want the terminal default", got)
		}
		// Solarized's foreground is below 4.5:1 on its own background, so text is darkened.
		if got := GenerateSystemThemeColors(terminals[1].input).Colors["text"]; got.Text == "" {
			t.Errorf("solarized text = %+v, want a generated color", got)
		}
		if diff := math.Abs(systemThemeHue(t, systemThemeResolved(t, dracula, "error")) - systemThemeHue(t, dracula.Palette[1])); !(diff < 8) {
			t.Errorf("error hue differs from palette red by %v", diff)
		}
	})

	// .upstream/v1.0.0/packages/coding-agent/test/system-theme.test.ts:87 (#10255).
	t.Run("keeps pastel palette colors pastel at other lightnesses", func(t *testing.T) {
		rgb := func(hex string) RgbColor { return systemThemeRgb(t, hex) }
		var palette []RgbColor
		for _, hex := range []string{"#51576d", "#e78284", "#a6d189", "#e5c890", "#8caaee", "#f4b8e4", "#81c8be", "#b5bfe2",
			"#626880", "#e67172", "#8ec772", "#d9ba73", "#7b9ef0", "#f2a4db", "#5abfb5", "#a5adce"} {
			palette = append(palette, rgb(hex))
		}
		frappe := SystemThemeInput{Background: new(rgb("#303446")), Foreground: new(rgb("#c6d0f5")), Palette: palette}
		chroma := func(value RgbColor) float64 {
			color, err := NewRgbColor(value.R, value.G, value.B)
			if err != nil {
				t.Fatal(err)
			}
			return ColorToOklch(color).C
		}
		pink := frappe.Palette[5]
		accent := systemThemeResolved(t, frappe, "accent")
		// The accent is darker than the pink, but must not gain chroma (it was 2x before the cap).
		if !(systemThemeLightness(t, accent) < systemThemeLightness(t, pink)-0.05) {
			t.Errorf("accent lightness %v, want < pink lightness %v - 0.05", systemThemeLightness(t, accent), systemThemeLightness(t, pink))
		}
		if !(chroma(accent) <= chroma(pink)*1.03) {
			t.Errorf("accent chroma %v, want <= pink chroma %v * 1.03", chroma(accent), chroma(pink))
		}
		for _, panel := range []string{"userMessageBg", "customMessageBg"} {
			if got := chroma(systemThemeResolved(t, frappe, panel)); !(got <= 0.1) {
				t.Errorf("%s chroma = %v, want <= 0.1", panel, got)
			}
		}
	})

	// system-theme.test.ts:86
	t.Run("renders grayscale at zero saturation", func(t *testing.T) {
		dracula, _ := systemThemeTerminals(t)
		dracula.Saturation = new(0.0)
		colors := GenerateSystemThemeColors(dracula).Colors
		color, err := ParseColor(colors["error"].Text)
		if err != nil {
			t.Fatal(err)
		}
		if c := ColorToOklch(color).C; !(c < 0.005) {
			t.Errorf("error chroma = %v, want < 0.005", c)
		}
	})

	// system-theme.test.ts:91
	t.Run("falls back to palette indices and faint text without a background", func(t *testing.T) {
		generated := GenerateSystemThemeColors(SystemThemeInput{AppearanceHint: "light"})
		if generated.Appearance != "light" {
			t.Errorf("appearance = %q, want light", generated.Appearance)
		}
		got := []ThemeColorValue{generated.Colors["error"], generated.Colors["text"], generated.Colors["userMessageBg"]}
		want := []ThemeColorValue{{Index: 1, IsIndex: true, isSet: true}, {isSet: true}, {isSet: true}}
		if !slices.Equal(got, want) {
			t.Errorf("[error, text, userMessageBg] = %+v, want %+v", got, want)
		}
		if !slices.Contains(generated.Dim, "muted") {
			t.Errorf("dim = %v, want it to contain muted", generated.Dim)
		}
		if got := GenerateSystemThemeColors(SystemThemeInput{Saturation: new(0.0)}).Colors["error"]; got != (ThemeColorValue{isSet: true}) {
			t.Errorf("error at zero saturation without a background = %+v, want the terminal default", got)
		}
	})
}

func TestSystemThemeUpstream(t *testing.T) {
	// system-theme.test.ts:101
	t.Run("is listed first, has no export colors, and is generated from the terminal colors", func(t *testing.T) {
		t.Cleanup(func() { SetTerminalColors(TerminalColors{}) })
		dracula, _ := systemThemeTerminals(t)
		registry := NewThemeRegistry()
		if names := registry.Names(); len(names) == 0 || names[0] != SystemThemeName {
			t.Fatalf("theme names = %v, want %q first", names, SystemThemeName)
		}
		system := registry.Get(SystemThemeName)
		if system == nil || system.ExportPageBg != "" || system.ExportCardBg != "" || system.ExportInfoBg != "" {
			t.Fatalf("system theme export colors = %+v, want none", system)
		}

		SetTerminalColors(TerminalColors{Foreground: dracula.Foreground, Background: dracula.Background, Palette: dracula.Palette})
		theme := registry.Get(SystemThemeName)
		if theme == nil {
			t.Fatal("the system theme is not registered")
		}
		if theme.Appearance() != "dark" {
			t.Errorf("appearance = %q, want dark", theme.Appearance())
		}
		if got := theme.Fg("text"); got != "\x1b[39m" {
			t.Errorf("text ansi = %q", got)
		}
		if got := theme.Fg("error"); !regexp.MustCompile(`^\x1b\[38;`).MatchString(got) {
			t.Errorf("error ansi = %q", got)
		}
	})

	// system-theme.test.ts:112
	t.Run("renders faint tokens with SGR 2 and closes it", func(t *testing.T) {
		t.Cleanup(func() { SetTerminalColors(TerminalColors{}) })
		SetTerminalColors(TerminalColors{})
		theme := NewThemeRegistry().Get(SystemThemeName)
		if theme == nil {
			t.Fatal("the system theme is not registered")
		}
		if got := theme.FgText("muted", "x"); got != "\x1b[39m\x1b[2mx\x1b[22;39m" {
			t.Errorf("fg = %q", got)
		}
		styled, err := theme.Style("x", ThemeStyle{FgToken: "muted"})
		if err != nil {
			t.Fatal(err)
		}
		if styled != "\x1b[39m\x1b[2mx\x1b[22m\x1b[39m" {
			t.Errorf("style = %q", styled)
		}
	})
}
