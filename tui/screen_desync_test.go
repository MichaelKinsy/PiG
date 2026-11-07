package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A row that the renderer measures as fitting but the terminal draws one cell
// wider wraps onto a second physical row. Every screen position below it then
// shifts, and the differential renderer's per-row erase reaches only the first
// physical row, so stale content survives repaints.
//
// This is the screen-level guard for the emoji width fix. Byte assertions
// cannot see it: the renderer emits the same reasonable output either way, and
// only replaying onto a screen shows the row landing in the wrong place.
//
// The line below is sized so its width decides whether it wraps. ⚠️ is
// U+26A0 with VS16, which go-runewidth counts as one cell and terminals draw as
// two, so measuring it wrongly is exactly the one-cell error that wraps it.
func TestEmojiRowDoesNotDesyncScreenPositions(t *testing.T) {
	const width, height = 40, 12

	// Fill the row to the terminal edge, counting the emoji as the two cells a
	// terminal actually draws.
	emoji := "\u26A0\uFE0F"
	label := strings.Repeat("a", width-2) // ⚠️ occupies two terminal cells
	overWide := label + emoji

	// The row fills the pane exactly in real terminal cells. If VisibleWidth
	// undercounts the emoji it reports width-1, believes a further cell is free,
	// and the row it emits wraps.
	if got := widthx.VisibleWidth(overWide); got != width {
		t.Fatalf("VisibleWidth reports %d cells for a row that occupies %d, so it will wrap unexpectedly", got, width)
	}

	sc := newScreen(width, height)
	ui := NewWithOutput(sc, width, height)
	ui.Add(NewText(overWide))
	marker := NewText("MARKER")
	ui.Add(marker)
	ui.Render()

	if n := sc.countRowsContaining("MARKER"); n != 1 {
		t.Fatalf("marker appears on %d rows, want 1", n)
		for r, row := range sc.Visible() {
			t.Logf("  row %2d: %q", r, widthx.StripAnsi(row))
		}
	}

	// Re-render with a changed marker. If the emoji row occupied more physical
	// rows than the renderer believed, the update lands on the wrong row and the
	// old marker survives.
	marker.SetText("MARKER2")
	ui.Render()

	if n := sc.countRowsContaining("MARKER"); n != 1 {
		for r, row := range sc.Visible() {
			t.Logf("  row %2d: %q", r, widthx.StripAnsi(row))
		}
		t.Errorf("after update, %d rows still contain a marker, want 1: the emoji row desynced screen positions", n)
	}
	if len(sc.unhandled) > 0 {
		t.Errorf("screen model met sequences it does not decode, so this result is not trustworthy: %v", sc.unhandled)
	}
}

// The screen model is only useful if it agrees with a plain, well-understood
// render. This pins its basic behaviour so a bug in the model cannot quietly
// turn into a false pass above.
func TestScreenModelReplaysASimpleRender(t *testing.T) {
	sc := newScreen(20, 6)
	ui := NewWithOutput(sc, 20, 6)
	ui.Add(NewText("first"))
	ui.Add(NewText("second"))
	ui.Render()

	rows := sc.Visible()
	// Rows arrive padded to the terminal width, which is what the renderer emits.
	if strings.TrimRight(widthx.StripAnsi(rows[0]), " ") != "first" ||
		strings.TrimRight(widthx.StripAnsi(rows[1]), " ") != "second" {
		t.Fatalf("unexpected screen: %q", rows[:2])
	}
	if len(sc.unhandled) > 0 {
		t.Errorf("undecoded sequences: %v", sc.unhandled)
	}
}

// Auto-wrap must be modelled, or the class of bug this file exists to catch
// cannot be reproduced at all.
func TestScreenModelWrapsPastTheRightEdge(t *testing.T) {
	sc := newScreen(10, 4)
	if _, err := sc.Write([]byte("abcdefghijKLM")); err != nil {
		t.Fatal(err)
	}
	rows := sc.Visible()
	if rows[0] != "abcdefghij" || rows[1] != "KLM" {
		t.Errorf("wrap not modelled: row0=%q row1=%q", rows[0], rows[1])
	}
}
