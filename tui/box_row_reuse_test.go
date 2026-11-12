package tui

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// rowsComponent is a child that draws the rows it holds.
type rowsComponent struct{ rows []string }

func (c *rowsComponent) Render(int) []string { return c.rows }
func (c *rowsComponent) Invalidate()         {}

// A Box that is drawn after Clear and AddChild, as the card of a streaming tool call is, gives the rows a fresh Box gives: the
// rows it keeps from its last render depend on the width, the padding, the background and its sample, and on the child row at
// the same index being the same.
func TestBoxRenderAfterClearMatchesAFreshBox(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 13))
	rows := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = strings.Repeat(string(rune('a'+rng.IntN(3))), 1+rng.IntN(30))
			if rng.IntN(4) == 0 {
				out[i] = "\x1b[31m" + out[i] + "\x1b[0m漢字"
			}
		}
		return out
	}
	tint := "\x1b[44m"
	bgFn := func(s string) string { return tint + s + "\x1b[49m" }
	for trial := range 300 {
		live := NewPaddedBox(rng.IntN(3), rng.IntN(3)-1, bgFn)
		if rng.IntN(5) == 0 {
			live.BgFn = nil
		}
		current := rows(1 + rng.IntN(12))
		width := 6 + rng.IntN(60)
		for step := range 14 {
			live.Clear()
			live.AddChild(&rowsComponent{rows: current[:len(current)/2]})
			live.AddChild(&rowsComponent{rows: current[len(current)/2:]})
			fresh := &Box{PaddingX: live.PaddingX, PaddingY: live.PaddingY, BgFn: live.BgFn}
			fresh.AddChild(&rowsComponent{rows: current})
			want := fresh.Render(width)
			if got := live.Render(width); !slices.Equal(got, want) {
				t.Fatalf("trial %d step %d width %d padding %d,%d:\n got %q\nwant %q", trial, step, width, live.PaddingX, live.PaddingY, got, want)
			}
			switch rng.IntN(9) {
			case 0, 1, 2: // the last row changes, then a row is added
				current = slices.Clone(current)
				current[len(current)-1] = rows(1)[0]
				current = append(current, rows(rng.IntN(2))...)
			case 3:
				current = slices.Clone(current)
				current[rng.IntN(len(current))] = rows(1)[0]
			case 4:
				width = 3 + rng.IntN(70)
			case 5:
				live.PaddingX, live.PaddingY = rng.IntN(3), rng.IntN(3)-1
			case 6:
				tint = []string{"\x1b[44m", "\x1b[42m", "\x1b[41m"}[rng.IntN(3)]
			case 7:
				if live.BgFn == nil {
					live.BgFn = bgFn
				} else {
					live.BgFn = nil
				}
			case 8:
				live.Invalidate()
			}
		}
	}
}

// A card draws again after each streamed update with its rows replaced through Clear and AddChild: the background function runs for
// the rows that changed and the sample, not for every row.
func TestBoxRenderAfterClearAppliesTheBackgroundToChangedRowsOnly(t *testing.T) {
	calls := 0
	box := NewPaddedBox(1, 1, func(s string) string { calls++; return s })
	rows := make([]string, 12)
	for i := range rows {
		rows[i] = strings.Repeat("x", i+1)
	}
	draw := func() {
		box.Clear()
		box.AddChild(&rowsComponent{rows: rows})
		box.Render(80)
	}
	draw()
	calls = 0
	rows = slices.Clone(rows)
	rows[len(rows)-1] = "changed"
	draw()
	// the sample, and the one changed row; the padding rows and the 11 other rows are served from the last render
	if calls != 2 {
		t.Errorf("the background ran %d times for one changed row of 12", calls)
	}
}
