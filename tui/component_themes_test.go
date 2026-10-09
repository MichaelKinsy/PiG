package tui

import (
	"slices"
	"testing"
)

func componentThemeOverride(t *testing.T) *Theme {
	t.Helper()
	derived, err := builtinDarkTheme().WithTokenColors(map[string]Color{
		"accent": mustParseColor(t, "#d75f00"), "muted": mustParseColor(t, "#123456"), "dim": mustParseColor(t, "#654321"),
		"mdHeading": mustParseColor(t, "#abcdef"), "mdCodeBlock": mustParseColor(t, "#fedcba"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return derived
}

// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts getSelectListTheme.
func TestComponentThemeSelectList(t *testing.T) {
	th := componentThemeOverride(t)
	theme := SelectListThemeFor(th)
	accent, muted := th.Fg("accent", "x"), th.Fg("muted", "x")
	got := []string{theme.SelectedPrefix("x"), theme.SelectedText("x"), theme.Description("x"), theme.ScrollInfo("x"), theme.NoMatch("x")}
	if want := []string{accent, accent, muted, muted, muted}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if accent == builtinDarkTheme().Fg("accent", "x") {
		t.Fatal("theme not bound to the given theme")
	}
}

// upstream: theme.ts getSettingsListTheme.
func TestComponentThemeSettingsList(t *testing.T) {
	th := componentThemeOverride(t)
	theme := SettingsListThemeFor(th)
	got := []string{theme.Label("x", true), theme.Label("x", false), theme.Value("x", true), theme.Value("x", false), theme.Description("x"), theme.Cursor, theme.Hint("x")}
	want := []string{th.Fg("accent", "x"), "x", th.Fg("accent", "x"), th.Fg("muted", "x"), th.Fg("dim", "x"), th.Fg("accent", "→ "), th.Fg("dim", "x")}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func markdownThemeStyles(theme MarkdownTheme) []string {
	var out []string
	for _, style := range []func(string) string{
		theme.Heading, theme.Link, theme.LinkUrl, theme.Code, theme.CodeBlock, theme.CodeBlockBorder,
		theme.Quote, theme.QuoteBorder, theme.Hr, theme.ListBullet, theme.Bold, theme.Italic, theme.Strikethrough, theme.Underline,
	} {
		out = append(out, style("x"))
	}
	return out
}

// upstream: theme.ts getMarkdownTheme.
func TestComponentThemeMarkdownBoundToTheme(t *testing.T) {
	th := componentThemeOverride(t)
	theme := MarkdownThemeFor(th)
	if got, want := theme.Heading("x"), th.Fg("mdHeading", "x"); got != want {
		t.Fatalf("Heading = %q, want %q", got, want)
	}
	if got, want := theme.Bold("x"), "\x1b[1mx\x1b[22m"; got != want {
		t.Fatalf("Bold = %q, want %q", got, want)
	}
	if got, want := theme.HighlightCode("a\nb", ""), codeBlockLines("a\nb", th); !slices.Equal(got, want) {
		t.Fatalf("HighlightCode = %q, want %q", got, want)
	}
	if got, want := theme.HighlightCode("x := 1", "go"), highlightCodeUncached(highlightRegistry(), "x := 1", "go", true, th); !slices.Equal(got, want) {
		t.Fatalf("HighlightCode(go) = %q, want %q", got, want)
	}
	if slices.Equal(theme.HighlightCode("a", ""), codeBlockLines("a", builtinDarkTheme())) {
		t.Fatal("code block lines not bound to the given theme")
	}
}

// GetMarkdownTheme equals MarkdownThemeFor(ActiveTheme()) and reads the
// active theme at every style call.
func TestComponentThemeDefaultMarkdownFollowsActiveTheme(t *testing.T) {
	previous := activeTheme.Load()
	t.Cleanup(func() { activeTheme.Store(previous) })
	activeTheme.Store(builtinDarkTheme())

	theme := GetMarkdownTheme()
	if got, want := markdownThemeStyles(theme), markdownThemeStyles(MarkdownThemeFor(ActiveTheme())); !slices.Equal(got, want) {
		t.Fatalf("default %q != bound %q", got, want)
	}
	if got, want := theme.HighlightCode("a", ""), highlightMarkdownCode("a", ""); !slices.Equal(got, want) {
		t.Fatalf("HighlightCode = %q, want %q", got, want)
	}
	override := componentThemeOverride(t)
	activeTheme.Store(override)
	if got, want := theme.Heading("x"), override.Fg("mdHeading", "x"); got != want {
		t.Fatalf("Heading after theme switch = %q, want %q", got, want)
	}
}
