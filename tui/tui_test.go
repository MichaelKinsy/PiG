package tui

import (
	"fmt"
	"strings"
	"testing"
)

type fixedLinesComponent struct{ lines []string }

func (c *fixedLinesComponent) Render(int) []string { return append([]string(nil), c.lines...) }
func (*fixedLinesComponent) Invalidate()           {}

func TestKittyDeletionPrecedesReplacementTransmission(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	t.Cleanup(ResetCapabilitiesCache)
	imageLine := "\x1b_Ga=T,f=100,c=20,r=3,i=42,C=1;AAAA\x1b\\"
	var output strings.Builder
	ui := NewWithOutput(&output, 80, 24)
	ui.Add(&fixedLinesComponent{lines: []string{imageLine, "", "updated", "agent text"}})
	ui.prevLines = []string{imageLine, "", "", "agent text"}
	ui.hasRendered = true
	ui.prevWidth = 80
	ui.prevHeight = 24
	ui.previousKittyImageIDs = map[int]struct{}{42: {}}
	ui.doRender()

	bytes := output.String()
	deleteAt := strings.Index(bytes, DeleteKittyImage(42))
	imageAt := strings.Index(bytes, imageLine)
	if deleteAt == -1 || imageAt == -1 || deleteAt > imageAt {
		t.Fatalf("Kitty replacement order is delete=%d image=%d: %q", deleteAt, imageAt, bytes)
	}
}

func TestExpandChangedRangeIncludesKittyReservedRows(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	t.Cleanup(ResetCapabilitiesCache)
	imageLine := "\x1b_Ga=T,f=100,c=20,r=3,i=42,C=1;AAAA\x1b\\"
	ui := NewWithOutput(new(strings.Builder), 80, 24)
	ui.prevLines = []string{imageLine, "", "", "agent text"}
	newLines := []string{imageLine, "", "updated", "agent text"}
	first, last := ui.expandChangedRangeForKittyImages(2, 2, newLines)
	if first != 0 || last != 2 {
		t.Fatalf("expanded range = (%d,%d), want (0,2)", first, last)
	}
}

// (2026-04-30 honesty sweep): the previous overlayLine
// stripped ANSI from the bg then per-rune overwrote a center band,
// which (a) left bg plain text visible left+right of the modal and
// (b) sliced overlay ANSI escapes mid-sequence. These tests pin the
// repaired behavior against regression.

func TestOverlayLineClearsBackgroundFully(t *testing.T) {
	bg := "left text \x1b[31mred\x1b[0m middle \x1b[32mgreen\x1b[0m right text"
	overlay := "│ MODAL │"
	col := 20
	termW := 60

	got := overlayLine(bg, overlay, col, termW)

	// Background plain text must NOT survive.
	for _, fragment := range []string{"left text", "right text", "middle"} {
		if strings.Contains(got, fragment) {
			t.Errorf("overlay should fully blank bg row, but %q survives in:\n%q", fragment, got)
		}
	}

	// Background SGR escapes must NOT survive (the leading [0m
	// discards them; we then write only spaces + the overlay).
	if strings.Contains(got, "\x1b[31m") || strings.Contains(got, "\x1b[32m") {
		t.Errorf("overlay should not leak bg SGR escapes, got:\n%q", got)
	}

	// Overlay payload itself must appear intact.
	if !strings.Contains(got, overlay) {
		t.Errorf("overlay payload not present in result:\n%q", got)
	}

	// Result must start with a reset and end with padding/reset.
	if !strings.HasPrefix(got, "\x1b[0m") {
		t.Errorf("expected leading \\x1b[0m, got:\n%q", got)
	}
	if !strings.Contains(got[strings.Index(got, overlay)+len(overlay):], "\x1b[0m") {
		t.Errorf("expected trailing \\x1b[0m after overlay, got:\n%q", got)
	}
}

func TestOverlayLinePadsToTerminalWidth(t *testing.T) {
	overlay := "│ x │"
	col := 10
	termW := 40
	got := overlayLine("anything", overlay, col, termW)

	// Visible width = visible chars (strip ANSI, keep runes).
	visible := stripANSI(got)
	if len([]rune(visible)) != termW {
		t.Errorf("expected visible width = termW (%d), got %d in %q", termW, len([]rune(visible)), visible)
	}

	// Spaces before the overlay must equal `col`.
	idx := strings.Index(visible, "│")
	if idx != col {
		t.Errorf("expected overlay to start at column %d in stripped output, got %d in %q", col, idx, visible)
	}
}

func TestOverlayLinePreservesOverlayANSI(t *testing.T) {
	// Overlay carries its own coloring (e.g. theme accent on title).
	// Our compositor must NOT re-slice these escapes.
	overlay := "\x1b[1;36m│ Tree │\x1b[0m"
	got := overlayLine("bg row", overlay, 5, 30)

	if !strings.Contains(got, "\x1b[1;36m│ Tree │\x1b[0m") {
		t.Errorf("overlay's own ANSI must survive intact, got:\n%q", got)
	}
}

func TestOverlayLineNegativeColClamped(t *testing.T) {
	// Defensive: a layout bug that produces col<0 must not panic
	// (previous impl panicked on negative slice access).
	got := overlayLine("bg", "│M│", -3, 20)
	if got == "" {
		t.Errorf("expected non-empty result for negative col, got empty")
	}
	if !strings.Contains(got, "│M│") {
		t.Errorf("overlay payload missing for negative col, got:\n%q", got)
	}
}

func TestDrawBoxPadsByVisibleRuneWidth(t *testing.T) {
	// Lines containing ANSI escapes must not be over-padded; lines
	// ending in multi-byte runes must not be cut mid-rune.
	// (a) regression: the prior byte-len pad/truncate produced UTF-8
	// mojibake on box-drawing separators.
	lines := []string{
		"\x1b[33mhello\x1b[0m",  // 5 visible cells, 13 bytes
		strings.Repeat("─", 12), // 12 visible cells, 36 bytes
	}
	out := drawBox("T", lines, 20, 6)

	// Every emitted row (including borders) must be exactly 20
	// visible cells wide.
	for i, row := range out {
		w := len([]rune(stripANSI(row)))
		if w != 20 {
			t.Errorf("row %d (%q): visible width = %d, want 20", i, row, w)
		}
	}

	// No mojibake byte (0xef 0xbf 0xbd = U+FFFD replacement char) anywhere.
	joined := strings.Join(out, "\n")
	if strings.ContainsRune(joined, '\uFFFD') {
		t.Errorf("drawBox output contains U+FFFD replacement char (mid-rune cut):\n%s", joined)
	}
}

// TestContainer_SetMaxLines_CapsOutput: more children than maxLines → only last N lines returned.
func TestContainer_SetMaxLines_CapsOutput(t *testing.T) {
	c := NewContainer()
	for range 10 {
		c.Add(NewText("line"))
	}
	c.SetMaxLines(3)
	lines := c.Render(80)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
}

// TestContainer_SetMaxLines_Zero_Unlimited: n=0 returns all lines.
func TestContainer_SetMaxLines_Zero_Unlimited(t *testing.T) {
	c := NewContainer()
	for range 10 {
		c.Add(NewText("line"))
	}
	c.SetMaxLines(0)
	lines := c.Render(80)
	if len(lines) != 10 {
		t.Fatalf("want 10 lines, got %d", len(lines))
	}
}

// TestContainer_SetMaxLines_MoreThanChildren: cap > children → all lines returned (no panic).
func TestContainer_SetMaxLines_MoreThanChildren(t *testing.T) {
	c := NewContainer()
	for range 3 {
		c.Add(NewText("line"))
	}
	c.SetMaxLines(10)
	lines := c.Render(80)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (no cap), got %d", len(lines))
	}
}

// countingText records how many times Render was called so we can assert
// that capped rendering only touches the trailing children.
type countingText struct {
	invalidatable
	text    string
	renders *int
}

func (c *countingText) Render(int) []string {
	*c.renders++
	return []string{c.text}
}

// TestContainer_SetMaxLines_RendersOnlyTail: with a cap, Render must return the
// last N lines AND avoid rendering the whole history (the O(n)-per-frame stall
// that froze /tree on long sessions).
func TestContainer_SetMaxLines_RendersOnlyTail(t *testing.T) {
	c := NewContainer()
	renders := make([]int, 100)
	for i := range renders {
		c.Add(&countingText{text: fmt.Sprintf("line-%d", i), renders: &renders[i]})
	}
	c.SetMaxLines(3)
	lines := c.Render(80)

	want := []string{"line-97", "line-98", "line-99"}
	if len(lines) != 3 || lines[0] != want[0] || lines[1] != want[1] || lines[2] != want[2] {
		t.Fatalf("want last 3 children %v, got %v", want, lines)
	}
	// Only the trailing children needed to fill the cap may be rendered.
	for i, n := range renders {
		if i >= 97 && n != 1 {
			t.Fatalf("trailing child %d: want 1 render, got %d", i, n)
		}
		if i < 97 && n != 0 {
			t.Fatalf("leading child %d rendered %d times; capped render must skip history", i, n)
		}
	}
}
