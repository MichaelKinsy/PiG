package tui

import (
	"strings"
	"testing"
)

// TestEditor_ScrollOffset_ShortContentNoScroll verifies that small
// buffers render in full with the plain top/bottom dividers.
func TestEditor_ScrollOffset_ShortContentNoScroll(t *testing.T) {
	ed := NewEditor()
	ed.SetMaxVisibleLines(5)
	ed.SetText("a\nb\nc")
	out := strings.Join(ed.Render(20), "\n")
	if strings.Contains(out, "more") {
		t.Fatalf("short content should not show scroll indicator, got %q", out)
	}
}

// TestEditor_ScrollOffset_TallContentClampsAndShowsIndicators verifies
// that buffers exceeding maxVisibleLines clamp the rendered window and
// add the "↓ N more" indicator on the bottom border. The cursor lives
// at line 0, so scrollOffset stays 0 and only the bottom indicator appears.
// Mirrors upstream editor.ts:451-465 + :540-560.
func TestEditor_ScrollOffset_TallContentClampsAndShowsIndicators(t *testing.T) {
	ed := NewEditor()
	ed.SetMaxVisibleLines(3)
	var b strings.Builder
	for range 10 {
		b.WriteString("line\n")
	}
	b.WriteString("end")
	ed.SetText(b.String())
	// Move cursor back to line 0 col 0 so scrollOffset = 0.
	ed.cursor = [2]int{0, 0}
	out := ed.Render(40)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "↓") {
		t.Fatalf("expected ↓ scroll indicator, got %q", joined)
	}
	if strings.Contains(joined, "↑") {
		t.Fatalf("did not expect ↑ indicator when at top, got %q", joined)
	}
	// Visible content rows: maxVisible(3) + top border + bottom border + leading blank
	if len(out) != 1+1+3+1 {
		t.Fatalf("expected %d rows, got %d (%q)", 1+1+3+1, len(out), out)
	}
}

// TestEditor_ScrollOffset_CursorAtEndShowsUpIndicator verifies the
// scroll-down branch: with the cursor on the last logical line, the
// editor scrolls so the cursor stays visible and emits "↑ N more" on
// the top border.
func TestEditor_ScrollOffset_CursorAtEndShowsUpIndicator(t *testing.T) {
	ed := NewEditor()
	ed.SetMaxVisibleLines(3)
	var b strings.Builder
	for range 10 {
		b.WriteString("line\n")
	}
	b.WriteString("end")
	ed.SetText(b.String())
	// SetText positions cursor at end of last line.
	out := ed.Render(40)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "↑") {
		t.Fatalf("expected ↑ indicator when cursor is at end of tall buffer, got %q", joined)
	}
}

// TestEditor_ScrollOffset_ResetOnClear verifies Clear() drops the
// scroll offset so the next edit starts at the top.
func TestEditor_ScrollOffset_ResetOnClear(t *testing.T) {
	ed := NewEditor()
	ed.SetMaxVisibleLines(3)
	var b strings.Builder
	for range 15 {
		b.WriteString("x\n")
	}
	ed.SetText(b.String())
	_ = ed.Render(20) // adjust scroll
	if ed.scrollOffset == 0 {
		t.Fatalf("scroll offset should advance for tall buffer")
	}
	ed.Clear()
	if ed.scrollOffset != 0 {
		t.Fatalf("Clear should reset scroll offset, got %d", ed.scrollOffset)
	}
}
