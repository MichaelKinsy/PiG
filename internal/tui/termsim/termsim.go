// Package termsim is a headless ANSI terminal simulator for byte-level parity
// testing. It accepts an ANSI byte stream (CSI/SGR/OSC/cursor moves) and
// reconstructs the final terminal state as a 2D cell grid.
//
// Mirrors only the subset of escape sequences the pi-tui renderer actually
// emits. New sequences MUST be added as upstream pi-tui starts emitting them.
//
// Reference: .upstream/current/packages/tui/src/tui.ts (renderer) for the
// emitter side; this is the consumer side.
package termsim

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Cell is one terminal column. Wide chars occupy two cells; the second has
// Continuation=true and Rune=0. Empty cells have Rune=' '.
type Cell struct {
	Rune         rune
	Continuation bool
	Style        Style
	Link         string // OSC 8 hyperlink target
}

// Style is the SGR attributes applied to a cell.
type Style struct {
	FG, BG    Color
	Bold      bool
	Italic    bool
	Underline bool
	Inverse   bool
	Strike    bool
	Dim       bool
}

// Color represents a foreground or background color. Mode discriminates.
type Color struct {
	Mode    ColorMode
	N       uint8 // for 16/256
	R, G, B uint8 // for truecolor
}

type ColorMode uint8

const (
	ColorDefault   ColorMode = 0
	Color16        ColorMode = 1
	Color256       ColorMode = 2
	ColorTrueColor ColorMode = 3
)

// Grid is the simulated terminal state.
type Grid struct {
	Rows, Cols int
	cells      [][]Cell
	curRow     int
	curCol     int
	style      Style
	link       string
	scrollback []string // rendered lines that scrolled past the top
	syncDepth  int      // mode 2026 nesting count (informational)
}

// New creates a fresh Grid of the given dimensions.
func New(rows, cols int) *Grid {
	g := &Grid{Rows: rows, Cols: cols}
	g.reset()
	return g
}

func (g *Grid) reset() {
	g.cells = make([][]Cell, g.Rows)
	for r := range g.cells {
		g.cells[r] = makeRow(g.Cols)
	}
	g.curRow, g.curCol = 0, 0
	g.style = Style{}
	g.link = ""
}

func makeRow(cols int) []Cell {
	row := make([]Cell, cols)
	for c := range row {
		row[c].Rune = ' '
	}
	return row
}

// Write feeds bytes into the simulator.
func (g *Grid) Write(b []byte) {
	i := 0
	for i < len(b) {
		c := b[i]
		switch {
		case c == 0x1B && i+1 < len(b):
			i += g.handleEscape(b[i:])
		case c == '\r':
			g.curCol = 0
			i++
		case c == '\n':
			g.lineFeed()
			i++
		case c == '\b':
			if g.curCol > 0 {
				g.curCol--
			}
			i++
		case c == '\t':
			g.curCol = (g.curCol/8 + 1) * 8
			if g.curCol >= g.Cols {
				g.curCol = g.Cols - 1
			}
			i++
		case c < 0x20:
			i++ // drop other control bytes
		default:
			r, n := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && n == 1 {
				i++
				continue
			}
			g.putRune(r)
			i += n
		}
	}
}

// WriteString is the string variant of Write.
func (g *Grid) WriteString(s string) { g.Write([]byte(s)) }

func (g *Grid) putRune(r rune) {
	w := runewidth.RuneWidth(r)
	if w == 0 {
		// Combining mark: append to previous cell (best-effort, parity-good).
		if g.curCol > 0 && g.curRow < g.Rows {
			prev := &g.cells[g.curRow][g.curCol-1]
			prev.Rune = combine(prev.Rune, r)
		}
		return
	}
	if g.curCol+w > g.Cols {
		// Auto-wrap to next line.
		g.curCol = 0
		g.lineFeed()
	}
	if g.curRow >= g.Rows {
		return
	}
	row := g.cells[g.curRow]
	row[g.curCol] = Cell{Rune: r, Style: g.style, Link: g.link}
	if w == 2 && g.curCol+1 < g.Cols {
		row[g.curCol+1] = Cell{Continuation: true, Style: g.style, Link: g.link}
	}
	g.curCol += w
}

// combine attaches a combining mark to a base rune; we keep only the base
// for parity purposes (matches upstream's per-cell rendering).
func combine(base, _ rune) rune { return base }

func (g *Grid) lineFeed() {
	g.curRow++
	if g.curRow >= g.Rows {
		// Scroll up: top row becomes scrollback.
		g.scrollback = append(g.scrollback, rowToString(g.cells[0]))
		copy(g.cells, g.cells[1:])
		g.cells[g.Rows-1] = makeRow(g.Cols)
		g.curRow = g.Rows - 1
	}
}

// handleEscape consumes one ANSI sequence and returns the number of bytes
// consumed (including the leading 0x1B).
func (g *Grid) handleEscape(b []byte) int {
	if len(b) < 2 {
		return 1
	}
	switch b[1] {
	case '[':
		return g.handleCSI(b)
	case ']':
		return g.handleOSC(b)
	case '_':
		return g.handleAPC(b)
	case '7':
		return 2 // DEC save cursor: ignored for parity
	case '8':
		return 2 // DEC restore cursor: ignored for parity
	case 'M':
		// reverse line feed
		if g.curRow > 0 {
			g.curRow--
		}
		return 2
	}
	return 2
}

// handleCSI parses ESC [ <params> <final>.
func (g *Grid) handleCSI(b []byte) int {
	// Find final byte (0x40-0x7E)
	end := 2
	for end < len(b) {
		c := b[end]
		if c >= 0x40 && c <= 0x7E {
			end++
			break
		}
		end++
	}
	if end > len(b) {
		return len(b)
	}
	final := b[end-1]
	params := string(b[2 : end-1])
	priv := strings.HasPrefix(params, "?")
	if priv {
		params = params[1:]
	}
	switch final {
	case 'A':
		g.curRow = max0(g.curRow - parseN(params, 1))
	case 'B':
		g.curRow = minI(g.curRow+parseN(params, 1), g.Rows-1)
	case 'C':
		g.curCol = minI(g.curCol+parseN(params, 1), g.Cols-1)
	case 'D':
		g.curCol = max0(g.curCol - parseN(params, 1))
	case 'E':
		g.curRow = minI(g.curRow+parseN(params, 1), g.Rows-1)
		g.curCol = 0
	case 'F':
		g.curRow = max0(g.curRow - parseN(params, 1))
		g.curCol = 0
	case 'G':
		g.curCol = max0(parseN(params, 1) - 1)
	case 'H', 'f':
		row, col := parseTwo(params, 1, 1)
		g.curRow = clampI(row-1, 0, g.Rows-1)
		g.curCol = clampI(col-1, 0, g.Cols-1)
	case 'J':
		g.eraseDisplay(parseN(params, 0))
	case 'K':
		g.eraseLine(parseN(params, 0))
	case 'm':
		g.applySGR(params)
	case 'h', 'l':
		// Private modes: track sync output (2026) nesting for diagnostics.
		if priv && params == "2026" {
			if final == 'h' {
				g.syncDepth++
			} else if g.syncDepth > 0 {
				g.syncDepth--
			}
		}
	}
	return end
}

// handleOSC parses ESC ] <params> (ST = ESC \ or BEL = 0x07).
func (g *Grid) handleOSC(b []byte) int {
	end := 2
	for end < len(b) {
		if b[end] == 0x07 {
			body := string(b[2:end])
			g.applyOSC(body)
			return end + 1
		}
		if b[end] == 0x1B && end+1 < len(b) && b[end+1] == '\\' {
			body := string(b[2:end])
			g.applyOSC(body)
			return end + 2
		}
		end++
	}
	return end
}

// handleAPC parses ESC _ <params> (ST). Cursor marker is APC; we drop it.
func (g *Grid) handleAPC(b []byte) int {
	end := 2
	for end < len(b) {
		if b[end] == 0x07 {
			return end + 1
		}
		if b[end] == 0x1B && end+1 < len(b) && b[end+1] == '\\' {
			return end + 2
		}
		end++
	}
	return end
}

func (g *Grid) applyOSC(body string) {
	// OSC 8 ; params ; URI: hyperlink. Empty URI ends the link.
	if strings.HasPrefix(body, "8;") {
		rest := body[2:]
		// skip the "params" segment up to next ';'
		if _, after, ok := strings.Cut(rest, ";"); ok {
			g.link = after
		}
	}
}

func (g *Grid) eraseDisplay(n int) {
	switch n {
	case 0: // cursor to end
		g.eraseLineFrom(g.curRow, g.curCol)
		for r := g.curRow + 1; r < g.Rows; r++ {
			g.cells[r] = makeRow(g.Cols)
		}
	case 1: // start to cursor
		for r := 0; r < g.curRow; r++ {
			g.cells[r] = makeRow(g.Cols)
		}
		g.eraseLineTo(g.curRow, g.curCol)
	case 2, 3: // entire screen (3 also clears scrollback)
		for r := 0; r < g.Rows; r++ {
			g.cells[r] = makeRow(g.Cols)
		}
		if n == 3 {
			g.scrollback = nil
		}
	}
}

func (g *Grid) eraseLine(n int) {
	switch n {
	case 0:
		g.eraseLineFrom(g.curRow, g.curCol)
	case 1:
		g.eraseLineTo(g.curRow, g.curCol)
	case 2:
		g.cells[g.curRow] = makeRow(g.Cols)
	}
}

func (g *Grid) eraseLineFrom(row, col int) {
	if row < 0 || row >= g.Rows {
		return
	}
	for c := col; c < g.Cols; c++ {
		g.cells[row][c] = Cell{Rune: ' '}
	}
}

func (g *Grid) eraseLineTo(row, col int) {
	if row < 0 || row >= g.Rows {
		return
	}
	for c := 0; c <= col && c < g.Cols; c++ {
		g.cells[row][c] = Cell{Rune: ' '}
	}
}

func (g *Grid) applySGR(params string) {
	if params == "" {
		g.style = Style{}
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		n, _ := atoi(parts[i])
		switch {
		case n == 0:
			g.style = Style{}
		case n == 1:
			g.style.Bold = true
		case n == 2:
			g.style.Dim = true
		case n == 3:
			g.style.Italic = true
		case n == 4:
			g.style.Underline = true
		case n == 7:
			g.style.Inverse = true
		case n == 9:
			g.style.Strike = true
		case n == 22:
			g.style.Bold = false
			g.style.Dim = false
		case n == 23:
			g.style.Italic = false
		case n == 24:
			g.style.Underline = false
		case n == 27:
			g.style.Inverse = false
		case n == 29:
			g.style.Strike = false
		case n >= 30 && n <= 37:
			g.style.FG = Color{Mode: Color16, N: uint8(n - 30)}
		case n == 38:
			i, g.style.FG = parseExtendedColor(parts, i)
		case n == 39:
			g.style.FG = Color{}
		case n >= 40 && n <= 47:
			g.style.BG = Color{Mode: Color16, N: uint8(n - 40)}
		case n == 48:
			i, g.style.BG = parseExtendedColor(parts, i)
		case n == 49:
			g.style.BG = Color{}
		case n >= 90 && n <= 97:
			g.style.FG = Color{Mode: Color16, N: uint8(n - 90 + 8)}
		case n >= 100 && n <= 107:
			g.style.BG = Color{Mode: Color16, N: uint8(n - 100 + 8)}
		}
	}
}

func parseExtendedColor(parts []string, i int) (int, Color) {
	if i+1 >= len(parts) {
		return i, Color{}
	}
	mode, _ := atoi(parts[i+1])
	switch mode {
	case 2:
		if i+4 >= len(parts) {
			return len(parts), Color{}
		}
		r, _ := atoi(parts[i+2])
		gn, _ := atoi(parts[i+3])
		bl, _ := atoi(parts[i+4])
		return i + 4, Color{Mode: ColorTrueColor, R: byte(r), G: byte(gn), B: byte(bl)}
	case 5:
		if i+2 >= len(parts) {
			return len(parts), Color{}
		}
		n, _ := atoi(parts[i+2])
		return i + 2, Color{Mode: Color256, N: byte(n)}
	}
	return i + 1, Color{}
}

// String returns the visible content as a newline-joined string with trailing
// spaces trimmed per row.
func (g *Grid) String() string {
	var b strings.Builder
	for r := 0; r < g.Rows; r++ {
		b.WriteString(rowToString(g.cells[r]))
		if r < g.Rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Scrollback returns rows that have scrolled past the top of the grid.
func (g *Grid) Scrollback() []string { return append([]string(nil), g.scrollback...) }

// CellAt returns the cell at (row, col). Out-of-range returns an empty cell.
func (g *Grid) CellAt(row, col int) Cell {
	if row < 0 || row >= g.Rows || col < 0 || col >= g.Cols {
		return Cell{}
	}
	return g.cells[row][col]
}

// Cursor returns the current (row, col) position.
func (g *Grid) Cursor() (int, int) { return g.curRow, g.curCol }

// StyleAt returns the style at the cursor position.
func (g *Grid) Style() Style { return g.style }

// SyncDepth reports the current mode 2026 nesting (0 = not inside sync block).
func (g *Grid) SyncDepth() int { return g.syncDepth }

func rowToString(row []Cell) string {
	var b strings.Builder
	last := len(row) - 1
	for last >= 0 && row[last].Rune == ' ' && !row[last].Continuation {
		last--
	}
	for i := 0; i <= last; i++ {
		c := row[i]
		if c.Continuation {
			continue
		}
		if c.Rune == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(c.Rune)
		}
	}
	return b.String()
}

// ─── small helpers ───────────────────────────────────────────────────────────

func parseN(s string, def int) int {
	if s == "" {
		return def
	}
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	n, err := atoi(s)
	if err != nil || n == 0 {
		return def
	}
	return n
}

func parseTwo(s string, da, db int) (int, int) {
	if s == "" {
		return da, db
	}
	parts := strings.SplitN(s, ";", 2)
	a, _ := atoi(parts[0])
	if a == 0 {
		a = da
	}
	b := db
	if len(parts) > 1 {
		bb, _ := atoi(parts[1])
		if bb != 0 {
			b = bb
		}
	}
	return a, b
}

func atoi(s string) (int, error) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return n, errBadNum
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var errBadNum = stringErr("bad num")

type stringErr string

func (e stringErr) Error() string { return string(e) }

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
