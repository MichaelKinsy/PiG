package tools

import "testing"

// TestGrepLineWindowDiscardShiftsInPlace pins TO-T17: dropping the lines no later match needs reuses the window's slice instead of
// cloning the kept lines per match.
func TestGrepLineWindowDiscardShiftsInPlace(t *testing.T) {
	w := &grepLineWindow{}
	step := func() {
		w.lines = append(w.lines, "a", "b", "c")
		w.next += 3
		w.discardBefore(float64(w.next - 1))
	}
	step()
	if got := w.line(w.next - 1); got != "b" || len(w.lines) != 2 {
		t.Fatalf("window holds %q, line %d = %q, want the last two lines", w.lines, w.next-1, got)
	}
	if allocs := testing.AllocsPerRun(100, step); allocs != 0 {
		t.Fatalf("discarding lines allocated %.1f times per match, want 0", allocs)
	}
}
