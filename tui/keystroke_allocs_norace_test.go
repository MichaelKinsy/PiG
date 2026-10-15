//go:build !race

package tui

import (
	"math"
	"slices"
	"testing"
)

// The per-keystroke gate: the allocations of matching one keystroke against every default binding may only go down. Before key identifiers and keystrokes were parsed once the figures were 162 (printable), 160 (enter), 193 (modified arrow) and 316 (Kitty CSI-u).
func TestKeystrokeBindingMatchAllocationCeiling(t *testing.T) {
	manager := NewTUIKeybindingsManager(nil)
	var actions []string
	for action := range TUIKeybindings {
		actions = append(actions, action)
	}
	slices.Sort(actions)
	for _, tc := range []struct {
		name    string
		inputs  []string
		ceiling float64
	}{
		{"printable", []string{"a", "b", "c", "d", "e", "f", "g", "h"}, 2},
		{"enter", []string{"\r", "\n", "\x1bOM"}, 2},
		{"modified arrow", []string{"\x1b[1;5C", "\x1b[1;5D", "\x1b[1;3A", "\x1b[1;5B"}, 3},
		{"kitty CSI-u", []string{"\x1b[97;5u", "\x1b[98;5u", "\x1b[99;5u", "\x1b[100;5u"}, 5},
	} {
		next := 0
		// Allocations of goroutines other tests left running only add to a run, so the least of several runs is the keystroke's own count.
		got := math.MaxFloat64
		for range 7 {
			got = min(got, testing.AllocsPerRun(200, func() {
				data := tc.inputs[next%len(tc.inputs)]
				next++
				for _, action := range actions {
					manager.Matches(data, action)
				}
			}))
		}
		if got > tc.ceiling {
			t.Errorf("%s: a keystroke allocates %v times, ceiling %v", tc.name, got, tc.ceiling)
		}
	}
}
