package tui

import (
	"strings"
	"testing"
)

// upstream: theme.bold, italic, underline, inverse and strikethrough are chalk
// calls (theme.ts:314-331), which return their text unchanged at chalk color
// level 0 (CI=1, TERM=dumb, stdout not a terminal) while theme.fg still colors.
func TestChalkModifiersOffDrawNoMarkdownStyles(t *testing.T) {
	SetTheme("dark")
	const source = "# Heading\n\n> quote\n\n| Name | Value |\n| --- | --- |\n| a | b |\n\n**bold** *italic* ~~gone~~ [docs](https://example.com)\n"
	theme := GetMarkdownTheme()
	render := func() string {
		return strings.Join(NewMarkdownWithOptions(source, 0, 0, &theme, nil, &MarkdownOptions{}).Render(60), "\n")
	}
	modifiers := []string{"\x1b[1m", "\x1b[3m", "\x1b[4m", "\x1b[7m", "\x1b[9m"}

	SetChalkModifiers(true)
	on := render()
	for _, m := range modifiers {
		if m == "\x1b[7m" {
			continue
		}
		if !strings.Contains(on, m) {
			t.Fatalf("modifiers on: rendering lacks %q:\n%q", m, on)
		}
	}

	SetChalkModifiers(false)
	t.Cleanup(func() { SetChalkModifiers(true) })
	off := render()
	for _, m := range modifiers {
		if strings.Contains(off, m) {
			t.Errorf("modifiers off: rendering holds %q:\n%q", m, off)
		}
	}
	if !strings.Contains(off, ActiveTheme().MDHeading) {
		t.Errorf("modifiers off: heading lost its theme.fg color:\n%q", off)
	}
	for name, got := range map[string]string{
		"bold": ChalkBold("x"), "italic": ChalkItalic("x"), "underline": ChalkUnderline("x"),
		"inverse": ChalkInverse("x"), "strikethrough": ChalkStrikethrough("x"), "Theme.Inverse": ActiveTheme().Inverse("x"),
		"boldText": boldText("x"),
	} {
		if got != "x" {
			t.Errorf("modifiers off: %s = %q, want plain text", name, got)
		}
	}
}
