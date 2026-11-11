package widthx

import (
	"slices"
	"strings"
	"testing"
)

// TestWrapTextWithAnsiStyledWordsAtTheWrapBoundaryPiCases ports Pi 2ce9d527 (#10925, packages/tui/test/wrap-ansi.test.ts "styled
// words at the wrap boundary") with Pi's inputs and expected lines. splitIntoTokensWithAnsi attaches the code that closes a styled
// word to the following space, so a styled word that ends at the wrap width must still drop that space, and strikethrough must be
// closed at line end like underline.
func TestWrapTextWithAnsiStyledWordsAtTheWrapBoundaryPiCases(t *testing.T) {
	equal := func(t *testing.T, text string, width int, want ...string) {
		t.Helper()
		if got := WrapTextWithAnsi(text, width); !slices.Equal(got, want) {
			t.Fatalf("WrapTextWithAnsi(%q, %d) = %q, want %q", text, width, got, want)
		}
	}
	t.Run("keeps the closing code on the wrapped line and drops the following space", func(t *testing.T) {
		equal(t, "aaaa \x1b[1mbold\x1b[22m next word", 9, "aaaa \x1b[1mbold\x1b[22m", "next word")
	})
	t.Run("does not re-open a style closed by the dropped space", func(t *testing.T) {
		equal(t, "aaaa \x1b[31mred\x1b[39m  next", 8, "aaaa \x1b[31mred\x1b[39m", "next")
	})
	t.Run("does not exceed width for styled whitespace wider than the line", func(t *testing.T) {
		for _, text := range []string{
			"\x1b[41m" + strings.Repeat(" ", 20) + "\x1b[0m",
			"ab\x1b[41m" + strings.Repeat(" ", 20) + "\x1b[49mcd",
			"\x1b[33m" + strings.Repeat(" ", 20) + "x\x1b[39m",
		} {
			for _, line := range WrapTextWithAnsi(text, 10) {
				if VisibleWidth(line) > 10 {
					t.Errorf("%q: line %q is %d columns wide", text, line, VisibleWidth(line))
				}
			}
		}
	})
	t.Run("trims trailing spaces before the line-end reset when a long word follows", func(t *testing.T) {
		equal(t, "aaa\x1b[9m    abcdefghij\x1b[29m", 9, "aaa\x1b[9m\x1b[29m", "\x1b[9mabcdefghi\x1b[29m", "\x1b[9mj\x1b[29m")
	})
	t.Run("resets strikethrough at line end and restores it on the next line", func(t *testing.T) {
		equal(t, "aaaa \x1b[9mstrike me\x1b[29m next", 12, "aaaa \x1b[9mstrike\x1b[29m", "\x1b[9mme\x1b[29m next")
	})
	t.Run("preserves background when resetting strikethrough at line end", func(t *testing.T) {
		equal(t, "\x1b[41;9mstrike me\x1b[0m", 6, "\x1b[41;9mstrike\x1b[29m", "\x1b[9;41mme\x1b[0m")
	})
}

// TestLineEndResetClosesStrikethrough pins the tracker half of Pi 2ce9d527: AnsiCodeTracker.getLineEndReset closes strikethrough
// (\x1b[29m) as well as underline, in that order and before an active hyperlink.
func TestLineEndResetClosesStrikethrough(t *testing.T) {
	var tr AnsiCodeTracker
	tr.UpdateFromText("\x1b[9m")
	if got := tr.LineEndReset(); got != "\x1b[29m" {
		t.Fatalf("LineEndReset with strikethrough = %q, want %q", got, "\x1b[29m")
	}
	tr.UpdateFromText("\x1b[4m")
	if got := tr.LineEndReset(); got != "\x1b[24m\x1b[29m" {
		t.Fatalf("LineEndReset with underline and strikethrough = %q, want %q", got, "\x1b[24m\x1b[29m")
	}
	tr.UpdateFromText("\x1b[29m\x1b[24m\x1b[41m")
	if got := tr.LineEndReset(); got != "" {
		t.Fatalf("LineEndReset with only a background = %q, want none", got)
	}
}
