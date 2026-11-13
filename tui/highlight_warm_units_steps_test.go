//go:build !pig_strip_syntax_highlight

package tui

import (
	"math/rand"
	"slices"
	"testing"
)

// A text warmed in steps that end anywhere, inside a multi-byte character or a lone surrogate included, and interleaved with the
// highlights a frame asks for, draws the lines of a run over all of it: what the warm-ups keep of the text's units across steps is
// the units of a fresh conversion.
func TestWarmHighlightStepsAcrossCharacterBoundariesDrawTheLinesOfAWholeRun(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")

	rng := rand.New(rand.NewSource(11))
	for _, steps := range []struct {
		name  string
		lines int
		next  func() int
	}{
		{"one byte", 8, func() int { return 1 }},
		{"a few bytes", 20, func() int { return 1 + rng.Intn(7) }},
		{"a hundred bytes", 120, func() int { return 60 + rng.Intn(80) }},
		{"a few kilobytes", 400, func() int { return 1500 + rng.Intn(1500) }},
	} {
		code := warmedSource(steps.lines)
		t.Run(steps.name, func(t *testing.T) {
			hlMu.Lock()
			resetHighlightCacheLocked()
			hlMu.Unlock()
			held := 0
			for end := steps.next(); end < len(code); end += steps.next() {
				if WarmHighlight(code[:end], "typescript") {
					held++
				}
				if held%37 == 4 {
					// a frame draws the text so far, which continues from the state and leaves one without the units
					want := highlightCodeUncached(highlightRegistry(), code[:end], "typescript", ActiveTheme())
					if got := HighlightCode(code[:end], "typescript"); !slices.Equal(got, want) {
						t.Fatalf("the lines of the first %d bytes differ from a whole run", end)
					}
				}
			}
			if held == 0 {
				t.Fatal("no step left a state to resume from")
			}
			want := highlightCodeUncached(highlightRegistry(), code, "typescript", ActiveTheme())
			if got := HighlightCode(code, "typescript"); !slices.Equal(got, want) {
				t.Errorf("the lines of the whole text differ from a whole run (%d lines, want %d)", len(got), len(want))
			}
		})
	}
}
