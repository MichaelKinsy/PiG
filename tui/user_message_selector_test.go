package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestUserMessageSelectorRenderMatchesUpstreamLayout(t *testing.T) {
	sel := NewUserMessageSelector([]string{
		"reply with exactly: alpha",
		"reply with exactly: beta",
	})

	got := stripUserMessageSelectorANSILines(sel.Render(80))
	want := []string{
		"",
		" Fork from Message",
		" Select a user message to copy the active path up to that point into a new",
		" session",
		"",
		strings.Repeat("─", 80),
		"",
		"  reply with exactly: alpha",
		"  Message 1 of 2",
		"",
		"› reply with exactly: beta",
		"  Message 2 of 2",
		"",
		"",
		strings.Repeat("─", 80),
	}
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d\n%q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestUserMessageSelectorHandleInputMovesSelectionAndConfirms(t *testing.T) {
	sel := NewUserMessageSelector([]string{"alpha", "beta"})
	sel.HandleInput("\x1b[A") // Up
	got := stripUserMessageSelectorANSILines(sel.Render(40))
	if got[8] != "› alpha" {
		t.Fatalf("selected row after Up = %q, want %q", got[8], "› alpha")
	}
	if got[11] != "  beta" {
		t.Fatalf("second row after Up = %q, want %q", got[11], "  beta")
	}
	sel.HandleInput("\r")
	if !sel.Done() {
		t.Fatal("selector should be done after Enter")
	}
	if sel.Cancelled() {
		t.Fatal("selector should not be cancelled after Enter")
	}
	if sel.SelectedIndex() != 0 {
		t.Fatalf("SelectedIndex = %d, want 0", sel.SelectedIndex())
	}
}

func TestUserMessageSelectorEmptyState(t *testing.T) {
	sel := NewUserMessageSelector(nil)
	got := stripUserMessageSelectorANSILines(sel.Render(40))
	// Upstream: the list renders only the empty-state row, then Spacer(1) and
	// the bottom DynamicBorder.
	tail := []string{"  No user messages found", "", strings.Repeat("─", 40)}
	if len(got) < len(tail) || !slices.Equal(got[len(got)-len(tail):], tail) {
		t.Fatalf("empty state tail = %q, want %q", got, tail)
	}
}

func stripUserMessageSelectorANSILines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		// Upstream Text rows pad to the render width; compare the content.
		out[i] = strings.TrimRight(stripANSI(line), " ")
	}
	return out
}
