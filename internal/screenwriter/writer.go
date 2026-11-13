// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

// Package screenwriter translates the bytes the terminal renderers write into fewer bytes that leave the same screen (D624).
//
// A Writer sits under a terminal's write. It reads every write, keeps what the writes leave on the alternate screen (rows of cells, the
// pen, the cursor, the scroll region) and, for a frame it can read completely, sends only what changes that screen:
//
//   - a row the frame rewrites with the cells it already shows is not written;
//   - a row whose cells change is written in place, from its first cell through its last changed one, as the first bytes of the
//     frame's own line (the first-cell rule), when the line is no narrower than the row, has a cell that is not a blank under
//     the default pen wherever the row has one (the same cluster, where terminals disagree on its width), and has others only
//     after the last of them; a line that has others is written through its last cell; what follows the written cells stays;
//   - a row as wide as the screen of which that would send every cell is written whole instead: the writer prints in the screen's
//     last column only as the frame does, and such a row waits for no settle write;
//   - every other row whose cells change is written whole, as the frame wrote it: the line erased, then the row;
//   - a frame that changes no cell, cursor position or cursor visibility is not written;
//   - once no frame was written for the settle time, every row that had a part written is rewritten whole, as the frame wrote it.
//
// The frame grammar is what TuiAltScreen.doRender writes (packages/tui/src/tui-alt-screen.ts) and D205 adds to it: a synchronized
// update holding cursor positions, rows erased at column 1 and followed by their line (text, SGR, the hyperlink close), a scroll region
// with line feeds or reverse indexes, an erase of the screen, and the cursor shown or hidden. A frame with anything else in it, and
// every write that is not a frame, is sent as it came; unless the table of sequences that change nothing the writer keeps covers it
// (sequence.go), every row is then forgotten and written whole when a frame next writes it. The writer makes no sequence of its own but
// the cursor address of a row's first column and a reset of the pen; a cursor address, a scroll and a region the frame wrote are sent
// as the frame wrote them.
//
// What tmux keeps of a line is modelled by the screens of the tests (gridterm) and checked against tmux itself: the lines of tmux
// 3.4 and 3.6 are recorded there, and where the tests find a tmux they ask that one (3.4 in the Linux container the tests run in).
// CI sets PIG_GRIDTERM_TMUX_REQUIRED on its Linux job: there the test that asks tmux fails where it would otherwise be skipped.
//
// Frames are translated only while autowrap is known to be off: a write turned it off (CSI ? 7 l) and none since turned it on,
// switched to or from an alternate screen, restored saved modes or reset the terminal. With autowrap on tmux stores a row's text
// in the runs it reads, and the line it keeps follows no rule of the writer's. While autowrap is not known to be off every frame
// is sent as it came and the rows are forgotten, so that the first frame after it is turned off again writes its rows whole.
//
// Before the alternate screen is entered and after it is left every byte is sent as it came.
//
// pig divergence (D624): tui.ProcessTerminal puts a Writer under its write when the terminal.screenWriter setting or PIG_TUI_SCREEN_WRITER selects it (tui/terminal_screen_writer.go).
package screenwriter

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"
)

// DefaultSettle is how long after the last written frame the rows that had a part written are rewritten whole.
const DefaultSettle = 250 * time.Millisecond

// Options are what a Writer needs from the terminal it writes for.
type Options struct {
	// Size is the terminal's size in cells.
	Size func() (cols, rows int)
	// Settle is the settle time: 0 is DefaultSettle, a negative value leaves the settle write to the caller (Settled).
	Settle time.Duration
	// Lock and Unlock hold the terminal's output lock, and Emit writes to the terminal under it: the settle timer's write.
	Lock, Unlock func()
	Emit         func(data string)
	// Record, when set, receives every write as it came and as it was sent on (RecordFrame, RecordOther; RecordSettle with no input).
	Record func(kind byte, in, out string)
}

// The kinds of write Options.Record receives.
const (
	RecordFrame  byte = 'F' // a synchronized update
	RecordOther  byte = 'R' // any other write, sent as it came
	RecordSettle byte = 'S' // the settle write, which no write of the renderer stands for
)

// AppendRecord appends a write to a record log: the kind, the two lengths as little-endian 32-bit numbers, then the bytes.
func AppendRecord(log []byte, kind byte, in, out string) []byte {
	log = append(log, kind)
	log = binary.LittleEndian.AppendUint32(log, uint32(len(in)))
	log = binary.LittleEndian.AppendUint32(log, uint32(len(out)))
	return append(append(log, in...), out...)
}

// NextRecord reads the first record of log and returns the rest; ok is false at the end of the log or at a record cut short.
func NextRecord(log []byte) (kind byte, in, out, rest []byte, ok bool) {
	if len(log) < 9 {
		return 0, nil, nil, nil, false
	}
	n, m := int(binary.LittleEndian.Uint32(log[1:])), int(binary.LittleEndian.Uint32(log[5:]))
	if len(log)-9 < n+m {
		return 0, nil, nil, nil, false
	}
	return log[0], log[9 : 9+n], log[9+n : 9+n+m], log[9+n+m:], true
}

// Stats counts what a Writer did with the synchronized updates it was given.
type Stats struct {
	Frames     int // synchronized updates read
	Translated int // of them, written as what changes the screen
	Unchanged  int // of them, not written: they changed nothing
	Repaints   int // of them, sent as they came: they erase the screen
	Unread     int // of them, sent as they came: something in them has no rule
	Wrapping   int // of them, sent as they came: autowrap was not known to be off
	Settles    int // settle writes
	BytesIn    int // bytes of the synchronized updates read
	BytesOut   int // bytes written for them, settle writes included
}

// Writer is the screen writer of one terminal. Translate and Settled are called with the terminal's output lock held; a write that
// leaves the alternate screen may come without it (the exit restore of a terminal whose lock a stuck write holds), and Outside needs
// none.
type Writer struct {
	size   func() (cols, rows int)
	settle time.Duration
	lock   func()
	unlock func()
	emit   func(string)
	record func(kind byte, in, out string)

	active     atomic.Bool // the alternate screen is shown
	past       atomic.Bool // a write that may have changed the screen reached the terminal past the writer
	dropped    atomic.Bool // Drop was called: every write is sent as it came
	pastWrap   atomic.Bool // a write that says something of autowrap reached the terminal past the writer
	wrapOff    bool        // autowrap is known to be off: frames are translated only then
	cols, high int
	rows       []row
	scratch    row

	// What the frames' own bytes leave: the terminal's state before and after every write.
	penKnown bool
	pen      pen
	curKnown bool
	curRow   int
	curCol   int
	visKnown bool
	visible  bool
	top, bot int
	// What the terminal has while a frame is being written; equal to the above between writes.
	aPen      pen
	aCurKnown bool
	aRow      int
	aCol      int

	out        []byte
	room       int // the room a row's line buffer is made with: half again the longest line so far
	symbols    symbolWidths
	anyPartial bool
	lastWrite  time.Time
	timer      *time.Timer
	armed      bool
	stopped    bool // Stop was called: the settle timer neither fires nor is armed again
	stats      Stats
}

// New returns a Writer for a terminal that is not in the alternate screen.
func New(options Options) *Writer {
	w := &Writer{size: options.Size, settle: options.Settle, lock: options.Lock, unlock: options.Unlock, emit: options.Emit, record: options.Record}
	if w.settle == 0 {
		w.settle = DefaultSettle
	}
	return w
}

// Stats returns the counts so far.
func (w *Writer) Stats() Stats { return w.stats }

// Active reports whether the writer translates frames now: the alternate screen is shown and the writer was not dropped. It is one atomic load and may be called
// without the terminal's output lock.
func (w *Writer) Active() bool { return w.active.Load() }

const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
)

// Translate is what the terminal is sent for data, a write of a renderer or of anything else that writes through the terminal: data
// itself, the bytes that change the screen as data does, or nothing. The result is valid until the next call.
func (w *Writer) Translate(data string) string {
	if w.dropped.Load() {
		return data
	}
	if !w.active.Load() || len(data) < len(syncBegin)+len(syncEnd) || !strings.HasPrefix(data, syncBegin) || !strings.HasSuffix(data, syncEnd) {
		w.other(data)
		if w.record != nil {
			w.record(RecordOther, data, data)
		}
		return data
	}
	w.catchUp()
	if !w.wrapOff {
		w.unread(data)
		w.stats.Frames++
		w.stats.Wrapping++
		w.stats.BytesIn += len(data)
		w.stats.BytesOut += len(data)
		if w.record != nil {
			w.record(RecordFrame, data, data)
		}
		return data
	}
	w.resize()
	w.stats.Frames++
	w.stats.BytesIn += len(data)
	body := data[len(syncBegin) : len(data)-len(syncEnd)]
	// A frame that erases the screen is sent as it came, and the screen kept is what it leaves. The renderers erase before they
	// write a row; an erase further in is a frame without a rule.
	whole := strings.Contains(body[:min(len(body), 64)], "\x1b[2J")
	out := data
	switch ok := w.frame(body, whole); {
	case !ok:
		w.unread(data)
		w.stats.Unread++
	case whole:
		w.stats.Repaints++
	case len(w.out) == len(syncBegin):
		w.stats.Unchanged++
		out = ""
	default:
		w.stats.Translated++
		w.out = append(w.out, syncEnd...)
		out = unsafe.String(unsafe.SliceData(w.out), len(w.out)) //nolint:gosec // G103: the writer's own buffer as the result, valid until the next call, without a copy of every frame.
	}
	if w.anyPartial && w.settle > 0 {
		w.lastWrite = time.Now()
		w.arm(w.settle)
	}
	w.stats.BytesOut += len(out)
	if w.record != nil {
		w.record(RecordFrame, data, out)
	}
	return out
}

// Outside takes a write that reached the terminal without passing through Translate: unless the table of sequences that change
// nothing the writer keeps covers it, the writer forgets the screen before its next write. It needs no lock.
func (w *Writer) Outside(data string) {
	_, cursor, forget, wrap := outside(data, true)
	if forget || cursor != 0 {
		w.past.Store(true)
	}
	if wrap != 0 {
		w.pastWrap.Store(true)
	}
}

// catchUp forgets the screen after a write past the writer; after one that says something of autowrap, autowrap is not known to be
// off, whatever the write says: where it came among the writes that pass through the writer is not known.
func (w *Writer) catchUp() {
	if w.pastWrap.Load() && w.pastWrap.Swap(false) {
		w.wrapOff = false
		w.forget()
	}
	if w.past.Load() && w.past.Swap(false) && w.active.Load() {
		w.resize()
		w.forget()
	}
}

// unread follows a frame that is sent as it came: the screen kept is forgotten, and the frame may have left the alternate screen
// or said something of autowrap.
func (w *Writer) unread(data string) {
	w.forget()
	if strings.LastIndex(data, "\x1b[?1049l") > strings.LastIndex(data, "\x1b[?1049h") {
		w.active.Store(false)
	}
	if _, _, _, wrap := outside(data, true); wrap != 0 {
		w.wrapOff = wrap > 0
	}
}

// other follows a write that is not a frame, which is sent as it came: the screen kept stays, or is forgotten. A write that leaves
// the alternate screen, or comes while it is not shown, changes nothing but whether it is shown: nothing of the main screen is kept,
// and entering the alternate screen forgets everything.
func (w *Writer) other(data string) {
	shown := w.active.Load()
	if !shown && !strings.Contains(data, "\x1b[?1049h") {
		return
	}
	screen, cursor, forget, wrap := outside(data, shown)
	if !screen {
		if shown {
			w.active.Store(false)
		}
		w.wrapOff = false
		return
	}
	w.catchUp()
	w.active.Store(true)
	if wrap != 0 {
		w.wrapOff = wrap > 0
	}
	switch {
	case forget || wrap < 0:
		w.resize()
		w.forget()
	case cursor != 0:
		w.visible, w.visKnown = cursor > 0, true
	}
}

// forget makes every row, the pen, the cursor and the scroll region unknown.
func (w *Writer) forget() {
	for i := range w.rows {
		w.rows[i].known, w.rows[i].partial, w.rows[i].lineOnly = false, false, false
	}
	w.penKnown, w.curKnown, w.visKnown, w.aCurKnown = false, false, false, false
	w.top, w.bot = 0, w.high-1
	w.anyPartial = false
}

// resize follows the terminal's size: at another size nothing is known.
func (w *Writer) resize() {
	cols, rows := w.size()
	if cols == w.cols && rows == w.high && len(w.rows) == rows {
		return
	}
	w.cols, w.high = cols, max(rows, 0)
	if cap(w.rows) < w.high {
		w.rows = append(w.rows[:cap(w.rows)], make([]row, w.high-cap(w.rows))...)
	}
	w.rows = w.rows[:w.high]
	w.forget()
}

// moveTo puts the terminal's cursor at row, col: the frame's own cursor address, or a row's first column.
func (w *Writer) moveTo(row, col int) {
	if w.aCurKnown && w.aRow == row && w.aCol == col {
		return
	}
	w.out = append(w.out, "\x1b["...)
	w.out = strconv.AppendInt(w.out, int64(row+1), 10)
	w.out = append(w.out, ';')
	w.out = strconv.AppendInt(w.out, int64(col+1), 10)
	w.out = append(w.out, 'H')
	w.aCurKnown, w.aRow, w.aCol = true, row, col
}

// setPen makes the terminal's pen p and reports false when that takes more than a reset: the writer makes no other SGR sequence.
func (w *Writer) setPen(p pen) bool {
	switch {
	case w.aPen == p:
	case p == pen{}:
		w.out = append(w.out, "\x1b[0m"...)
		w.aPen = p
	default:
		return false
	}
	return true
}

// writeWhole writes a row as the frame did: erased under the default pen, then r's line.
func (w *Writer) writeWhole(index int, r *row) {
	w.moveTo(index, 0)
	w.setPen(pen{})
	w.out = append(w.out, "\x1b[2K"...)
	w.out = append(w.out, r.line...)
	w.aCurKnown = false
	w.aPen = r.endPen
}

// writeDiff makes a row that shows o show t in place: the first bytes of t's line through its last changed cell. shared and same are
// how many of t's first and last cells are known to be o's (parse); 0 when not known. It reports false, having written nothing, when
// only writing the row whole does.
//
// A row written in place must leave a terminal the line the frame's erase and line leave it: the cells, and what the terminal keeps
// of a line besides them. tmux keeps how many cells a line has and how far cells were stored in it, frees them only when the line
// is erased whole, and does not store a blank under the default pen past the cells the line has: both depend on the order in which
// the other cells arrived since the erase and on the blanks that arrived after them, not only on the cells the row shows.
//
// So a row is written in place only when that keeps this invariant: since the row's line was last erased whole (by a whole write
// of the row, a repaint, or a scroll that brought the line in), no line the frames wrote for it was narrower than the one before
// it, each had a cell that is not a default blank exactly where the one before it had one (a cluster of uncertain width being the
// same cluster there) and, besides, only after the last of them, and a line that had some there was sent through its last cell.
// Then the terminal was sent, since the line's erase, the cells that are not default blanks in the order of the columns in which
// the frame's line has them, and after the last of them every cell of the line: what the frame's erase and line send it, some of
// it more than once. Every other way a row changes writes it whole (the settle write), moves the line with the row (a scroll), or
// leaves the row unknown, to be written whole by the next frame (a row erased under another pen, another screen size, the
// alternate screen entered again, a write that is not a frame and may have changed the screen).
func (w *Writer) writeDiff(index int, o, t *row, shared, same int) bool {
	if t.width < o.width {
		return false
	}
	no, nt := len(o.cells), len(t.cells)
	p := shared
	if p == 0 {
		// The cells that end in the bytes the two lines share at their start are the same cells.
		common := commonPrefix(o.line, t.line)
		p = sort.Search(min(no, nt), func(i int) bool { return t.cells[i].end() > common })
	}
	for p < no && p < nt && sameCell(o, p, t, p) {
		p++
	}
	if p == no && no == nt {
		return true
	}
	// The last same of t's cells are o's last cells, where they were (parse). With as many cells in both rows they are the cells of
	// the same index from kept on: none of them is compared again.
	kept := nt
	if no == nt {
		kept = nt - same
	}
	// The cells before p are o's. From there through o's last cell that is not a default blank, t's cells must lie where o's do and
	// be default blanks where o's are.
	ink := o.inked
	if ink > nt {
		return false
	}
	for k := p; k < min(ink, kept); k++ {
		a, b := o.cells[k], t.cells[k]
		if a.col() != b.col() || a.width() != b.width() || o.blankAt(k) != t.blankAt(k) {
			return false
		}
		// A cluster of uncertain width keeps its place: with another cluster there, the cells after it lie in other columns than
		// counted here in a terminal that draws it wider or narrower.
		if (!a.certain() || !b.certain()) && !bytes.Equal(o.line[a.off():a.end()], t.line[b.off():b.end()]) {
			return false
		}
	}
	s := 0
	if o.width == t.width && t.inked == ink {
		limit := min(no, nt) - p
		if s = min(same, limit); s == 0 {
			// The cells that start in the bytes the two lines share at their end are the same clusters: they are the same cells where
			// they are as far from the line's end, in the same column and under the same pen. Where one pair of cells is, every pair
			// after it is: the same bytes are read from the same place under the same pen. So the first such pair is searched for.
			lo, lt := len(o.line), len(t.line)
			tail := commonSuffix(o.line, t.line)
			s = sort.Search(limit, func(k int) bool {
				a, b := o.cells[no-1-k], t.cells[nt-1-k]
				from := lo - a.off()
				return from > tail || from != lt-b.off() || a.col() != b.col() || o.pens[a.pen()] != t.pens[b.pen()]
			})
		}
		// Before them, the same cells in other bytes (another sequence for the same pen).
		for s < limit && sameCell(o, no-1-s, t, nt-1-s) {
			s++
		}
	}
	oe, te := no-s, nt-s
	// Every cell of a row as wide as the screen would be sent. The frame's own write of the row is a few bytes more (the erase and
	// what ends the line) and is whole: what a terminal keeps of the line, after another program wrote to it too, is what the frame
	// leaves, so no settle write is owed for the row; and the last column is printed in only as the frame prints in it.
	if te == nt && t.width == w.cols {
		return false
	}
	// The cells after the written ones stay where they are only if those are as wide as what they replace in every terminal.
	if !uncertainAlike(o, oe, t, te, p) {
		return false
	}
	// A cluster of uncertain width may be wider in the terminal than counted here. Where the row could then reach the screen's edge
	// before the written cells end short of the row's end, the terminal clips it: only the row written whole shows that.
	if te < nt && t.reach(te) > w.cols-1 {
		return false
	}
	w.moveTo(index, 0)
	w.setPen(pen{})
	if te > 0 {
		last := t.cells[te-1]
		w.out = append(w.out, t.line[:last.end()]...)
		w.aPen = t.pens[last.pen()]
		// Short of the last column: a row that reaches it has a cell after these, or was written whole (above).
		w.aCol = last.col() + last.width()
		if t.uncertain < te {
			w.aCurKnown = false
		}
	}
	return true
}

// shows is a row other than index that is known to show line, nil when there is none.
func (w *Writer) shows(line string, index int) *row {
	for i := range w.rows {
		if r := &w.rows[i]; len(r.line) == len(line) && r.known && i != index && string(r.line) == line {
			return r
		}
	}
	return nil
}

// scroll moves the rows of the scroll region by one: up for a line feed at its bottom, down for a reverse index at its top.
func (w *Writer) scroll(up bool) {
	// The row that leaves the region gives its buffers to the row that comes in, blank, at the region's other end.
	region := w.rows[w.top : w.bot+1]
	in := len(region) - 1
	if !up {
		in = 0
	}
	spare := region[len(region)-1-in]
	if up {
		copy(region, region[1:])
	} else {
		copy(region[1:], region)
	}
	region[in] = spare
	region[in].blank(w.penKnown && w.pen == pen{})
}

// frame applies the body of a synchronized update to the screen kept and, unless whole, puts into out what makes the terminal show
// it. It reports false for a body with anything it has no rule for.
func (w *Writer) frame(body string, whole bool) bool {
	w.out = append(w.out[:0], syncBegin...)
	if w.cols < 2 || w.high < 1 {
		return false
	}
	w.aPen, w.aCurKnown, w.aRow, w.aCol = w.pen, w.curKnown, w.curRow, w.curCol
	vis, visSet := w.visible, false
	for i := 0; i < len(body); {
		kind, end := nextToken(body, i)
		seq := body[i:end]
		i = end
		switch kind {
		case tokenCSI:
			final := seq[len(seq)-1]
			params := seq[2 : len(seq)-1]
			switch {
			case final == 'H':
				r, c, ok := twoParams(params)
				if !ok {
					return false
				}
				// The position is kept as written: a column past the screen's edge is the terminal's to place.
				w.curRow, w.curCol, w.curKnown = r-1, c-1, true
			case final == 'K' && params == "2":
				if !w.curKnown || w.curCol != 0 || w.curRow >= w.high {
					return false
				}
				j := lineEnd(body, i)
				positioned := false
				if j < len(body) {
					k, e := nextToken(body, j)
					positioned = k == tokenCSI && body[e-1] == 'H'
				}
				if !w.row(w.curRow, body[i:j], whole, positioned) {
					return false
				}
				i = j
			case final == 'J' && params == "2":
				if !whole {
					return false
				}
				clean := w.penKnown && w.pen == pen{}
				for r := range w.rows {
					w.rows[r].blank(clean)
				}
			case final == 'r':
				t, b := 1, w.high
				if params != "" {
					var ok bool
					if t, b, ok = twoParams(params); !ok || t >= b || b > w.high {
						return false
					}
				}
				// Setting the region homes the cursor in most terminals and leaves it in some: the next write positions it, as the frame's does.
				w.top, w.bot = t-1, b-1
				w.curKnown = false
				if !whole {
					w.out = append(w.out, seq...)
					w.aCurKnown = false
				}
			case seq == "\x1b[?25h" || seq == "\x1b[?25l":
				vis, visSet = final == 'h', true
			default:
				return false
			}
		case tokenC0, tokenEscape:
			up := seq == "\n"
			if !up && seq != "\x1bM" {
				return false
			}
			if !w.curKnown || !w.penKnown || w.curRow >= w.high {
				return false
			}
			if !whole {
				// The terminal scrolls from where the frame's cursor is, under the frame's pen.
				w.moveTo(w.curRow, w.curCol)
				if !w.setPen(w.pen) {
					return false
				}
				w.out = append(w.out, seq...)
			}
			switch {
			case up && w.curRow == w.bot, !up && w.curRow == w.top:
				w.scroll(up)
			case up && w.curRow < w.high-1:
				w.curRow++
			case !up && w.curRow > 0:
				w.curRow--
			}
			w.aRow = w.curRow
		default:
			return false
		}
	}
	if whole {
		if visSet {
			w.visible, w.visKnown = vis, true
		}
		return true
	}
	// A frame leaves the cursor where it last put it and the pen as its last line left it. A frame whose last row no cursor address
	// follows leaves the cursor where that row's line ended: such a row was written whole, as the frame wrote it (row), so the
	// terminal's cursor is there, wherever that is; the writer knows of it only that it does not know it.
	if w.curKnown {
		w.moveTo(w.curRow, w.curCol)
	} else if w.aCurKnown {
		return false
	}
	if w.penKnown && !w.setPen(w.pen) {
		return false
	}
	if visSet && (!w.visKnown || vis != w.visible) {
		seq := "\x1b[?25l"
		if vis {
			seq = "\x1b[?25h"
		}
		w.out = append(w.out, seq...)
		w.visible, w.visKnown = vis, true
	}
	return true
}

// row takes the frame's write of a row: erased at column 1, then line. positioned is whether the frame moves the cursor next. It
// reports false when the row cannot be written under the pen the frame has there. While the frame's pen is not known the terminal's
// is the frame's all the same: every row is then written as the frame wrote it, until a line ends with a reset.
func (w *Writer) row(index int, line string, whole, positioned bool) bool {
	r := &w.rows[index]
	w.curKnown = false
	clean := w.penKnown && w.pen == pen{}
	// What the row shows decides how it is written only here: under the default pen, in a frame that is not sent as it came, with
	// the cursor moved after the row. A line kept unread is read now.
	inPlace := clean && positioned && !whole
	if inPlace && r.lineOnly {
		w.readRow(r)
	}
	if inPlace && r.known && string(r.line) == line {
		// The row shows the line already. The frame rewrote it whole all the same: the settle write does.
		r.partial, w.anyPartial = true, true
		w.pen = r.endPen
		return true
	}
	t := &w.scratch
	shared, same, plain := 0, 0, false
	if clean {
		if len(line) > w.room {
			w.room = len(line) + len(line)/2
		}
		// A row that is written whole whatever cells the line has keeps the line unread: a row of a frame that is sent as it came,
		// a row no cursor address follows, a row that is not known, and a row without a cell written with a line as wide as the
		// screen (so many printable ASCII bytes: read, it has that many columns, and is not written in place; not read, it is
		// written as the frame wrote it). The line ends with a reset, so the pen after it is the default one, read or not.
		if resetEnds(line) && (!inPlace || !r.known || len(r.cells) == 0 && asciiColumns(line) >= w.cols) {
			w.keepLine(index, r, line, whole)
			return true
		}
		// A line another row shows (the renderers moved it) has that row's cells: they are taken from it, and say nothing of what
		// the line shares with the row it is written to.
		moved := w.shows(line, index)
		if moved == nil {
			moved = r
		}
		if shared, same, plain = t.parse(line, moved, w.cols, w.room, &w.symbols); moved != r {
			shared, same = 0, 0
		}
	}
	switch {
	case whole && !plain:
		r.known, r.partial, r.lineOnly = false, false, false
		w.penAfter(line)
	case !plain:
		// A line the writer does not read is written as the frame wrote it, under the frame's pen, and the row is not known after it.
		w.moveTo(index, 0)
		if w.penKnown && !w.setPen(w.pen) {
			return false
		}
		w.out = append(w.out, "\x1b[2K"...)
		w.out = append(w.out, line...)
		w.aCurKnown = false
		r.known, r.partial, r.lineOnly = false, false, false
		w.penAfter(line)
		if w.penKnown {
			w.aPen = w.pen
		}
	default:
		// A frame that erases the screen is sent as it came: nothing is written for its rows. A row another frame rewrote and the
		// writer did not write whole waits for the settle write, whether or not a part of it was written.
		partial := false
		if !whole {
			if partial = r.known && positioned && w.writeDiff(index, r, t, shared, same); !partial {
				w.writeWhole(index, t)
			}
		}
		*r, *t = *t, *r
		r.known, r.partial, r.lineOnly = true, partial, false
		w.anyPartial = w.anyPartial || partial
		w.pen = r.endPen
	}
	return true
}

// keepLine takes a row that is written whole whatever it shows, with a line that ends with a reset: unless the frame is sent as it
// came the row is written as the frame wrote it, erased under the default pen, then the line. The line is kept in the row unread:
// the row's cells are read when a frame next writes the row in a way that depends on them (readRow), and never when the row is
// scrolled away, erased or forgotten first.
func (w *Writer) keepLine(index int, r *row, line string, whole bool) {
	if !whole {
		w.moveTo(index, 0)
		w.setPen(pen{})
		w.out = append(w.out, "\x1b[2K"...)
		w.out = append(w.out, line...)
		w.aCurKnown = false
		w.aPen = pen{}
	}
	if cap(r.line) < len(line) {
		r.line = make([]byte, 0, max(len(line), w.room))
	}
	r.line = append(r.line[:0], line...)
	r.known, r.partial, r.lineOnly = false, false, true
	w.pen = pen{}
}

// readRow reads the line a row keeps unread into the row's cells. The row is then what it would be had the line been read when the
// frame wrote it: known when the writer takes the line, not known otherwise.
func (w *Writer) readRow(r *row) {
	line := unsafe.String(unsafe.SliceData(r.line), len(r.line)) //nolint:gosec // G103: the row's own line, read and not written while its cells are read.
	_, _, ok := r.readLine(line, nil, w.cols, w.room, &w.symbols)
	r.known, r.lineOnly = ok, false
}

// penAfter is the pen after a line the writer did not read: the default one when the line ends with a reset, unknown otherwise.
func (w *Writer) penAfter(line string) {
	if resetEnds(line) {
		w.pen, w.penKnown = pen{}, true
		return
	}
	if line != "" {
		w.penKnown = false
	}
}

// arm makes the settle timer fire after d, unless it is waiting already or the writer was stopped.
func (w *Writer) arm(d time.Duration) {
	if w.armed || w.stopped {
		return
	}
	w.armed = true
	if w.timer == nil {
		w.timer = time.AfterFunc(d, w.settleFire)
		return
	}
	w.timer.Reset(d)
}

// Stop ends the settle timer: when it returns the timer is cancelled, a settle write already under way has been written, and none
// comes later. Call it without the terminal's output lock, before the terminal's output is released. A stopped writer still
// translates; only its settle write is gone.
func (w *Writer) Stop() {
	if w.lock != nil {
		w.lock()
		defer w.unlock()
	}
	w.stopTimer()
}

// stopTimer cancels the settle timer for good. The terminal's output lock is held.
func (w *Writer) stopTimer() {
	w.stopped = true
	w.armed = false
	if w.timer != nil {
		w.timer.Stop()
	}
}

// Drop ends the writer for good: when it returns every later write is sent as it came, Active is false, and no settle write comes
// (one already under way has been written). The terminal then gets the renderers' bytes as a terminal without a writer does. Call
// it without the terminal's output lock.
func (w *Writer) Drop() {
	if w.lock != nil {
		w.lock()
		defer w.unlock()
	}
	w.dropped.Store(true)
	w.active.Store(false)
	w.stopTimer()
}

// settleFire is the settle timer: it writes the settle write once no frame was written for the settle time.
func (w *Writer) settleFire() {
	w.lock()
	defer w.unlock()
	w.armed = false
	if w.stopped || !w.anyPartial || !w.active.Load() {
		return
	}
	if since := time.Since(w.lastWrite); since < w.settle {
		w.arm(w.settle - since)
		return
	}
	if out := w.Settled(); out != "" {
		w.emit(out)
	}
}

// Settled is the settle write: every row that had a part written since it was last written whole, written whole as the frame wrote
// it, in one synchronized update that leaves the cursor where it is. It is empty when no row is waiting for it, when the terminal
// changed size since the last frame, or when the cursor is not known or the pen is not the default one; the rows then wait for the
// frame that next writes them.
func (w *Writer) Settled() string {
	w.catchUp()
	if cols, rows := w.size(); cols != w.cols || rows != w.high {
		// The terminal changed size since the last frame: the rows kept are not what it shows, and rows of the old width written now
		// would wrap. The repaint the resize brings is the next frame.
		w.resize()
		return ""
	}
	if !w.anyPartial || !w.active.Load() || !w.curKnown || !w.penKnown || w.pen != (pen{}) {
		return ""
	}
	w.anyPartial = false
	w.out = append(w.out[:0], syncBegin...)
	w.aPen, w.aCurKnown, w.aRow, w.aCol = w.pen, true, w.curRow, w.curCol
	for index := range w.rows {
		if r := &w.rows[index]; r.known && r.partial {
			r.partial = false
			w.writeWhole(index, r)
		}
	}
	if len(w.out) == len(syncBegin) {
		return ""
	}
	w.moveTo(w.curRow, w.curCol)
	w.setPen(pen{})
	w.out = append(w.out, syncEnd...)
	out := unsafe.String(unsafe.SliceData(w.out), len(w.out)) //nolint:gosec // G103: the writer's own buffer as the result, valid until the next call, without a copy.
	w.stats.Settles++
	w.stats.BytesOut += len(out)
	if w.record != nil {
		w.record(RecordSettle, "", out)
	}
	return out
}
