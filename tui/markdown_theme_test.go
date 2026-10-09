package tui

import "testing"

// theme.ts getMarkdownTheme: each markdown function colors with the active theme's md* token when it runs, so a theme switch changes the result of a theme taken earlier.
func TestGetMarkdownThemeFollowsTheActiveTheme(t *testing.T) {
	previous := ActiveTheme()
	t.Cleanup(func() { activeTheme.Store(previous) })
	SetTheme("dark")
	markdown := GetMarkdownTheme()
	dark := markdown.Heading("title")
	if want := ActiveTheme().MDHeading + "title" + FgClose(ActiveTheme().MDHeading); dark != want {
		t.Fatalf("Heading = %q, want the dark mdHeading span %q", dark, want)
	}
	SetTheme("light")
	if light := markdown.Heading("title"); light == dark || light != ActiveTheme().MDHeading+"title"+FgClose(ActiveTheme().MDHeading) {
		t.Fatalf("Heading after the switch = %q, dark was %q", light, dark)
	}
	if got := markdown.Bold("x"); got != "\x1b[1mx"+SGRBoldDimReset {
		t.Fatalf("Bold = %q", got)
	}
}
