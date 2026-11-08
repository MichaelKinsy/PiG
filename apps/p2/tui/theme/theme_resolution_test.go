package theme_test

// pi: apps/p2/test/p2/tui-theme.test.ts "theme resolution: pairs, color modes, custom data, invalid fallback, lowlight highlighting"
// (the runtime-bound cases of that file are in apps/p2/tui/tui_theme_test.go).

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestThemeResolutionPairsColorModesCustomDataInvalidFallbackLowlightHighlighting(t *testing.T) {
	terminal := theme.TerminalState{Scheme: "light"}
	resolve := func(options theme.ResolveOptions) (*theme.Theme, error) { return theme.ResolveTheme(options) }

	got, _ := resolve(theme.ResolveOptions{Setting: "light/dark", Mode: tui.TerminalColorMode256, Terminal: terminal})
	if got.Name != "light" {
		t.Fatalf("light/dark on a light terminal = %q, want light", got.Name)
	}
	got, _ = resolve(theme.ResolveOptions{Setting: "light/dark", Mode: tui.TerminalColorMode256})
	if got.Name != "dark" {
		t.Fatalf("light/dark on an unreported terminal = %q, want dark", got.Name)
	}

	custom := theme.DarkTheme.WithColor("accent", theme.Str("#123456"))
	custom.Name = "mine"
	truecolor, err := resolve(theme.ResolveOptions{Setting: "mine", Themes: map[string]theme.ThemeJSON{"mine": custom}, Mode: tui.TerminalColorModeTrueColor, Terminal: terminal})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`38;2;18;52;86`).MatchString(truecolor.Fg("accent", "x")) {
		t.Fatalf("truecolor accent = %q", truecolor.Fg("accent", "x"))
	}
	indexed, err := theme.CreateTheme(custom, tui.TerminalColorMode256, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`38;5;\d+m`).MatchString(indexed.Fg("accent", "x")) {
		t.Fatalf("256color accent = %q", indexed.Fg("accent", "x"))
	}

	bad := theme.DarkTheme.WithColor("accent", theme.Str("nope"))
	bad.Name = "bad"
	broken, brokenErr := resolve(theme.ResolveOptions{Setting: "bad", Themes: map[string]theme.ThemeJSON{"bad": bad}, Mode: tui.TerminalColorMode256, Terminal: terminal})
	if broken.Name != "system" {
		t.Fatalf("a broken theme falls back to %q, want system", broken.Name)
	}
	if brokenErr == nil || !regexp.MustCompile(`nope|Variable`).MatchString(brokenErr.Error()) {
		t.Fatalf("broken theme error = %v", brokenErr)
	}
	_, missingErr := resolve(theme.ResolveOptions{Setting: "missing", Mode: tui.TerminalColorMode256, Terminal: terminal})
	if missingErr == nil || missingErr.Error() != "Theme not found: missing" {
		t.Fatalf("missing theme error = %v", missingErr)
	}

	light, err := theme.CreateTheme(theme.LightTheme, tui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	lines := light.Highlight("const x = 'a';\n// note", "ts")
	if len(lines) != 2 {
		t.Fatalf("highlighted lines = %q, want 2", lines)
	}
	if !strings.Contains(lines[0], light.GetFgAnsi("syntaxKeyword")) {
		t.Fatalf("line 1 lacks the keyword color: %q", lines[0])
	}
	if !strings.Contains(lines[1], light.GetFgAnsi("syntaxComment")) {
		t.Fatalf("line 2 lacks the comment color: %q", lines[1])
	}
	if got, want := light.Highlight("plain", "nonexistent-language"), []string{light.Fg("mdCodeBlock", "plain")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown language = %q, want %q", got, want)
	}
	if got := light.Bold("b"); got != "\x1b[1mb\x1b[22m" {
		t.Fatalf("Bold = %q", got)
	}
	if got := theme.CreateSystemTheme(tui.TerminalColorMode256, theme.TerminalState{Pending: true}).Name; got != "system" {
		t.Fatalf("system theme name = %q", got)
	}
}
