// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"slices"
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// cell is one grapheme cluster of a row, a single code point, packed: where its bytes start in the row's line (16 bits), its column
// (16), how many more columns than counted a terminal may give the row's cells up to and including it (16), its pen as an index into
// the row's pens (8), how many bytes it has (3), how many columns (2), and whether every terminal draws it in that many columns (1).
// A line that does not fit the fields (maxLine bytes, maxPens pens) is one the writer does not take.
type cell uint64

const (
	cellColShift   = 16
	cellOverShift  = 32
	cellPenShift   = 48
	cellSizeShift  = 56
	cellWidthShift = 59
	cellCertain    = cell(1) << 61

	maxLine = 1<<16 - 1
	maxPens = 1 << 8
)

func (c cell) off() int      { return int(c & 0xffff) }
func (c cell) col() int      { return int(c >> cellColShift & 0xffff) }
func (c cell) over() int     { return int(c >> cellOverShift & 0xffff) }
func (c cell) pen() int      { return int(c >> cellPenShift & 0xff) }
func (c cell) size() int     { return int(c >> cellSizeShift & 7) }
func (c cell) width() int    { return int(c >> cellWidthShift & 3) }
func (c cell) certain() bool { return c&cellCertain != 0 }
func (c cell) end() int      { return c.off() + c.size() }

// row is what the terminal shows on a row.
type row struct {
	known     bool   // the row shows cells, written from column 1 after the row was erased under the default pen, and erased columns after them
	partial   bool   // the frames rewrote the row since the writer last wrote it whole
	lineOnly  bool   // the row was written whole, as the frame wrote it, and shows line, which is not read into cells yet: the row is not known until it is
	line      []byte // the line as the frame wrote it
	cells     []cell
	pens      []pen // the pens of the cells, in the order the line sets them; the first is the default pen
	width     int   // the columns the cells cover
	endPen    pen   // the pen the line leaves
	uncertain int   // the first cell that is not certain; len(cells) when all are
	inked     int   // how many cells the row has through its last one that is not a blank under the default pen
}

func (r *row) blank(known bool) {
	r.known, r.partial, r.lineOnly = known, false, false
	r.cells, r.line, r.pens = r.cells[:0], r.line[:0], r.pens[:0]
	r.width, r.uncertain, r.endPen, r.inked = 0, 0, pen{}, 0
}

// blankAt reports whether cell i is a blank under the default pen: the one cell a terminal may keep no trace of having been sent
// (tmux does not store it past the cells the row's line has).
func (r *row) blankAt(i int) bool {
	c := r.cells[i]
	return c.size() == 1 && r.line[c.off()] == ' ' && r.pens[c.pen()] == pen{}
}

// inkBefore is how many of the row's first n cells there are through the last of them that is not a blank under the default pen.
func (r *row) inkBefore(n int) int {
	for i := range slices.Backward(r.cells[:n]) {
		if !r.blankAt(i) {
			return i + 1
		}
	}
	return 0
}

// reach is the last column a terminal may have reached after the cells [0,j).
func (r *row) reach(j int) int {
	if j == 0 {
		return 0
	}
	c := r.cells[j-1]
	return c.col() + c.width() + c.over()
}

// certainWidth reports whether every terminal draws cluster in one column: printable ASCII, a Braille pattern, or a Latin, Greek or
// Cyrillic letter that is not of ambiguous East Asian width.
func certainWidth(cluster string) bool {
	r, size := utf8.DecodeRuneInString(cluster)
	if size != len(cluster) {
		return false
	}
	if r >= 0x20 && r < 0x7f || r >= 0x2800 && r <= 0x28ff {
		return true
	}
	if r >= 0xa1 && r < 0x530 && unicode.IsLetter(r) {
		// go-runewidth's ambiguous table is already in the binary (the ANSI package links it). For every letter of this range it gives the answer
		// of golang.org/x/text/width (Neutral or EastAsianNarrow), which would link a 15 KB table for this one question:
		// TestCertainWidthAgreesWithXTextWidth compares the two over the whole range.
		return !runewidth.IsAmbiguousWidth(r)
	}
	return false
}

func hasC1(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == 0xc2 && s[i+1] >= 0x80 && s[i+1] <= 0x9f {
			return true
		}
	}
	return false
}

// The hyperlink close a line's reset ends with (widthx.SegmentReset), in both terminators.
const (
	linkCloseBEL = "\x1b]8;;\x07"
	linkCloseST  = "\x1b]8;;\x1b\\"
)

// symbolWidths remembers, for each character of U+2500..U+28FF (box drawing, blocks, shapes, symbols, dingbats, arrows, Braille), the
// columns the renderers count for it: 0 not asked yet, symbolRejected a width other than 1 or 2, else the width, with symbolCertain
// when every terminal draws it as wide.
type symbolWidths [0x400]uint8

const (
	symbolCertain  = 0x80
	symbolRejected = 0x7f
)

// symbolAt reports whether line holds, at i, a character of U+2500..U+28FF: 0xE2, a byte of 0x94..0xA3 and a continuation byte.
func symbolAt(line string, i int) bool {
	return i+2 < len(line) && line[i] == 0xe2 && line[i+1] >= 0x94 && line[i+1] <= 0xa3 && line[i+2]&0xc0 == 0x80
}

// buffers makes the row's buffers once: a full row of cells, every pen a row may have, and room bytes of line at least.
func (r *row) buffers(cols, lineLen, room int) {
	if cap(r.cells) <= cols {
		r.cells = make([]cell, 0, cols+1)
	}
	if cap(r.pens) < maxPens {
		r.pens = make([]pen, 0, maxPens)
	}
	if cap(r.line) < lineLen {
		r.line = make([]byte, 0, max(lineLen, room))
	}
}

// parse reads line into r's cells (readLine) and keeps the line.
func (r *row) parse(line string, old *row, cols, room int, symbols *symbolWidths) (shared, same int, ok bool) {
	if shared, same, ok = r.readLine(line, old, cols, room, symbols); ok {
		r.line = append(r.line[:0], line...)
	}
	return shared, same, ok
}

// readLine reads line, written from column 1 of a row erased under the default pen, into r's cells, and returns how many of its first cells are
// old's first cells unchanged and how many of its last are old's last, where they were. The line itself is not kept: line may be the line r keeps. ok is false for a line the writer does not take: one with a sequence other than SGR of the forms applySGR
// reads and the hyperlink close, a control character, a cluster of several code points or without a width of 1 or 2, cells past the
// screen's edge, or more bytes or pens than a cell can name. room is the length of the longest line the rows may hold: a row's buffers
// are made once, so the rows, which trade their buffers as they are written, stop allocating.
//
// What the line shares with old's line is not read again. The cells before the first byte that differs are old's (but the last, whose
// cluster the bytes after it decide). The cells in the bytes the two lines share at their end are old's too, moved to where they are
// now, from the first of them that the reading reaches at a cluster's start under the pen old had there: from the same place under
// the same pen the same bytes read the same.
func (r *row) readLine(line string, old *row, cols, room int, symbols *symbolWidths) (shared, same int, ok bool) {
	if len(line) > maxLine || cols > maxLine {
		return 0, 0, false
	}
	r.buffers(cols, len(line), room)
	cells, pens := r.cells[:0], r.pens[:0]
	pens = append(pens, pen{})
	uncertain := -1
	var p pen
	var over, at cell          // the over count and the pen's index, in their fields
	col, start, ink := 0, 0, 0 // ink: the cells so far through the last that is not a blank under the default pen
	// tail is the first of old's cells that may still be taken over, and splice where in line it would start.
	tail, splice, moved := 0, len(line)+1, 0
	if old != nil && old.known && len(old.cells) > 0 {
		// A cell at the start is taken only when four more bytes are shared after it, so the code point that follows it, which decides
		// where its cluster ends, is the same.
		common := commonPrefix(old.line, bytesOf(line))
		shared = sort.Search(len(old.cells), func(i int) bool { return old.cells[i].end() > common-4 })
		if shared > 0 {
			cells = append(cells, old.cells[:shared]...)
			last := old.cells[shared-1]
			pens = append(pens[:0], old.pens[:last.pen()+1]...)
			p, at, over = pens[last.pen()], last&(0xff<<cellPenShift), last&(0xffff<<cellOverShift)
			col, start = last.col()+last.width(), last.end()
			if old.uncertain < shared {
				uncertain = old.uncertain
			}
			if ink = old.inked; ink > shared {
				ink = old.inkBefore(shared)
			}
		}
		moved = len(line) - len(old.line)
		first := len(old.line) - commonSuffix(old.line, bytesOf(line))
		tail = sort.Search(len(old.cells), func(i int) bool { return old.cells[i].off() >= first })
		if tail < len(old.cells) {
			splice = old.cells[tail].off() + moved
		}
	}
	// Every cell takes a column at least and col stays within cols, so the cells stay within the cols+1 the buffer holds.
	for i := start; i < len(line); {
		if i >= splice {
			for tail < len(old.cells) && old.cells[tail].off()+moved < i {
				tail++
			}
			splice = len(line) + 1
			if tail < len(old.cells) {
				splice = old.cells[tail].off() + moved
			}
			if i == splice {
				first := old.cells[tail]
				if rest := len(old.pens) - 1 - first.pen(); p == old.pens[first.pen()] && len(pens)+rest <= maxPens {
					// The rest of the line is old's from this cell on: its cells, where they are now.
					if col += old.width - first.col(); col > cols {
						return 0, 0, false
					}
					if col == old.width {
						same = len(old.cells) - tail
					}
					var before cell
					if tail > 0 {
						before = old.cells[tail-1] & (0xffff << cellOverShift)
					}
					shift := cell(moved) + cell(col-old.width)<<cellColShift + (over - before) + (at - first&(0xff<<cellPenShift))
					n := len(cells)
					cells = cells[:n+len(old.cells)-tail]
					if shift == 0 {
						copy(cells[n:], old.cells[tail:])
					} else {
						for k, c := range old.cells[tail:] {
							cells[n+k] = c + shift
						}
					}
					if old.inked > tail {
						ink = n + old.inked - tail
					}
					switch {
					case uncertain >= 0:
					case old.uncertain >= tail:
						if old.uncertain < len(old.cells) {
							uncertain = n + old.uncertain - tail
						}
					default:
						for k, c := range old.cells[tail:] {
							if !c.certain() {
								uncertain = n + k
								break
							}
						}
					}
					pens = append(pens, old.pens[first.pen()+1:]...)
					p = old.endPen
					break
				}
				// Not under the pen old had here: the next of old's cells may be.
				tail++
				splice = len(line) + 1
				if tail < len(old.cells) {
					splice = old.cells[tail].off() + moved
				}
			}
		}
		c := line[i]
		if c == 0x1b {
			kind, end := nextToken(line, i)
			seq := line[i:end]
			switch {
			case kind == tokenCSI && seq[len(seq)-1] == 'm':
				before := p
				if !p.applySGR(seq[2 : len(seq)-1]) {
					return 0, 0, false
				}
				if p != before {
					if len(pens) == maxPens {
						return 0, 0, false
					}
					at = cell(len(pens)) << cellPenShift
					pens = append(pens, p)
				}
			case kind == tokenOSC && (seq == linkCloseBEL || seq == linkCloseST):
			default:
				return 0, 0, false
			}
			i = end
			continue
		}
		if c < 0x20 || c == 0x7f {
			return 0, 0, false
		}
		if c < 0x80 {
			// A run of printable ASCII: a cell each, but for the last one when a byte of another character follows it, which may be a
			// mark that joins it. The run stops where old's cells may be taken over.
			j := asciiEnd(line, i+1)
			if j < len(line) && line[j] >= 0x80 {
				j--
			}
			if j = min(j, splice); j > i {
				n := len(cells)
				if col+j-i > cols {
					return 0, 0, false
				}
				cells = cells[:n+j-i]
				next := cell(i) | cell(col)<<cellColShift | over | at | 1<<cellSizeShift | 1<<cellWidthShift | cellCertain
				for k := n; k < len(cells); k++ {
					cells[k] = next
					next += 1 | 1<<cellColShift
				}
				// The run's cells are blanks under the default pen from its last byte that is not a space on.
				last := j
				if p == (pen{}) {
					for last > i && line[last-1] == ' ' {
						last--
					}
				}
				if last > i {
					ink = n + last - i
				}
				col += j - i
				i = j
				continue
			}
		}
		next := cell(i) | cell(col)<<cellColShift | at
		var wd int
		if symbolAt(line, i) && (i+3 == len(line) || line[i+3] < 0x80 || symbolAt(line, i+3)) {
			// A character of U+2500..U+28FF that an ASCII byte, an escape sequence or another such character follows is a cluster of its
			// own (nothing in those ranges or in ASCII extends a cluster): the segmenter is not needed for it, and its width is remembered.
			known := &symbols[int(line[i+1]-0x94)<<6|int(line[i+2]&0x3f)]
			if *known == 0 {
				cluster := line[i : i+3]
				switch wd := widthx.GraphemeWidth(cluster); {
				case wd < 1 || wd > 2:
					*known = symbolRejected
				case wd == 1 && certainWidth(cluster):
					*known = 1 | symbolCertain
				default:
					*known = uint8(wd)
				}
			}
			if *known == symbolRejected {
				return 0, 0, false
			}
			wd = int(*known &^ symbolCertain)
			next |= 3<<cellSizeShift | cell(wd)<<cellWidthShift
			step := cell(3) | cell(wd)<<cellColShift
			if *known&symbolCertain != 0 {
				next |= cellCertain
			} else {
				if uncertain < 0 {
					uncertain = len(cells)
				}
				// A terminal gives a code point two columns at most.
				step |= cell(2-wd) << cellOverShift
				over += cell(2-wd) << cellOverShift
			}
			next |= over
			for second, third := line[i+1], line[i+2]; ; {
				cells = append(cells, next)
				i += 3
				if col += wd; col > cols {
					return 0, 0, false
				}
				// The same character again, as much a cluster of its own, short of where old's cells may be taken over.
				if i == splice || i+2 >= len(line) || line[i] != 0xe2 || line[i+1] != second || line[i+2] != third || i+3 < len(line) && line[i+3] >= 0x80 && !symbolAt(line, i+3) {
					break
				}
				next += step
				over += step & (0xffff << cellOverShift)
			}
			ink = len(cells)
			continue
		}
		cluster, _ := widthx.FirstGrapheme(line[i:])
		wd = widthx.GraphemeWidth(cluster)
		// A cluster of several code points (a combining mark, a joiner, a variation selector) is where terminals differ most, down to
		// what they do with the text around it: a row holding one is written whole, as the frame writes it.
		if len(cluster) == 0 || wd < 1 || wd > 2 || hasC1(cluster) || utf8.RuneCountInString(cluster) > 1 {
			return 0, 0, false
		}
		if wd == 1 && certainWidth(cluster) {
			next |= cellCertain
		}
		next |= cell(len(cluster)) << cellSizeShift
		i += len(cluster)
		if !next.certain() {
			if uncertain < 0 {
				uncertain = len(cells)
			}
			// A terminal gives a code point two columns at most.
			over += cell(2-wd) << cellOverShift
		}
		cells = append(cells, next|over|cell(wd)<<cellWidthShift)
		ink = len(cells)
		if col += wd; col > cols {
			return 0, 0, false
		}
	}
	r.cells, r.pens = cells, pens
	r.width = col
	r.endPen = p
	r.inked = ink
	if r.uncertain = uncertain; uncertain < 0 {
		r.uncertain = len(cells)
	}
	return shared, same, true
}

// commonPrefix is how many bytes a and b share at their start.
func commonPrefix(a, b []byte) int {
	n, limit := 0, min(len(a), len(b))
	for ; n+8 <= limit; n += 8 {
		if x := binary.LittleEndian.Uint64(a[n:]) ^ binary.LittleEndian.Uint64(b[n:]); x != 0 {
			return n + bits.TrailingZeros64(x)/8
		}
	}
	for n < limit && a[n] == b[n] {
		n++
	}
	return n
}

// commonSuffix is how many bytes a and b share at their end.
func commonSuffix(a, b []byte) int {
	n, limit := 0, min(len(a), len(b))
	// Sixty-four bytes at a time while they are the same (bytes.Equal compares them in the machine's widest registers), then by words.
	for ; n+64 <= limit && bytes.Equal(a[len(a)-n-64:len(a)-n], b[len(b)-n-64:len(b)-n]); n += 64 {
	}
	for ; n+8 <= limit; n += 8 {
		if x := binary.LittleEndian.Uint64(a[len(a)-n-8:]) ^ binary.LittleEndian.Uint64(b[len(b)-n-8:]); x != 0 {
			return n + bits.LeadingZeros64(x)/8
		}
	}
	for n < limit && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// sameCell reports whether o's cell i and t's cell j are the same cluster under the same pen in the same column.
func sameCell(o *row, i int, t *row, j int) bool {
	a, b := o.cells[i], t.cells[j]
	return a.col() == b.col() && a.size() == b.size() && o.pens[a.pen()] == t.pens[b.pen()] && bytes.Equal(o.line[a.off():a.end()], t.line[b.off():b.end()])
}

// uncertainAlike reports whether o's cells [from,i1) and t's cells [from,j1) hold the same clusters of uncertain width in the same
// order, so that a terminal draws both ranges as wide as each other whatever widths it gives those clusters.
func uncertainAlike(o *row, i1 int, t *row, j1, from int) bool {
	if o.uncertain >= i1 && t.uncertain >= j1 {
		return true
	}
	i, j := from, from
	for {
		for i < i1 && o.cells[i].certain() {
			i++
		}
		for j < j1 && t.cells[j].certain() {
			j++
		}
		if i == i1 || j == j1 {
			return i == i1 && j == j1
		}
		a, b := o.cells[i], t.cells[j]
		if !bytes.Equal(o.line[a.off():a.end()], t.line[b.off():b.end()]) {
			return false
		}
		i++
		j++
	}
}
