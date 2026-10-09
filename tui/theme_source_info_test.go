package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/source"
)

// Pi: packages/coding-agent/src/modes/interactive/theme/theme.ts:259-277 (Theme constructor options name, sourcePath, sourceInfo): the options land on the theme's name, sourcePath and sourceInfo.
func TestNewThemeKeepsTheConstructorOptionsSourceInfo(t *testing.T) {
	restoreBakedThemeState(t)
	black, white := RgbColor{}, RgbColor{R: 255, G: 255, B: 255}
	foregrounds, backgrounds := splitThemeTokenValues(GenerateSystemThemeColors(SystemThemeInput{Foreground: &black, Background: &white}).Colors)
	info := &source.SourceInfo{Path: "/themes/named.json", Source: "local"}
	theme, err := NewTheme(foregrounds, backgrounds, TerminalColorModeTrueColor, ThemeOptions{Name: "named", SourcePath: "/themes/named.json", SourceInfo: info})
	if err != nil {
		t.Fatal(err)
	}
	if theme.Name != "named" || theme.SourcePath != "/themes/named.json" || theme.SourceInfo != info {
		t.Fatalf("theme options = name %q sourcePath %q sourceInfo %p, want the constructor options (%p)", theme.Name, theme.SourcePath, theme.SourceInfo, info)
	}
	plain, err := NewTheme(foregrounds, backgrounds, TerminalColorModeTrueColor, ThemeOptions{})
	if err != nil || plain.SourceInfo != nil {
		t.Fatalf("a theme built without sourceInfo has %v (err %v), want none", plain.SourceInfo, err)
	}
}
