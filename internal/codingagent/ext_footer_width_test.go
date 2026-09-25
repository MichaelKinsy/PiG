package codingagent

import (
	"slices"
	"strings"
	"testing"
)

// Header and footer rows are painted as rendered, as upstream's component
// factories are: a row that does not fit reaches the renderer's overflow
// check instead of being re-flowed by the host.
func TestExtensionFooterPaintsCurrentWidthRowsAsRendered(t *testing.T) {
	wide := strings.Repeat("x", 90)
	c := newSpecialLinesComponent(func() {})
	c.SetLines([]string{wide})
	if got := c.Render(60); !slices.Equal(got, []string{wide}) {
		t.Fatalf("unknown-width lines = %q, want unchanged", got)
	}
	c.SetLinesAt([]string{wide}, 60)
	if got := c.Render(60); !slices.Equal(got, []string{wide}) {
		t.Fatalf("current-width lines = %q, want unchanged", got)
	}
	c.SetRenderer(func(int) []string { return []string{wide} })
	if got := c.Render(60); !slices.Equal(got, []string{wide}) {
		t.Fatalf("renderer rows = %q, want unchanged", got)
	}
}

// A subprocess frame rendered at another width is never painted, whether its
// rows fit or not; a frame for a width the terminal returns to is valid again.
func TestExtensionFooterNeverPaintsStaleOverWideFrame(t *testing.T) {
	row := strings.Repeat("y", 70)
	c := newSpecialLinesComponent(func() {})
	c.SetLinesAt([]string{row}, 100)
	if got := c.Render(100); !slices.Equal(got, []string{row}) {
		t.Fatalf("frame at its width = %q", got)
	}
	if got := c.Render(60); got != nil {
		t.Fatalf("stale over-wide frame painted at 60: %q", got)
	}
	if got := c.Render(100); !slices.Equal(got, []string{row}) {
		t.Fatalf("frame after resizing back = %q", got)
	}
	c.SetLinesAt([]string{"fits"}, 100)
	if got := c.Render(60); got != nil {
		t.Fatalf("fitting stale frame painted when narrower: %q", got)
	}
	if got := c.Render(120); got != nil {
		t.Fatalf("stale frame painted when wider: %q", got)
	}
	c.SetLinesAt([]string{strings.Repeat("z", 60)}, 60)
	if got := c.Render(60); len(got) != 1 {
		t.Fatalf("fresh frame = %q", got)
	}
}

// Clamping must not corrupt content that already fits, including wide runes.
func TestExtensionFooterLeavesFittingLinesByteIdentical(t *testing.T) {
	const width = 40
	c := newSpecialLinesComponent(func() {})
	in := []string{"\x1b[2mdim status\x1b[0m", "\u65e5\u672c\u8a9e", ""}
	c.SetLines(in)

	got := c.Render(width)
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("line %d changed although it fits:\n got  %q\n want %q", i, got[i], in[i])
		}
	}
}

func TestExtensionFooterFittingLinesAreNotReflowed(t *testing.T) {
	const width = 60
	c := newSpecialLinesComponent(func() {})
	in := []string{"short", "also short"}
	c.SetLines(in)

	got := c.Render(width)
	if len(got) != len(in) {
		t.Fatalf("row count changed for content that fits: %d -> %d", len(in), len(got))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("row %d was rewritten although it fits:\n got  %q\n want %q", i, got[i], in[i])
		}
	}
}
