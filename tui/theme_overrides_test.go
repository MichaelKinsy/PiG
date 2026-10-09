package tui

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func mustParseColor(t *testing.T, value string) Color {
	t.Helper()
	color, err := ParseColor(value)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

func TestWithTokenColorsEscapesInThemeColorMode(t *testing.T) {
	override := mustParseColor(t, "#d75f00")
	background := mustParseColor(t, "#102030")
	for _, mode := range []TerminalColorMode{TerminalColorModeTrueColor, TerminalColorMode256} {
		original := builtinDarkTheme().WithColorMode(mode)
		originalFg, originalBg := original.ANSIPalette()
		originalAccent, originalKeys := original.Accent, slices.Clone(original.ColorKeys())

		derived, err := original.WithTokenColors(map[string]Color{"accent": override, "selectedBg": background})
		if err != nil {
			t.Fatal(err)
		}
		if want := ForegroundAnsi(override, mode); derived.GetFgAnsi("accent") != want || derived.Accent != want {
			t.Errorf("%s: accent = %q / %q, want %q", mode, derived.GetFgAnsi("accent"), derived.Accent, want)
		}
		if got, want := derived.Fg("accent", "x"), ForegroundAnsi(override, mode)+"x"+SGRFgReset; got != want {
			t.Errorf("%s: FgText = %q, want %q", mode, got, want)
		}
		if want := BackgroundAnsi(background, mode); derived.GetBgAnsi("selectedBg") != want || derived.SelectedBg != want {
			t.Errorf("%s: selectedBg = %q / %q, want %q", mode, derived.GetBgAnsi("selectedBg"), derived.SelectedBg, want)
		}
		if derived.Name != original.Name || derived.GetColorMode() != original.GetColorMode() || derived.Muted != original.Muted {
			t.Errorf("%s: derived name %q mode %q muted %q", mode, derived.Name, derived.GetColorMode(), derived.Muted)
		}
		if derived.WithColorMode(otherColorMode(mode)) != derived {
			t.Errorf("%s: a derived theme must not rebuild from the JSON source", mode)
		}
		if got := derived.Colors()["accent"]; ColorToHex(got) != "#d75f00" {
			t.Errorf("%s: ColorValues accent = %v", mode, got)
		}
		if !slices.Equal(derived.ColorKeys(), originalKeys) {
			t.Errorf("%s: color keys changed for already concrete tokens", mode)
		}

		fg, bg := original.ANSIPalette()
		if original.Accent != originalAccent || !maps.Equal(fg, originalFg) || !maps.Equal(bg, originalBg) || ColorToHex(original.Colors()["accent"]) == "#d75f00" {
			t.Errorf("%s: original theme modified", mode)
		}
	}
}

func otherColorMode(mode TerminalColorMode) TerminalColorMode {
	if mode == TerminalColorMode256 {
		return TerminalColorModeTrueColor
	}
	return TerminalColorMode256
}

func TestWithTokenColorsKeepsFaintAndPromotesDefaultTokens(t *testing.T) {
	// The system recipe leaves text at the terminal's default color.
	black, white := RgbColor{}, RgbColor{R: 255, G: 255, B: 255}
	foregrounds, backgrounds := splitThemeTokenValues(GenerateSystemThemeColors(SystemThemeInput{Foreground: &black, Background: &white}).Colors)
	original, err := NewTheme(foregrounds, backgrounds, TerminalColorModeTrueColor, ThemeOptions{Name: "custom", Appearance: "light", Dim: []string{"dim"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := original.concreteColors["dim"]; !ok || !slices.Contains(original.defaultForegroundTokens, "text") {
		t.Fatalf("theme: concrete %v, default %v; the test needs a concrete dim and a default text", original.concreteColors, original.defaultForegroundTokens)
	}
	defaults := slices.Clone(original.defaultForegroundTokens)
	grey, whiteText := mustParseColor(t, "#aaaaaa"), mustParseColor(t, "#ffffff")
	derived, err := original.WithTokenColors(map[string]Color{"dim": grey, "text": whiteText})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := derived.GetFgAnsi("dim"), ForegroundAnsi(grey, TerminalColorModeTrueColor)+"\x1b[2m"; got != want {
		t.Errorf("dim = %q, want %q", got, want)
	}
	if got, want := derived.Text, ForegroundAnsi(whiteText, TerminalColorModeTrueColor); got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	keys := derived.ColorKeys()
	if slices.Contains(derived.defaultForegroundTokens, "text") || slices.Index(keys, "text") > slices.Index(keys, derived.defaultForegroundTokens[0]) {
		t.Errorf("text is not a concrete token: keys %q, defaults %q", keys, derived.defaultForegroundTokens)
	}
	if original.Text != SGRFgReset || !slices.Equal(original.defaultForegroundTokens, defaults) {
		t.Errorf("original modified: text %q defaults %q", original.Text, original.defaultForegroundTokens)
	}
}

func TestWithTokenColorsRejectsUnknownToken(t *testing.T) {
	derived, err := builtinDarkTheme().WithTokenColors(map[string]Color{"accent": mustParseColor(t, "#ffffff"), "nope": mustParseColor(t, "#000000")})
	if err == nil || derived != nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("derived %v err %v", derived, err)
	}
}

func TestViewOverridableTokensAreThemeTokens(t *testing.T) {
	dark := builtinDarkTheme()
	for _, token := range ViewOverridableTokens {
		_, fg := dark.fgAnsi[token]
		_, bg := dark.bgAnsi[token]
		if !fg && !bg {
			t.Errorf("%s is not a theme token", token)
		}
	}
}
