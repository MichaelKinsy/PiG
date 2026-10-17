package codingagent

import (
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// armin.ts:62-80 the constructor picks EFFECTS[floor(Math.random() * EFFECTS.length)], builds the final grid, creates the empty grid (every cell " "), runs
// initEffect() and leaves cachedVersion -1, so the first render builds the lines whatever the width.
// mutation-checked: a fixed effect, a missing initEffect, a zero-value current grid and a cachedVersion of 0 fail.
func TestNewArminComponentPicksEachEffectAndStartsFromAnEmptyGrid(t *testing.T) {
	for i, effect := range arminEffects {
		pick := (float64(i) + 0.5) / float64(len(arminEffects))
		a := newArminComponent(func() float64 { return pick })
		if _, ok := any(a).(*ArminComponent); !ok {
			t.Fatalf("newArminComponent returned %T", a)
		}
		if a.effect != effect {
			t.Fatalf("random %.3f picked %q, want EFFECTS[%d] = %q", pick, a.effect, i, effect)
		}
		for row := range a.currentGrid {
			for x, cell := range a.currentGrid[row] {
				if cell == 0 {
					t.Fatalf("%s: current grid cell (%d,%d) is the zero value, want the empty grid's space or an effect's character", effect, row, x)
				}
			}
		}
		if a.cachedVersion != -1 {
			t.Fatalf("%s: cachedVersion = %d, want -1", effect, a.cachedVersion)
		}
		if lines := a.Render(0); len(lines) != arminDisplayHeight+1 {
			t.Fatalf("%s: first Render(0) gave %d lines, want the grid rows and the caption (%d)", effect, len(lines), arminDisplayHeight+1)
		}
	}
}

// armin.ts:67 picks the effect with Math.random(): the constructor draws it from a random source, so repeated components do not all show one effect.
// mutation-checked: a constant random source fails.
func TestNewArminComponentDrawsItsEffectAtRandom(t *testing.T) {
	ui := tui.NewWithOutput(io.Discard, 40, 10)
	seen := map[string]bool{}
	for range 300 {
		a := NewArminComponent(ui)
		seen[a.effect] = true
		a.Dispose()
	}
	if len(seen) != len(arminEffects) {
		t.Fatalf("300 components showed %d of the %d effects: %v", len(seen), len(arminEffects), seen)
	}
}
