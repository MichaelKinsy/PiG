package tui

import (
	"slices"
	"testing"
)

// TestMarkdownStyledWordsAtTheWrapBoundaryPiCase ports Pi 2ce9d527 (#10925, packages/tui/test/markdown.test.ts "styled words at
// the wrap boundary"): padded Markdown with a background, wrapped where a bold and a struck word end at the wrap width, starts no
// line with a space and carries no style into the padding.
func TestMarkdownStyledWordsAtTheWrapBoundaryPiCase(t *testing.T) {
	mdCaseSetup(t)
	style := &DefaultTextStyle{BgColor: func(text string) string { return "\x1b[44m" + text + "\x1b[49m" }}
	got := NewMarkdownWithOptions("aaaa **bold** next ~~strk~~ more words", 1, 0, mdUpstreamTheme(), style, nil).Render(11)
	want := []string{
		"\x1b[44m aaaa \x1b[1mbold\x1b[22m \x1b[49m",
		"\x1b[44m next \x1b[9mstrk\x1b[29m \x1b[49m",
		"\x1b[44m more      \x1b[49m",
		"\x1b[44m words     \x1b[49m",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Render(11) = %q, want %q", got, want)
	}
}
