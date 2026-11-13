// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// readAtOnce reads every line w keeps unread: the writer is then one that read each line when the frame wrote it.
func readAtOnce(w *Writer) {
	for i := range w.rows {
		if w.rows[i].lineOnly {
			w.readRow(&w.rows[i])
		}
	}
}

// sameRows reports how the rows of later, with every line it keeps unread read in a copy, differ from the rows of now.
func sameRows(later, now *Writer) string {
	if len(later.rows) != len(now.rows) {
		return fmt.Sprintf("%d rows and %d", len(later.rows), len(now.rows))
	}
	for i := range later.rows {
		a, b := later.rows[i], &now.rows[i]
		if a.lineOnly {
			a = row{line: slices.Clone(a.line), lineOnly: true}
			later.readRow(&a)
		}
		if a.known != b.known || a.partial != b.partial {
			return fmt.Sprintf("row %d: known %v, partial %v; read at once: known %v, partial %v", i, a.known, a.partial, b.known, b.partial)
		}
		if !a.known {
			continue
		}
		if !bytes.Equal(a.line, b.line) || !slices.Equal(a.cells, b.cells) || !slices.Equal(a.pens, b.pens) || a.width != b.width || a.endPen != b.endPen || a.uncertain != b.uncertain || a.inked != b.inked {
			return fmt.Sprintf("row %d: the line %q read later is not the line %q read at once, cell for cell", i, a.line, b.line)
		}
	}
	return ""
}

// A row written whole whatever its cells are keeps its line unread, and the line is read when a later frame's write of the row
// depends on the cells. That changes nothing a terminal is sent: a writer that reads every such line at once sends the same bytes
// for every frame and every settle write, and keeps the same rows. Shown on the recorded streams and on random frames: rows as
// wide as the screen and narrower, lines the writer does not take, rows written again with the same line, with another line, with
// one more character; a scroll region that moves unread rows; the screen erased; another screen size; a frame that holds an image;
// a frame whose last row no cursor address follows; a line that ends under a pen. The random frames also leave the screens the
// renderers' bytes leave.
func TestALineReadLaterIsTheLineReadAtOnce(t *testing.T) {
	frames, unread, readLater, neverRead := 0, 0, 0, 0
	// step gives both writers a write (or the settle write, for ""), compares what they send and what they keep, and counts the
	// lines kept unread, read by a later frame, and gone without having been read.
	step := func(name string, later, now *Writer, pair *gridterm.Pair, in string) {
		t.Helper()
		before := make([]bool, len(later.rows))
		for i := range later.rows {
			before[i] = later.rows[i].lineOnly
		}
		var a, b string
		if in == "" {
			a, b = later.Settled(), now.Settled()
		} else {
			a, b = later.Translate(in), now.Translate(in)
		}
		if a != b {
			t.Fatalf("%s: for %q\n  lines read later: %q\n  lines read at once: %q", name, in, a, b)
		}
		if pair != nil {
			if d := pair.Step(in, a); d != "" {
				t.Fatalf("%s: %s\n  frame  %q\n  writer %q", name, d, in, a)
			}
		}
		readAtOnce(now)
		if d := sameRows(later, now); d != "" {
			t.Fatalf("%s: after %q: %s", name, in, d)
		}
		frames++
		moved := strings.Contains(in, "r\x1b[") || strings.Contains(in, "\x1b[2J")
		for i := range later.rows {
			r := &later.rows[i]
			switch {
			case r.lineOnly:
				unread++
			case i < len(before) && before[i] && r.known && !moved:
				readLater++
			case i < len(before) && before[i]:
				neverRead++
			}
		}
	}
	for name, stream := range testStreams(t) {
		later, now := newTestWriter(160, 48), newTestWriter(160, 48)
		for i, write := range gridterm.SplitWrites(stream) {
			step(name, later, now, nil, write)
			if i%7 == 6 {
				step(name, later, now, nil, "")
			}
		}
	}
	type piece struct {
		text  string
		width int
	}
	pieces := []piece{{"a", 1}, {"bc", 2}, {"def", 3}, {" ", 1}, {"  ", 2}, {"x", 1}, {"é", 1}, {"\u2500", 1}, {"\u65e5", 2},
		{"\x1b[1m", 0}, {"\x1b[22m", 0}, {"\x1b[31m", 0}, {"\x1b[39m", 0}, {"\x1b[48;5;237m", 0}, {"\x1b[49m", 0}, {"\x1b[0m", 0}}
	const kittyImage = "\x1b_Ga=T,f=100,q=2,C=1,c=3,r=2,i=7;AAAA\x1b\\"
	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 21))
		cols, rows := 16+4*rng.IntN(4), 5+rng.IntN(3)
		size := func() (int, int) { return cols, rows }
		later, now := New(Options{Size: size, Settle: -1}), New(Options{Size: size, Settle: -1})
		name := fmt.Sprintf("seed %d", seed)
		pair := gridterm.NewPair(cols, rows)
		for _, write := range gridterm.SplitWrites(started) {
			step(name, later, now, pair, write)
		}
		// texts are the rows' lines; bare is whether a row's line ends without the renderers' reset, under the pen it set.
		texts, bare := make([]string, rows), make([]bool, rows)
		emit := func(r int) string {
			if bare[r] {
				return at(r+1, 1) + "\x1b[2K" + texts[r]
			}
			return line(r+1, texts[r])
		}
		// text is a row's line: pieces, then mostly blanks through the screen's edge; now and then a line the writer does not take.
		text := func() string {
			var b strings.Builder
			width := 0
			for range rng.IntN(8) {
				if p := pieces[rng.IntN(len(pieces))]; width+p.width <= cols {
					b.WriteString(p.text)
					width += p.width
				}
			}
			switch k := rng.IntN(12); {
			case k == 0:
				b.WriteString("e\u0301")
			case k == 1:
				b.WriteString("\x1b]8;;http://a\x07a")
			case k > 3:
				b.WriteString(strings.Repeat(" ", cols-width))
			}
			return b.String()
		}
		for range 120 {
			var parts []string
			switch k := rng.IntN(60); {
			case k < 2:
				cols, rows = 16+4*rng.IntN(4), 5+rng.IntN(3)
				pair.Resize(cols, rows)
				texts, bare = make([]string, rows), make([]bool, rows)
				fallthrough
			case k < 5:
				parts = append(parts, "\x1b[2J")
				for r := range texts {
					texts[r], bare[r] = text(), false
					parts = append(parts, emit(r))
				}
			case k < 8:
				r := rng.IntN(rows)
				texts[r], bare[r] = strings.Repeat(" ", rng.IntN(cols-3))+kittyImage, false
				parts = append(parts, emit(r))
			case k < 20:
				first := rng.IntN(rows - 1)
				bottom := first + 1 + rng.IntN(rows-1-first)
				region := fmt.Sprintf("\x1b[%d;%dr", first+1, bottom+1)
				if rng.IntN(2) == 0 {
					parts = append(parts, region+at(bottom+1, 1)+"\n\x1b[r")
					copy(texts[first:bottom], texts[first+1:bottom+1])
					copy(bare[first:bottom], bare[first+1:bottom+1])
					texts[bottom], bare[bottom] = "", false
				} else {
					parts = append(parts, region+at(first+1, 1)+"\x1bM\x1b[r")
					copy(texts[first+1:bottom+1], texts[first:bottom])
					copy(bare[first+1:bottom+1], bare[first:bottom])
					texts[first], bare[first] = "", false
				}
			}
			if len(parts) == 0 || rng.IntN(2) == 0 {
				for range 1 + rng.IntN(3) {
					r := rng.IntN(rows)
					switch k := rng.IntN(10); {
					case k < 2:
					case k < 5 && len(texts[r]) > 0 && !strings.Contains(texts[r], "\x1b_"):
						texts[r] = "x" + texts[r][1:]
						if texts[r][1:] == "" || texts[r][0] == 0x1b {
							texts[r], bare[r] = text(), false
						}
					case k == 9:
						// A line that ends under the pen it set: the rows after it in the frame are erased under that pen.
						texts[r], bare[r] = text()+"\x1b[44mz", true
					default:
						texts[r], bare[r] = text(), false
					}
					parts = append(parts, emit(r))
				}
			}
			// Most frames end with the cursor's address, as the renderers' do; some end with their last row.
			in := syncBegin + strings.Join(parts, "") + syncEnd
			if rng.IntN(10) > 0 {
				in = frame(parts...)
			}
			step(name, later, now, pair, in)
			if rng.IntN(8) == 0 {
				step(name, later, now, pair, "")
			}
		}
	}
	t.Logf("%d writes: %d times a row kept its line unread after a write, %d lines were read by a later frame, %d were gone unread", frames, unread, readLater, neverRead)
	if unread < 100000 || readLater < 3000 || neverRead < 6000 {
		t.Errorf("%d writes: %d times a row kept its line unread after a write, %d lines were read by a later frame, %d were gone unread: the frames do not exercise the lines kept unread", frames, unread, readLater, neverRead)
	}
}

// asciiColumns counts the printable ASCII bytes outside a line's sequences, and a line the writer reads has no fewer columns.
func TestASCIIColumns(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	read := 0
	for range 30000 {
		line := randomLine(rng, rng.IntN(12))
		if rng.IntN(3) == 0 {
			line += strings.Repeat(" ", rng.IntN(30))
		}
		line = line[:lineEnd(line, 0)]
		want := 0
		for i := 0; i < len(line); {
			kind, end := nextToken(line, i)
			if kind == tokenText {
				for _, c := range []byte(line[i:end]) {
					if c < 0x80 {
						want++
					}
				}
			}
			i = end
		}
		if got := asciiColumns(line); got != want {
			t.Fatalf("asciiColumns(%q) = %d, want %d", line, got, want)
		}
		if _, width, _, ok := referenceParse(line, 400); ok {
			read++
			if width < want {
				t.Fatalf("%q has %d columns and %d printable ASCII bytes outside its sequences", line, width, want)
			}
		}
	}
	if read < 10000 {
		t.Errorf("%d of the lines are lines the writer reads: too few", read)
	}
}

// A row without a cell is written whole, its line kept unread, by a line of as many printable ASCII bytes as the screen has
// columns, and is written as it was before by every other line: in place by one narrower than the screen, whole after its cells
// were read by one as wide as the screen that is not all ASCII.
func TestARowWithoutACellKeepsOnlyALineAsWideAsTheScreenUnread(t *testing.T) {
	const cols = 20
	for _, c := range []struct {
		name, text     string
		erased, unread bool
	}{
		{"as wide as the screen, ASCII", padded("new", cols), true, true},
		{"as wide as the screen, with a wide character", "a\u65e5" + strings.Repeat(" ", cols-3), true, false},
		{"narrower than the screen", "new", false, false},
	} {
		w := newTestWriter(cols, 5)
		pair := newTestPair(cols, 5)
		in := frame(line(3, c.text))
		out := w.Translate(in)
		if d := pair.Step(in, out); d != "" {
			t.Fatalf("%s: %s", c.name, d)
		}
		if erased := strings.Contains(out, "\x1b[2K"); erased != c.erased || w.rows[2].lineOnly != c.unread {
			t.Errorf("%s: the row was erased %v and keeps its line unread %v, want %v and %v: %q", c.name, erased, w.rows[2].lineOnly, c.erased, c.unread, out)
		}
		// The next frame changes one cell of the row: the cells are read then, and the row is written in place.
		in = frame(line(3, "N"+c.text[1:]))
		out = w.Translate(in)
		if d := pair.Step(in, out); d != "" {
			t.Fatalf("%s, a cell changed: %s", c.name, d)
		}
		if strings.Contains(out, "\x1b[2K") || w.rows[2].lineOnly || !w.rows[2].known {
			t.Errorf("%s: with a cell changed the row was not written in place from its cells: %q", c.name, out)
		}
	}
}
