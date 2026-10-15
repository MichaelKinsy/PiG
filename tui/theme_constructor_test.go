package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/source"
)

// theme.ts Theme constructor: the options name, sourcePath, sourceInfo, appearance and dim become the theme's name, source, appearance and faint tokens.
func TestNewThemeCarriesItsOptions(t *testing.T) {
	saturation := 0.0
	generated := GenerateSystemThemeColors(SystemThemeInput{Saturation: &saturation, AppearanceHint: "dark"})
	foregrounds, backgrounds := splitThemeTokenValues(generated.Colors)
	info := &source.SourceInfo{Path: "/ext/theme.json", Source: "npm:pkg", Scope: "user", Origin: "package", BaseDir: "/ext"}
	theme, err := NewTheme(foregrounds, backgrounds, TerminalColorModeTrueColor, ThemeOptions{Name: "custom", SourcePath: "/ext/theme.json", SourceInfo: info, Appearance: "light", Dim: []string{"muted"}})
	if err != nil {
		t.Fatal(err)
	}
	if theme.Name != "custom" || theme.SourceInfo != info {
		t.Fatalf("name = %q, sourceInfo = %v, want custom and the option's value", theme.Name, theme.SourceInfo)
	}
	if got := theme.Fg("muted", "x"); !strings.Contains(got, "\x1b[2m") {
		t.Fatalf("dim option: muted renders %q, want a faint (SGR 2) token", got)
	}
	plain, err := NewTheme(foregrounds, backgrounds, TerminalColorModeTrueColor, ThemeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.SourceInfo != nil || plain.Name != "" || strings.Contains(plain.Fg("muted", "x"), "\x1b[2m") {
		t.Fatalf("a theme without options has name %q, sourceInfo %v, faint muted %v", plain.Name, plain.SourceInfo, strings.Contains(plain.Fg("muted", "x"), "\x1b[2m"))
	}
}

// theme.ts Theme constructor: a token without a value throws "Invalid color value: undefined".
func TestNewThemeRejectsAMissingColor(t *testing.T) {
	if _, err := NewTheme([]ThemeTokenValue{{Token: "text"}}, nil, TerminalColorModeTrueColor, ThemeOptions{}); err == nil || err.Error() != "Invalid color value: undefined" {
		t.Fatalf("error = %v, want Invalid color value: undefined", err)
	}
}
