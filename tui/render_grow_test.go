package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// scrollVT is a height-capped virtual terminal that models the one behavior the
// earlier in-viewport test was missing: an LF (\n) on the bottom row scrolls the
// screen up (top row to scrollback, blank row at the bottom), while cursor-down
// (CUD, \x1b[NB) clamps at the bottom without scrolling. This is exactly the
// distinction the differential renderer relies on, so it lets us reproduce a
// stale row left behind when content grows past the viewport.
type scrollVT struct {
	rows       [][]rune
	scrollback [][]rune
	row, col   int
	w, h       int
}

func newScrollVT(w, h int) *scrollVT {
	v := &scrollVT{w: w, h: h}
	for range h {
		v.rows = append(v.rows, blankVTRow(w))
	}
	return v
}

func blankVTRow(w int) []rune {
	r := make([]rune, w)
	for i := range r {
		r[i] = ' '
	}
	return r
}

func (v *scrollVT) scrollUp() {
	v.scrollback = append(v.scrollback, v.rows[0])
	v.rows = append(v.rows[1:], blankVTRow(v.w))
}

func (v *scrollVT) lf() {
	if v.row >= v.h-1 {
		v.scrollUp()
		v.row = v.h - 1
		return
	}
	v.row++
}

var vtCSIRe = regexp.MustCompile(`^\x1b\[(\??)([0-9;]*)([A-Za-z])`)

func (v *scrollVT) apply(s string) {
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			// OSC (\x1b]...): skip to BEL or ST.
			if i+1 < len(s) && s[i+1] == ']' {
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
			case priv == "?": // ?2026h/l, ?25h/l: no screen effect
			case fin == "A":
				v.row = max(0, v.row-n)
			case fin == "B":
				v.row = min(v.h-1, v.row+n) // CUD clamps, never scrolls
			case fin == "G":
				v.col = max(0, n-1)
			case fin == "K":
				v.rows[v.row] = blankVTRow(v.w)
			case fin == "J" && numStr == "2":
				for r := range v.rows {
					v.rows[r] = blankVTRow(v.w)
				}
			case fin == "J" && numStr == "3":
				v.scrollback = nil
			case fin == "H":
				v.row, v.col = 0, 0
			}
			i += len(m[0])
			continue
		}
		switch s[i] {
		case '\r':
			v.col = 0
		case '\n':
			v.lf()
		default:
			if v.col < v.w {
				v.rows[v.row][v.col] = rune(s[i])
				v.col++
			}
		}
		i++
	}
}

func (v *scrollVT) visibleLine(r int) string { return strings.TrimRight(string(v.rows[r]), " ") }

// TestTUIRender_ViewportScrollGrowNoDuplicate reproduces the reported editor
// duplication: with more content than fits the terminal (so the viewport has
// already scrolled), the editor input grows from one row to two. The redraw
// must not leave a stale copy of the pre-growth editor row on screen.
func TestTUIRender_ViewportScrollGrowNoDuplicate(t *testing.T) {
	const w, h = 24, 5
	var lines []string
	comp := renderFuncComponent(func(int) []string { return lines })

	var out bytes.Buffer
	ui := NewWithOutput(&out, w, h)
	ui.Add(comp)
	vt := newScrollVT(w, h)

	// Buffer taller than the 5-row viewport; editor is a single row "ED-loca"
	// with the cursor at its end (the marker drives end-of-frame cursor
	// positioning, which the next frame's diff move starts from).
	lines = []string{"L0", "L1", "L2", "L3", "ED-loca" + widthx.CursorMarker, "BOT", "F1", "F2"}
	out.Reset()
	ui.Render()
	vt.apply(out.String())

	// Editor wraps to two rows; cursor now at the end of the second row.
	lines = []string{"L0", "L1", "L2", "L3", "ED-local", "ED-rest" + widthx.CursorMarker, "BOT", "F1", "F2"}
	out.Reset()
	ui.Render()
	vt.apply(out.String())

	// The visible 5 rows must be the tail of the new buffer, in order.
	want := []string{"ED-local", "ED-rest", "BOT", "F1", "F2"}
	for r := range h {
		if got := vt.visibleLine(r); got != want[r] {
			t.Errorf("visible row %d = %q, want %q", r, got, want[r])
		}
	}
	for r := range h {
		if vt.visibleLine(r) == "ED-loca" {
			t.Fatalf("stale pre-growth editor row 'ED-loca' still visible at row %d", r)
		}
	}
}

func TestTUIRender_EmptyFrameClearsFirstRow(t *testing.T) {
	lines := []string{"one", "two", "three"}
	comp := renderFuncComponent(func(width int) []string { return lines })
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 4)
	ui.Add(comp)
	vt := newScrollVT(20, 4)

	ui.Render()
	vt.apply(out.String())

	out.Reset()
	lines = nil
	ui.Render()
	vt.apply(out.String())
	for row := range 4 {
		if got := vt.visibleLine(row); got != "" {
			t.Fatalf("row %d remained after empty frame: %q (output %q)", row, got, out.String())
		}
	}
}

func TestTUIRender_StreamingToolCollapseDoesNotRetainHiddenRowsInScrollback(t *testing.T) {
	for _, toolName := range []string{"bash", "extension_tool"} {
		t.Run(toolName, func(t *testing.T) {
			const width, height = 48, 10
			var out bytes.Buffer
			ui := NewWithOutput(&out, width, height)
			tool := NewToolExecutionComponent(toolName, "long-running call")
			if toolName == "extension_tool" {
				tool.SetStructuredArgs(json.RawMessage(`{}`))
			}
			ui.Add(renderFuncComponent(func(renderWidth int) []string {
				lines := tool.Render(renderWidth)
				for i := range lines {
					lines[i] = stripANSI(lines[i])
				}
				return lines
			}))
			vt := newScrollVT(width, height)

			ui.Render()
			vt.apply(out.String())

			var output strings.Builder
			output.WriteString("STREAM-FIRST\n")
			for i := range 98 {
				fmt.Fprintf(&output, "streamed row %03d\n", i+1)
			}
			output.WriteString("STREAM-LAST")

			out.Reset()
			tool.SetStreaming(output.String())
			ui.Render()
			vt.apply(out.String())

			out.Reset()
			tool.SetResult(output.String(), false, 0)
			ui.Add(NewText("LATER-TOOL-BLOCK"))
			ui.Render()
			vt.apply(out.String())

			var transcript strings.Builder
			for _, row := range vt.scrollback {
				transcript.WriteString(string(row))
				transcript.WriteByte('\n')
			}
			for _, row := range vt.rows {
				transcript.WriteString(string(row))
				transcript.WriteByte('\n')
			}
			rendered := transcript.String()
			if strings.Contains(rendered, "STREAM-LAST") {
				t.Fatalf("hidden streaming output remained in terminal scrollback:\n%s", rendered)
			}
			firstOutput := strings.LastIndex(rendered, "STREAM-FIRST")
			laterBlock := strings.LastIndex(rendered, "LATER-TOOL-BLOCK")
			if firstOutput < 0 || laterBlock < 0 || firstOutput >= laterBlock {
				t.Fatalf("streaming preview and later block are out of order:\n%s", rendered)
			}
		})
	}
}
