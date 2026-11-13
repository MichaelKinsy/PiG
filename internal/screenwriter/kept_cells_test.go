// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// writtenInPlace is what writeDiff writes for a row that shows before and is given after, and whether it wrote the row in place:
// with what parse says the two lines share (hints), or with nothing said, so that writeDiff compares every cell itself.
func writtenInPlace(t *testing.T, before, after string, cols int, hints bool, symbols *symbolWidths) (out string, inPlace, plain bool) {
	t.Helper()
	o, n := &row{}, &row{}
	if _, _, ok := o.parse(before, nil, cols, 0, symbols); !ok {
		return "", false, false
	}
	o.known = true
	old := o
	if !hints {
		old = nil
	}
	shared, same, ok := n.parse(after, old, cols, 0, symbols)
	if !ok {
		return "", false, false
	}
	w := newTestWriter(cols, 4)
	w.out = w.out[:0]
	w.aPen, w.aCurKnown = pen{}, false
	inPlace = w.writeDiff(1, o, n, shared, same)
	for name, r := range map[string]*row{"the row": o, "the line": n} {
		if r.inked != r.inkBefore(len(r.cells)) {
			t.Fatalf("%s of %q then %q (hints %v): %d cells counted through the last that is not a default blank, the cells say %d", name, before, after, hints, r.inked, r.inkBefore(len(r.cells)))
		}
	}
	return string(w.out), inPlace, true
}

// What parse says a line shares with the row's line spares writeDiff the cells it need not compare again, and changes nothing of
// what is written: for the lines of the recorded streams, each after the line its row held before, and for random rows as wide as
// the screen and narrower whose text changes at its start, in its middle and at its end, writeDiff writes the same bytes, in place
// or not, as it writes when it compares every cell.
func TestWhatParseKeptOfARowChangesNothingWritten(t *testing.T) {
	var symbols symbolWidths
	pairs, inPlace, spared := 0, 0, 0
	check := func(name, before, after string, cols int) {
		t.Helper()
		got, gotIn, ok := writtenInPlace(t, before, after, cols, true, &symbols)
		want, wantIn, wantOK := writtenInPlace(t, before, after, cols, false, &symbols)
		if ok != wantOK {
			t.Fatalf("%s: %q then %q: the line is read %v with the row's line beside it and %v alone", name, before, after, ok, wantOK)
		}
		if !ok {
			return
		}
		pairs++
		if gotIn {
			inPlace++
		}
		if got != want || gotIn != wantIn {
			t.Fatalf("%s: %q then %q on %d columns:\n  with what parse kept: in place %v, %q\n  every cell compared:  in place %v, %q", name, before, after, cols, gotIn, got, wantIn, want)
		}
		o, n := &row{}, &row{}
		o.parse(before, nil, cols, 0, &symbols)
		o.known = true
		if _, same, _ := n.parse(after, o, cols, 0, &symbols); same > 0 && len(o.cells) == len(n.cells) {
			spared++
		}
	}
	for name, stream := range testStreams(t) {
		rows := map[string]string{}
		for _, write := range gridterm.SplitWrites(stream) {
			for i := 0; i < len(write); {
				kind, end := nextToken(write, i)
				seq := write[i:end]
				i = end
				if kind != tokenCSI || seq != "\x1b[2K" {
					continue
				}
				address := write[strings.LastIndex(write[:i-len(seq)], "\x1b[")+2 : i-len(seq)]
				j := lineEnd(write, i)
				if before, held := rows[address]; held && before != write[i:j] {
					check(name, before, write[i:j], 160)
				}
				rows[address] = write[i:j]
				i = j
			}
		}
	}
	type piece struct {
		text  string
		width int
	}
	pieces := []piece{{"a", 1}, {"bc", 2}, {"def", 3}, {" ", 1}, {"  ", 2}, {"x", 1}, {"é", 1}, {"\u2500", 1}, {"\u00b1", 1}, {"\u65e5", 2},
		{"\x1b[1m", 0}, {"\x1b[22m", 0}, {"\x1b[31m", 0}, {"\x1b[39m", 0}, {"\x1b[48;5;237m", 0}, {"\x1b[49m", 0}, {"\x1b[0m", 0}}
	rng := rand.New(rand.NewPCG(11, 12))
	text := func(n int) (string, int) {
		var b strings.Builder
		width := 0
		for range n {
			p := pieces[rng.IntN(len(pieces))]
			b.WriteString(p.text)
			width += p.width
		}
		return b.String(), width
	}
	for range 60000 {
		cols := []int{12, 24, 40}[rng.IntN(3)]
		head, headWidth := text(rng.IntN(5))
		tail, tailWidth := text(rng.IntN(5))
		// A row is its text, then, for most rows, blanks through the screen's edge, under the pen the text left or after a reset.
		line := func(middle string, middleWidth int) string {
			s := head + middle + tail
			if rng.IntN(3) == 0 {
				s += "\x1b[0m"
			}
			if used := headWidth + middleWidth + tailWidth; used < cols && rng.IntN(5) > 0 {
				s += strings.Repeat(" ", cols-used)
			}
			return s + reset
		}
		middle, middleWidth := text(rng.IntN(4))
		before := line(middle, middleWidth)
		for range 4 {
			other, otherWidth := text(rng.IntN(4))
			check("random", before, line(other, otherWidth), cols)
		}
		check("random", before, head+middle+reset, cols)
	}
	if pairs < 50000 || inPlace < pairs/20 || spared < pairs/10 {
		t.Errorf("of %d pairs of lines %d were written in place and %d kept cells of the same index at their end: the lines do not exercise what parse keeps", pairs, inPlace, spared)
	}
}
