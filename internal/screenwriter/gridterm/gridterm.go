// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

// Package gridterm is a terminal reduced to a grid of cells, the pen, the cursor and the scroll region: what a stream of the alternate
// screen's bytes leaves on it, whichever way the stream writes its rows (whole, or in part). It is the in-process screen oracle of the
// screen writer (internal/screenwriter, D624) and shares no code with it: it reads a stream byte by byte, and a Model is its own idea of
// what a cluster is and how wide, two of which disagree with the renderers, and of two things terminal engines do differently.
// Only tests import it.
package gridterm

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Model is one idea of a terminal.
type Model struct {
	Name string
	// Segment is the first cluster of s and the columns it takes.
	Segment func(s string) (cluster string, width int)
	// NoHome is a terminal that leaves the cursor where it is when the scroll region is set (pyte); PastEdge one that keeps a cursor
	// position past the last column as it was addressed (wezterm-term).
	NoHome, PastEdge bool
}

// Models are the renderers' own widths, UAX 29 clusters, and a terminal without clusters that gives every code point its wcwidth, with
// ambiguous East Asian characters narrow and wide.
var Models = []Model{
	{Name: "repo", Segment: segmentRepo},
	{Name: "uax29", Segment: segmentUAX, PastEdge: true},
	{Name: "wcwidth", Segment: segmentRunes(false), NoHome: true},
	{Name: "wcwidth-ambiguous-wide", Segment: segmentRunes(true), NoHome: true, PastEdge: true},
}

type pen struct {
	fg, bg string
	attrs  uint16
}

type cell struct {
	text  string
	pen   pen
	link  string
	state uint8 // 0 erased, 1 written, 2 the right half of the wide cluster to its left
}

// Terminal is the screen a stream leaves.
type Terminal struct {
	cols, rows  int
	grid        [][]cell
	row, col    int
	pen         pen
	link        string
	visible     bool
	autowrap    bool
	top, bottom int
	model       Model
	last        *cell
	unknown     map[string]int
}

// New returns a terminal of cols by rows cells with an empty screen.
func New(cols, rows int, model Model) *Terminal {
	v := &Terminal{model: model, visible: true, autowrap: true, unknown: map[string]int{}}
	v.Resize(cols, rows)
	return v
}

// Unknown counts what the terminal was sent and has no rule for, by kind.
func (v *Terminal) Unknown() map[string]int { return v.unknown }

// Row is the text row shows, erased cells as spaces, without the erased cells at its end.
func (v *Terminal) Row(row int) string {
	var b strings.Builder
	for _, c := range v.grid[row] {
		switch c.state {
		case 0:
			b.WriteByte(' ')
		case 1:
			b.WriteString(c.text)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func (v *Terminal) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	grid := make([][]cell, rows)
	for r := range grid {
		grid[r] = make([]cell, cols)
		if r < len(v.grid) {
			copy(grid[r], v.grid[r])
		}
	}
	v.grid, v.cols, v.rows = grid, cols, rows
	v.top, v.bottom = 0, rows-1
	v.row, v.col = min(v.row, rows-1), min(v.col, cols-1)
	v.last = nil
}

func segmentUAX(s string) (string, int) {
	cluster, _, width, _ := uniseg.FirstGraphemeClusterInString(s, -1)
	return cluster, width
}

func segmentRepo(s string) (string, int) {
	cluster, _ := widthx.FirstGrapheme(s)
	return cluster, widthx.GraphemeWidth(cluster)
}

// segmentRunes is a terminal without grapheme clusters: every code point is a cell of its own wcwidth, ambiguous ones wide when wide is set.
func segmentRunes(wide bool) func(string) (string, int) {
	cond := runewidth.NewCondition()
	cond.EastAsianWidth = wide
	return func(s string) (string, int) {
		r, size := utf8.DecodeRuneInString(s)
		return s[:size], cond.RuneWidth(r)
	}
}

func (v *Terminal) unwide(r, c int) {
	at := &v.grid[r][c]
	switch {
	case at.state == 2 && c > 0:
		v.grid[r][c-1] = cell{text: " ", pen: at.pen, state: 1}
	case at.state == 1 && c+1 < v.cols && v.grid[r][c+1].state == 2:
		v.grid[r][c+1] = cell{text: " ", pen: at.pen, state: 1}
	}
}

func (v *Terminal) print(cluster string, width int) {
	if width <= 0 {
		if v.last != nil {
			v.last.text += cluster
		}
		return
	}
	if v.col+width > v.cols {
		if v.autowrap {
			v.col = 0
			v.lineFeed()
		} else {
			v.col = v.cols - width
		}
		if v.col < 0 {
			v.col = 0
			return
		}
	}
	for k := range width {
		v.unwide(v.row, v.col+k)
	}
	v.grid[v.row][v.col] = cell{text: cluster, pen: v.pen, link: v.link, state: 1}
	v.last = &v.grid[v.row][v.col]
	for k := 1; k < width; k++ {
		v.grid[v.row][v.col+k] = cell{pen: v.pen, link: v.link, state: 2}
	}
	v.col += width
	if v.col >= v.cols && !v.autowrap {
		v.col = v.cols - 1
	}
}

func (v *Terminal) erase(r, from, to int) {
	if from < to && from < v.cols {
		v.unwide(r, from)
	}
	for c := from; c < to && c < v.cols; c++ {
		v.grid[r][c] = cell{pen: pen{bg: v.pen.bg}}
	}
}

func (v *Terminal) lineFeed() {
	v.last = nil
	switch {
	case v.row == v.bottom:
		first := v.grid[v.top]
		copy(v.grid[v.top:v.bottom], v.grid[v.top+1:v.bottom+1])
		v.grid[v.bottom] = first
		v.erase(v.bottom, 0, v.cols)
	case v.row < v.rows-1:
		v.row++
	}
}

func (v *Terminal) reverseIndex() {
	v.last = nil
	switch {
	case v.row == v.top:
		last := v.grid[v.bottom]
		copy(v.grid[v.top+1:v.bottom+1], v.grid[v.top:v.bottom])
		v.grid[v.top] = last
		v.erase(v.top, 0, v.cols)
	case v.row > 0:
		v.row--
	}
}

func params(s string) []int {
	if s == "" {
		return nil
	}
	var out []int
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ':' }) {
		n, _ := strconv.Atoi(part)
		out = append(out, n)
	}
	if strings.HasPrefix(s, ";") {
		out = append([]int{0}, out...)
	}
	return out
}

func param(p []int, i, def int) int {
	if i < len(p) && p[i] > 0 {
		return p[i]
	}
	return def
}

func (v *Terminal) sgr(p []int) {
	if len(p) == 0 {
		p = []int{0}
	}
	for i := 0; i < len(p); i++ {
		n := p[i]
		switch {
		case n == 0:
			v.pen = pen{}
		case n >= 1 && n <= 9:
			v.pen.attrs |= 1 << n
		case n == 22:
			v.pen.attrs &^= 1<<1 | 1<<2
		case n >= 23 && n <= 29:
			v.pen.attrs &^= 1 << (n - 20)
		case n == 53:
			v.pen.attrs |= 1 << 10
		case n == 55:
			v.pen.attrs &^= 1 << 10
		case n >= 30 && n <= 37, n >= 90 && n <= 97:
			v.pen.fg = strconv.Itoa(n)
		case n == 39:
			v.pen.fg = ""
		case n >= 40 && n <= 47, n >= 100 && n <= 107:
			v.pen.bg = strconv.Itoa(n)
		case n == 49:
			v.pen.bg = ""
		case n == 38 || n == 48:
			color := ""
			switch {
			case i+2 < len(p) && p[i+1] == 5:
				color = fmt.Sprint("i", p[i+2])
				i += 2
			case i+4 < len(p) && p[i+1] == 2:
				color = fmt.Sprint("r", p[i+2], ",", p[i+3], ",", p[i+4])
				i += 4
			default:
				v.unknown[fmt.Sprint("SGR ", p)]++
				return
			}
			if n == 38 {
				v.pen.fg = color
			} else {
				v.pen.bg = color
			}
		default:
			v.unknown[fmt.Sprint("SGR ", n)]++
		}
	}
}

func (v *Terminal) csi(private byte, list string, final byte) {
	p := params(list)
	v.last = nil
	if private != 0 {
		if private == '?' && (final == 'h' || final == 'l') {
			for _, mode := range p {
				switch mode {
				case 25:
					v.visible = final == 'h'
				case 7:
					v.autowrap = final == 'h'
				case 1049:
					if final == 'h' {
						for r := range v.grid {
							v.grid[r] = make([]cell, v.cols)
						}
					}
				}
			}
		}
		return
	}
	switch final {
	case 'H', 'f':
		v.row, v.col = min(param(p, 0, 1), v.rows)-1, min(param(p, 1, 1), v.cols)-1
		if v.model.PastEdge && param(p, 1, 1) > v.cols {
			v.col = v.cols
		}
	case 'A':
		v.row = max(v.row-param(p, 0, 1), 0)
	case 'B':
		v.row = min(v.row+param(p, 0, 1), v.rows-1)
	case 'C':
		v.col = min(v.col+param(p, 0, 1), v.cols-1)
	case 'D':
		v.col = max(v.col-param(p, 0, 1), 0)
	case 'G':
		v.col = min(param(p, 0, 1), v.cols) - 1
	case 'K':
		switch param(p, 0, 0) {
		case 0:
			v.erase(v.row, v.col, v.cols)
		case 1:
			v.erase(v.row, 0, v.col+1)
		case 2:
			v.erase(v.row, 0, v.cols)
		}
	case 'J':
		switch param(p, 0, 0) {
		case 0:
			v.erase(v.row, v.col, v.cols)
			for r := v.row + 1; r < v.rows; r++ {
				v.erase(r, 0, v.cols)
			}
		case 1:
			for r := range v.row {
				v.erase(r, 0, v.cols)
			}
			v.erase(v.row, 0, v.col+1)
		case 2:
			for r := range v.rows {
				v.erase(r, 0, v.cols)
			}
		}
	case 'm':
		v.sgr(p)
	case 'r':
		v.top, v.bottom = param(p, 0, 1)-1, min(param(p, 1, v.rows), v.rows)-1
		if v.top >= v.bottom {
			v.top, v.bottom = 0, v.rows-1
		}
		if !v.model.NoHome {
			v.row, v.col = 0, 0
		}
	default:
		v.unknown["CSI "+string(final)]++
	}
}

// Write is the terminal receiving s. The cells keep a copy: s may be a buffer its writer uses again.
func (v *Terminal) Write(s string) {
	s = strings.Clone(s)
	for i := 0; i < len(s); {
		b := s[i]
		switch {
		case b == 0x1b && i+1 < len(s):
			switch s[i+1] {
			case '[':
				j := i + 2
				var private byte
				if j < len(s) && s[j] >= '<' && s[j] <= '?' {
					private = s[j]
					j++
				}
				start := j
				for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
					j++
				}
				params := s[start:j]
				for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
					j++
					private = '!'
				}
				if j < len(s) {
					v.csi(private, params, s[j])
					j++
				}
				i = j
			case ']':
				j := i + 2
				for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				if body := s[i+2 : j]; strings.HasPrefix(body, "8;") {
					if k := strings.IndexByte(body[2:], ';'); k >= 0 {
						v.link = body[2:]
						if body[2+k+1:] == "" {
							v.link = ""
						}
					}
				}
				if j < len(s) && s[j] == 0x1b {
					j++
				}
				i = j + 1
			case 'P', '_', '^', 'X':
				j := i + 2
				for j+1 < len(s) && !(s[j] == 0x1b && s[j+1] == '\\') {
					j++
				}
				v.unknown["string "+s[i+1:i+2]]++
				i = j + 2
			case 'M':
				v.reverseIndex()
				i += 2
			case '=', '>':
				i += 2
			default:
				v.unknown["ESC "+s[i+1:i+2]]++
				i += 2
			}
		case b == '\r':
			v.col, v.last = 0, nil
			i++
		case b == '\n':
			v.lineFeed()
			i++
		case b == '\b':
			v.col, v.last = max(v.col-1, 0), nil
			i++
		case b < 0x20 || b == 0x7f:
			if b != 0x07 {
				v.unknown[fmt.Sprintf("C0 %#x", b)]++
			}
			i++
		default:
			j := i
			for j < len(s) && s[j] >= 0x20 && s[j] != 0x7f {
				j++
			}
			for run := s[i:j]; run != ""; {
				cluster, width := v.model.Segment(run)
				if cluster == "" {
					break
				}
				v.print(cluster, width)
				run = run[len(cluster):]
			}
			i = j
		}
	}
}

// Differ is how the screens of a and b differ, "" when they show the same cells, pen, cursor and scroll region.
func Differ(a, b *Terminal) string {
	for r := range a.grid {
		for c := range a.grid[r] {
			if a.grid[r][c] != b.grid[r][c] {
				return fmt.Sprintf("row %d column %d: %+v, not %+v", r+1, c+1, b.grid[r][c], a.grid[r][c])
			}
		}
	}
	switch {
	case a.row != b.row || a.col != b.col:
		return fmt.Sprintf("cursor at %d;%d, not %d;%d", b.row+1, b.col+1, a.row+1, a.col+1)
	case a.visible != b.visible:
		return fmt.Sprintf("cursor shown %v, not %v", b.visible, a.visible)
	case a.pen != b.pen:
		return fmt.Sprintf("pen %+v, not %+v", b.pen, a.pen)
	case a.link != b.link:
		return fmt.Sprintf("hyperlink %q, not %q", b.link, a.link)
	case a.top != b.top || a.bottom != b.bottom:
		return fmt.Sprintf("scroll region %d-%d, not %d-%d", b.top+1, b.bottom+1, a.top+1, a.bottom+1)
	}
	return ""
}
