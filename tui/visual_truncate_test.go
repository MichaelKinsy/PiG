package tui

// pi: packages/coding-agent/src/modes/interactive/components/visual-truncate.ts

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// plain is a line without its ANSI sequences and the spaces Text pads it with to the width.
func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(widthx.StripAnsi(line), " ")
	}
	return out
}

// visual-truncate.ts (0.99.2): truncateToVisualLines keeps the last visual lines by default or, with keep "start", the
// first ones; VisualLinePreview puts the hint before kept end lines and after kept start lines. The upstream tree has no
// test file for either; the cases follow the branches of the source and the inputs of codemode-renderer.test.ts.

// Pi: packages/coding-agent/src/modes/interactive/components/visual-truncate.ts:10 (VisualTruncateResult.visualLines); packages/coding-agent/src/modes/interactive/components/visual-truncate.ts:12 (VisualTruncateResult.skippedCount).
func TestTruncateToVisualLinesKeepsTheEndByDefaultAndTheStartOnRequest(t *testing.T) {
	text := "one\ntwo\nthree\nfour\nfive"
	for _, tc := range []struct {
		name string
		got  VisualTruncateResult
		want []string
	}{
		{"default", TruncateToVisualLines(text, 2, 20), []string{"four", "five"}},
		{"keep end", TruncateToVisualLinesKeeping(text, 2, 20, 0, VisualKeepEnd), []string{"four", "five"}},
		{"keep start", TruncateToVisualLinesKeeping(text, 2, 20, 0, VisualKeepStart), []string{"one", "two"}},
	} {
		if !slices.Equal(plain(tc.got.VisualLines), tc.want) || tc.got.SkippedCount != 3 {
			t.Errorf("%s: %q skipped %d, want %q skipped 3", tc.name, plain(tc.got.VisualLines), tc.got.SkippedCount, tc.want)
		}
	}
	// Text that fits, or no text, is returned whole whichever end is kept.
	fits := TruncateToVisualLinesKeeping("one\ntwo", 2, 20, 0, VisualKeepStart)
	if !slices.Equal(plain(fits.VisualLines), []string{"one", "two"}) || fits.SkippedCount != 0 {
		t.Errorf("fits: %+v", fits)
	}
	if empty := TruncateToVisualLinesKeeping("", 2, 20, 0, VisualKeepStart); len(empty.VisualLines) != 0 || empty.SkippedCount != 0 {
		t.Errorf("empty: %+v", empty)
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/visual-truncate.ts:10 (VisualTruncateResult.visualLines); packages/coding-agent/src/modes/interactive/components/visual-truncate.ts:12 (VisualTruncateResult.skippedCount).
func TestTruncateToVisualLinesCountsWrappedLinesNotLogicalLines(t *testing.T) {
	result := TruncateToVisualLinesKeeping(strings.Repeat("x", 1000), 5, 50, 0, VisualKeepStart)
	if len(result.VisualLines) != 5 || result.SkippedCount != 15 || result.VisualLines[0] != strings.Repeat("x", 50) {
		t.Errorf("got %d lines, skipped %d", len(result.VisualLines), result.SkippedCount)
	}
}

func hintOf(hidden int) string {
	return "hidden:" + string(rune('0'+hidden%10)) + strings.Repeat("!", hidden/10)
}

func TestVisualLinePreviewPlacesTheHintAfterKeptStartLinesAndBeforeKeptEndLines(t *testing.T) {
	text := "one\ntwo\nthree\nfour"
	start := NewVisualLinePreview(VisualLinePreviewOptions{Text: text, MaxVisualLines: 2, Keep: VisualKeepStart, FormatHint: hintOf})
	if got := start.Render(20); !slices.Equal(plain(got), []string{"one", "two", "hidden:2"}) {
		t.Errorf("keep start: %q", got)
	}
	end := NewVisualLinePreview(VisualLinePreviewOptions{Text: text, MaxVisualLines: 2, Keep: VisualKeepEnd, FormatHint: hintOf})
	if got := end.Render(20); !slices.Equal(plain(got), []string{"hidden:2", "three", "four"}) {
		t.Errorf("keep end: %q", got)
	}
	// Nothing hidden: no hint.
	whole := NewVisualLinePreview(VisualLinePreviewOptions{Text: "one\ntwo", MaxVisualLines: 2, Keep: VisualKeepStart, FormatHint: hintOf})
	if got := whole.Render(20); !slices.Equal(plain(got), []string{"one", "two"}) {
		t.Errorf("fits: %q", got)
	}
}

func TestVisualLinePreviewTruncatesTheHintToTheWidth(t *testing.T) {
	preview := NewVisualLinePreview(VisualLinePreviewOptions{
		Text: "one\ntwo\nthree", MaxVisualLines: 1, Keep: VisualKeepStart,
		FormatHint: func(int) string { return "a hint that is much too long for the width" },
	})
	got := preview.Render(10)
	if len(got) != 2 || plain(got)[1] != "a hint ..." {
		t.Errorf("got %q, want the hint cut to the width with an ellipsis", got)
	}
}

// The preview renders on every frame for every result in the transcript: it computes its lines once per width.
func TestVisualLinePreviewCachesItsLinesPerWidthUntilInvalidated(t *testing.T) {
	calls := 0
	preview := NewVisualLinePreview(VisualLinePreviewOptions{
		Text: strings.Repeat("x", 100), MaxVisualLines: 2, Keep: VisualKeepStart,
		FormatHint: func(hidden int) string { calls++; return hintOf(hidden) },
	})
	first := preview.Render(10)
	preview.Render(10)
	if calls != 1 {
		t.Fatalf("hint formatted %d times for two renders at one width, want 1", calls)
	}
	if got := preview.Render(20); slices.Equal(got, first) || calls != 2 {
		t.Fatalf("a new width gave %q after %d hints, want new lines and a second hint", got, calls)
	}
	preview.Invalidate()
	preview.Render(20)
	if calls != 3 {
		t.Fatalf("hint formatted %d times after Invalidate, want 3", calls)
	}
}

// visual-truncate.ts:42-44: with keep "end" the kept lines are allVisualLines.slice(-maxVisualLines), and slice(-0) is the whole
// array, so a limit of zero keeps every line while skippedCount is still length - 0.
func TestTruncateToVisualLinesWithAZeroLimitFollowsSliceMinusZero(t *testing.T) {
	end := TruncateToVisualLinesKeeping("a\nb\nc", 0, 20, 0, VisualKeepEnd)
	if len(end.VisualLines) != 3 || end.SkippedCount != 3 {
		t.Fatalf("keep end: %d lines, %d skipped; want 3 and 3", len(end.VisualLines), end.SkippedCount)
	}
	start := TruncateToVisualLinesKeeping("a\nb\nc", 0, 20, 0, VisualKeepStart)
	if len(start.VisualLines) != 0 || start.SkippedCount != 3 {
		t.Fatalf("keep start: %d lines, %d skipped; want 0 and 3", len(start.VisualLines), start.SkippedCount)
	}
}
