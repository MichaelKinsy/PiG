package tui

import (
	"strings"
	"testing"
)

// TestBorderedLoader_Render_Structure verifies the bordered loader renders
// the expected structural elements: top border, loader line, optional
// cancel hint, spacer, and bottom border.
// Mirrors upstream bordered-loader.ts (68 LOC).
func TestBorderedLoader_Render_Structure(t *testing.T) {
	t.Run("cancellable", func(t *testing.T) {
		bl := NewBorderedLoader("Loading data...", true)
		lines := bl.Render(80)

		// Must have: border + loader + blank + hint + blank + border = 6 lines minimum.
		if len(lines) < 6 {
			t.Fatalf("expected >= 6 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
		}

		// First line: dynamic border (all ─ chars).
		first := stripANSI(lines[0])
		if !strings.ContainsRune(first, '─') {
			t.Errorf("first line should be a border, got: %q", first)
		}

		// Last line: dynamic border.
		last := stripANSI(lines[len(lines)-1])
		if !strings.ContainsRune(last, '─') {
			t.Errorf("last line should be a border, got: %q", last)
		}

		// Should contain the message text.
		all := stripANSI(strings.Join(lines, "\n"))
		if !strings.Contains(all, "Loading data") {
			t.Errorf("output should contain loader message, got:\n%s", all)
		}

		// Should contain the upstream tui.select.cancel hint.
		if !strings.Contains(all, " escape/ctrl+c cancel") {
			t.Errorf("cancellable loader should show Esc cancel hint, got:\n%s", all)
		}
	})

	t.Run("non-cancellable", func(t *testing.T) {
		bl := NewBorderedLoader("Processing...", false)
		lines := bl.Render(80)

		// Must have: border + loader + blank + border = 4 lines minimum.
		if len(lines) < 4 {
			t.Fatalf("expected >= 4 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
		}

		// First line: dynamic border.
		first := stripANSI(lines[0])
		if !strings.ContainsRune(first, '─') {
			t.Errorf("first line should be a border, got: %q", first)
		}

		// Last line: dynamic border.
		last := stripANSI(lines[len(lines)-1])
		if !strings.ContainsRune(last, '─') {
			t.Errorf("last line should be a border, got: %q", last)
		}

		// Should contain the message text.
		all := stripANSI(strings.Join(lines, "\n"))
		if !strings.Contains(all, "Processing") {
			t.Errorf("output should contain loader message, got:\n%s", all)
		}

		// Should NOT contain cancel hint.
		if strings.Contains(all, "cancel") {
			t.Errorf("non-cancellable loader should not show cancel hint, got:\n%s", all)
		}
	})
}

// TestBorderedLoader_NextFrame verifies the spinner advances.
func TestBorderedLoader_NextFrame(t *testing.T) {
	bl := NewBorderedLoader("Thinking...", true)
	before := strings.Join(bl.Render(80), "\n")
	bl.NextFrame()
	after := strings.Join(bl.Render(80), "\n")
	// At least one line should change (the spinner frame).
	if before == after {
		t.Error("NextFrame should change the rendered output (spinner animation)")
	}
}
