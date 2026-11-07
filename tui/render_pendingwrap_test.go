package tui

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// wrapVT extends the scroll-aware VT model with DECAWM "pending wrap"
// semantics, which the plain scrollVT omits. Real terminals do NOT advance
// the cursor past the last column: writing a glyph into the rightmost cell
// leaves the cursor on that cell with a "pending wrap" latch set. The NEXT
// printable glyph first performs CR+LF (scrolling at the bottom), then prints
// at column 0. Any explicit cursor move (CUU/CUD/CHA/CUP), CR, or LF clears
// the latch. Stale-row / phantom-cursor artifacts in the live TUI live in this
// exact corner, so a VT that ignores it can never reproduce them.
type wrapVT struct {
	rows        [][]rune
	scrollback  [][]rune
	row, col    int
	w, h        int
	pendingWrap bool
}

func newWrapVT(w, h int) *wrapVT {
	v := &wrapVT{w: w, h: h}
	for range h {
		v.rows = append(v.rows, blankVTRow(w))
	}
	return v
}

func (v *wrapVT) scrollUp() {
	v.scrollback = append(v.scrollback, v.rows[0])
	v.rows = append(v.rows[1:], blankVTRow(v.w))
}

func (v *wrapVT) lf() {
	v.pendingWrap = false
	if v.row >= v.h-1 {
		v.scrollUp()
		v.row = v.h - 1
		return
	}
	v.row++
}

func (v *wrapVT) putRune(r rune) {
	if v.pendingWrap {
		// DECAWM: deferred wrap fires on the next printable glyph.
		v.col = 0
		v.lf()
	}
	if v.col < v.w {
		v.rows[v.row][v.col] = r
	}
	if v.col == v.w-1 {
		v.pendingWrap = true // latch; cursor stays on the last cell
	} else if v.col < v.w {
		v.col++
	}
}

func (v *wrapVT) apply(s string) {
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			if i+1 < len(s) && s[i+1] == ']' { // OSC: skip to BEL/ST
				j := i + 2
				for j < len(s) && s[j] != '\x07' {
					if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
						j++
						break
					}
					j++
				}
				i = j + 1
				continue
			}
			m := vtCSIRe.FindStringSubmatch(s[i:])
			if m == nil {
				i++
				continue
			}
			priv, numStr, fin := m[1], m[2], m[3]
			n := 1
			if numStr != "" {
				n, _ = strconv.Atoi(numStr)
			}
			switch {
			case priv == "?": // ?2026h/l, ?25h/l: no screen/position effect
			case fin == "A":
				v.row = max(0, v.row-n)
				v.pendingWrap = false
			case fin == "B":
				v.row = min(v.h-1, v.row+n)
				v.pendingWrap = false
			case fin == "G":
				v.col = max(0, n-1)
				v.pendingWrap = false
			case fin == "K":
				v.rows[v.row] = blankVTRow(v.w)
			case fin == "J":
				if numStr == "2" {
					for r := range v.rows {
						v.rows[r] = blankVTRow(v.w)
					}
				}
			case fin == "H":
				v.row, v.col, v.pendingWrap = 0, 0, false
			}
			i += len(m[0])
			continue
		}
		switch s[i] {
		case '\r':
			v.col = 0
			v.pendingWrap = false
		case '\n':
			v.lf()
		default:
			v.putRune(rune(s[i]))
		}
		i++
	}
}

func (v *wrapVT) visibleLine(r int) string { return strings.TrimRight(string(v.rows[r]), " ") }

// fullWidthEditorRow mimics what Editor.buildVisualLines emits for a chunk that
// exactly fills the width with the cursor on its last cell: `width` visible
// columns, last cell inverse-video, plus the APC cursor marker. This is the
// shape that puts the terminal into pending-wrap.
func fullWidthEditorRow(width int) string {
	body := strings.Repeat("a", width-1)
	return body + widthx.CursorMarker + "\033[7m" + "a" + "\033[0m"
}

// TestTUIRender_FullWidthCursorGrowNoGhost drives the reported sequence: an
// editor row that exactly fills the terminal width (cursor latched in pending
// wrap), then the user types one more char so the editor wraps to two rows.
// Under DECAWM the post-growth redraw must not leave a stale copy of the
// full-width row, and the visible screen must equal the tail of the new buffer.
func TestTUIRender_FullWidthCursorGrowNoGhost(t *testing.T) {
	const w, h = 12, 5
	var lines []string
	comp := renderFuncComponent(func(int) []string { return lines })

	var out bytes.Buffer
	ui := NewWithOutput(&out, w, h)
	ui.Add(comp)
	vt := newWrapVT(w, h)

	// Frame 1: buffer taller than the viewport; the editor occupies a single
	// full-width row whose last cell is the cursor.
	lines = []string{"L0", "L1", "L2", fullWidthEditorRow(w), "FOOT"}
	out.Reset()
	ui.Render()
	vt.apply(out.String())

	// Frame 2: the editor wraps: row 1 is the full-width body (no cursor),
	// row 2 holds the overflow char with the cursor.
	wrapped0 := strings.Repeat("a", w)
	wrapped1 := "b" + widthx.CursorMarker + "\033[7m" + " " + "\033[0m"
	lines = []string{"L0", "L1", "L2", wrapped0, wrapped1, "FOOT"}
	out.Reset()
	ui.Render()
	vt.apply(out.String())

	want := []string{"L1", "L2", strings.Repeat("a", w), "b", "FOOT"}
	for r := range h {
		if got := vt.visibleLine(r); got != want[r] {
			t.Errorf("visible row %d = %q, want %q", r, got, want[r])
		}
	}
}
