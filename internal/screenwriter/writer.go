// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

// Package screenwriter translates the bytes the terminal renderers write into fewer bytes that leave the same screen (D624).
//
// A Writer sits under a terminal's write. It reads every write, keeps what the writes leave on the alternate screen (rows of cells, the
// pen, the cursor, the scroll region) and, for a frame it can read completely, sends only what changes that screen:
//
//   - a row the frame rewrites with the cells it already shows is not written;
//   - a row whose cells change is written from its first cell through its last changed one, as the first bytes of the frame's own
//     line (the first-cell rule); what follows on the row stays;
//   - a row that got shorter is written whole, as the frame wrote it: the line erased, then the row;
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
// Before the alternate screen is entered and after it is left every byte is sent as it came.
//
// pig additive (D624): tui.ProcessTerminal puts a Writer under its write only when PIG_TUI_SCREEN_WRITER=1 (tui/terminal_screen_writer.go).
package screenwriter

import (
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

// Active reports whether the writer translates frames now: the alternate screen is shown. It is one atomic load and may be called
// without the terminal's output lock.
func (w *Writer) Active() bool { return w.active.Load() }

const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
)

// Translate is what the terminal is sent for data, a write of a renderer or of anything else that writes through the terminal: data
// itself, the bytes that change the screen as data does, or nothing. The result is valid until the next call.
func (w *Writer) Translate(data string) string {
	if !w.active.Load() || len(data) < len(syncBegin)+len(syncEnd) || !strings.HasPrefix(data, syncBegin) || !strings.HasSuffix(data, syncEnd) {
		w.other(data)
		if w.record != nil {
			w.record(RecordOther, data, data)
		}
		return data
	}
	w.catchUp()
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
		w.forget()
		if strings.LastIndex(data, "\x1b[?1049l") > strings.LastIndex(data, "\x1b[?1049h") {
			w.active.Store(false)
		}
		w.stats.Unread++
	case whole:
		w.stats.Repaints++
	case len(w.out) == len(syncBegin):
		w.stats.Unchanged++
		out = ""
	default:
		w.stats.Translated++
		w.out = append(w.out, syncEnd...)
		out = unsafe.String(unsafe.SliceData(w.out), len(w.out))
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
	if _, cursor, forget := outside(data, true); forget || cursor != 0 {
		w.past.Store(true)
	}
}

// catchUp forgets the screen after a write past the writer.
func (w *Writer) catchUp() {
	if w.past.Load() && w.past.Swap(false) && w.active.Load() {
		w.resize()
		w.forget()
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
	screen, cursor, forget := outside(data, shown)
	if !screen {
		if shown {
			w.active.Store(false)
		}
		return
	}
	w.catchUp()
	w.active.Store(true)
	switch {
	case forget:
		w.resize()
		w.forget()
	case cursor != 0:
		w.visible, w.visKnown = cursor > 0, true
	}
}

// forget makes every row, the pen, the cursor and the scroll region unknown.
func (w *Writer) forget() {
	for i := range w.rows {
		w.rows[i].known, w.rows[i].partial = false, false
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

// writeDiff makes a row that shows o show t: the first bytes of t's line through its last changed cell. shared and same are how many
// of t's first and last cells are known to be o's (parse); 0 when not known. It reports false, having written nothing, when only
// writing the row whole does.
func (w *Writer) writeDiff(index int, o, t *row, shared, same int) bool {
	// A row that got shorter is written whole: the frame erased the line and wrote the row. Erasing from the row's new end instead
	// shows the same cells, but a terminal that frees a line's cells only when the line is erased whole (tmux) keeps the longer line.
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
	if p == no && p == nt {
		return true
	}
	s := 0
	if o.width == t.width {
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
		w.aCol = min(last.col()+last.width(), w.cols-1)
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
	clean := w.penKnown && w.pen == pen{}
	if up {
		first := w.rows[w.top]
		copy(w.rows[w.top:w.bot], w.rows[w.top+1:w.bot+1])
		w.rows[w.bot] = first
		w.rows[w.bot].blank(clean)
	} else {
		last := w.rows[w.bot]
		copy(w.rows[w.top+1:w.bot+1], w.rows[w.top:w.bot])
		w.rows[w.top] = last
		w.rows[w.top].blank(clean)
	}
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
		if vis {
			w.out = append(w.out, "\x1b[?25h"...)
		} else {
			w.out = append(w.out, "\x1b[?25l"...)
		}
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
	if clean && r.known && positioned && !whole && string(r.line) == line {
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
		if moved := w.shows(line, index); moved != nil {
			// The line is one another row shows (the renderers moved it): its cells are that row's.
			t.copyOf(moved, w.cols, w.room)
			plain = true
		} else {
			shared, same, plain = t.parse(line, r, w.cols, w.room, &w.symbols)
		}
	}
	switch {
	case whole && plain:
		w.rows[index], w.scratch = *t, *r
		w.rows[index].known, w.rows[index].partial = true, false
		w.pen = w.rows[index].endPen
	case whole:
		r.known, r.partial = false, false
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
		r.known, r.partial = false, false
		w.penAfter(line)
		if w.penKnown {
			w.aPen = w.pen
		}
	default:
		// A row the frame rewrote and the writer did not write whole waits for the settle write, whether or not a part of it was written.
		partial := r.known && positioned && w.writeDiff(index, r, t, shared, same)
		if !partial {
			w.writeWhole(index, t)
		}
		w.rows[index], w.scratch = *t, *r
		w.rows[index].known, w.rows[index].partial = true, partial
		w.anyPartial = w.anyPartial || partial
		w.pen = w.rows[index].endPen
	}
	return true
}

// penAfter is the pen after a line the writer did not read: the default one when the line ends with a reset, unknown otherwise.
func (w *Writer) penAfter(line string) {
	if strings.HasSuffix(line, "\x1b[0m"+linkCloseBEL) || strings.HasSuffix(line, "\x1b[0m") {
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
	w.stopped = true
	w.armed = false
	if w.timer != nil {
		w.timer.Stop()
	}
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
	out := unsafe.String(unsafe.SliceData(w.out), len(w.out))
	w.stats.Settles++
	w.stats.BytesOut += len(out)
	if w.record != nil {
		w.record(RecordSettle, "", out)
	}
	return out
}
