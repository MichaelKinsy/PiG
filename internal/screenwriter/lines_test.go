// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"compress/gzip"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// rowInPlace is a row the writer wrote in place: the address of its first column, then text and no erase.
var rowInPlace = regexp.MustCompile(`\x1b\[\d+;1H(?:\x1b\[0m)?[^\x1b]`)

// The run of parity scenario oauth/28 (a login cancelled, back to the menu; 120 by 35 cells) that failed under the writer as it
// was: the frame that returns to the menu rewrote three padded rows with shorter text, the writer wrote them in place because their
// padded widths were equal, and tmux kept the longer lines, so capture-pane -e ended the rows' pens elsewhere. The log holds every
// write of that run and what the writer sent for it then. The screens see the longer lines in what was sent then, and no
// difference in what the writer sends now.
func TestRecordedMenuReturnLeavesTheRenderersLines(t *testing.T) {
	file, err := os.Open("testdata/oauth-menu-return.rec.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	log, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	const cols, rows = 120, 35
	w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
	then, now := gridterm.NewPair(cols, rows), gridterm.NewPair(cols, rows)
	var seen []string
	records := 0
	for ; len(log) > 0; records++ {
		kind, in, out, rest, ok := NextRecord(log)
		if !ok {
			t.Fatalf("record %d is cut short", records)
		}
		log = rest
		if d := then.Step(string(in), string(out)); d != "" {
			seen = append(seen, d)
		}
		var sent string
		if kind == RecordSettle {
			sent = w.Settled()
		} else {
			sent = w.Translate(string(in))
		}
		if d := now.Step(string(in), sent); d != "" {
			t.Errorf("record %d: %s\n  frame  %q\n  writer %q", records, d, in, sent)
		}
	}
	t.Logf("of what the writer sent then the screens say: %q", seen)
	if records != 21 {
		t.Errorf("%d records, want the run's 21", records)
	}
	if !slices.ContainsFunc(seen, func(d string) bool { return strings.Contains(d, "tmux keeps a line of 120 cells, not of 30") }) {
		t.Errorf("of what the writer sent then the screens say %q: they do not see the longer lines tmux kept", seen)
	}
}

// Random frames as the renderers write them leave the same screens and the same lines with and without the writer, after every
// write and on every width model. Text rows are padded with blanks to the screen's width, after a reset or under the text's last
// pen; rows without text are empty; rows grow, lose their end, change a cell for one as wide or a pen for another, get text before
// their first cell, become blanks or empty; a region scrolls up or down; the screen is repainted, at the same size or another; the
// settle write comes. The screens are narrow, so that text crosses a quarter and a half of the width, where tmux's lines step,
// all the time.
func TestRandomFramesLeaveTheRenderersLines(t *testing.T) {
	type piece struct {
		text  string
		width int
	}
	pieces := []piece{{"a", 1}, {"bc", 2}, {"def", 3}, {" ", 1}, {"  ", 2}, {"x", 1}, {"é", 1}, {"\u2500", 1}, {"\u65e5", 2},
		{"\x1b[1m", 0}, {"\x1b[22m", 0}, {"\x1b[31m", 0}, {"\x1b[39m", 0}, {"\x1b[48;5;237m", 0}, {"\x1b[49m", 0}, {"\x1b[0m", 0}}
	width := func(text []piece) int {
		n := 0
		for _, p := range text {
			n += p.width
		}
		return n
	}
	frames, inPlace, whole, failed := 0, 0, 0, 0
	for seed := range uint64(300) {
		rng := rand.New(rand.NewPCG(seed, 11))
		cols, rows := 16+4*rng.IntN(4), 5+rng.IntN(3)
		w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
		pair := gridterm.NewPair(cols, rows)
		for _, write := range gridterm.SplitWrites(started) {
			pair.Step(write, w.Translate(write))
		}
		// texts are the rows' texts, nil for a row without one; resetFirst is whether a row's padding follows a reset.
		texts, resetFirst := make([][]piece, rows), make([]bool, rows)
		render := func(r int) string {
			if texts[r] == nil {
				return line(r+1, "")
			}
			var b strings.Builder
			for _, p := range texts[r] {
				b.WriteString(p.text)
			}
			if resetFirst[r] {
				b.WriteString("\x1b[0m")
			}
			b.WriteString(strings.Repeat(" ", cols-width(texts[r])))
			return line(r+1, b.String())
		}
		for step := 0; step < 100 && failed < 10; step++ {
			var parts []string
			switch k := rng.IntN(40); {
			case k < 2:
				if k == 0 {
					cols, rows = 16+4*rng.IntN(4), 5+rng.IntN(3)
					pair.Resize(cols, rows)
					texts, resetFirst = make([][]piece, rows), make([]bool, rows)
				}
				parts = append(parts, "\x1b[2J")
				for r := range rows {
					parts = append(parts, render(r))
				}
			case k < 7:
				top := rng.IntN(rows - 1)
				bottom := top + 1 + rng.IntN(rows-1-top)
				region := fmt.Sprintf("\x1b[%d;%dr", top+1, bottom+1)
				if rng.IntN(2) == 0 {
					parts = append(parts, region+at(bottom+1, 1)+"\n\x1b[r")
					copy(texts[top:bottom], texts[top+1:bottom+1])
					copy(resetFirst[top:bottom], resetFirst[top+1:bottom+1])
					texts[bottom] = nil
				} else {
					parts = append(parts, region+at(top+1, 1)+"\x1bM\x1b[r")
					copy(texts[top+1:bottom+1], texts[top:bottom])
					copy(resetFirst[top+1:bottom+1], resetFirst[top:bottom])
					texts[top] = nil
				}
			}
			for range 1 + rng.IntN(3) {
				r := rng.IntN(rows)
				text := texts[r]
				switch k := rng.IntN(14); {
				case text == nil:
					texts[r], resetFirst[r] = []piece{pieces[rng.IntN(len(pieces))]}, rng.IntN(2) == 0
				case k == 0:
					texts[r] = nil
				case k == 1 || len(text) == 0:
					texts[r] = []piece{{" ", 1}}
				case k == 2:
					texts[r] = text[:rng.IntN(len(text))]
				case k == 3:
					if p := pieces[rng.IntN(len(pieces))]; width(text)+p.width <= cols {
						texts[r] = append([]piece{p}, text...)
					}
				case k < 7:
					i := rng.IntN(len(text))
					for {
						if p := pieces[rng.IntN(len(pieces))]; p.width == text[i].width {
							text[i] = p
							break
						}
					}
				case k == 7:
				default:
					for range 1 + rng.IntN(3) {
						if p := pieces[rng.IntN(len(pieces))]; width(texts[r])+p.width <= cols {
							texts[r] = append(texts[r], p)
						}
					}
				}
				parts = append(parts, render(r))
			}
			parts = append(parts, at(1+rng.IntN(rows), 1+rng.IntN(cols)))
			if rng.IntN(2) == 0 {
				parts = append(parts, "\x1b[?25h")
			} else {
				parts = append(parts, "\x1b[?25l")
			}
			in := syncBegin + strings.Join(parts, "") + syncEnd
			out := w.Translate(in)
			frames++
			if out != in {
				inPlace += len(rowInPlace.FindAllString(out, -1))
				whole += strings.Count(out, "\x1b[2K")
			}
			if d := pair.Step(in, out); d != "" {
				t.Errorf("seed %d step %d, %d by %d: %s\n  frame  %q\n  writer %q", seed, step, cols, rows, d, in, out)
				failed++
				break
			}
			if rng.IntN(8) == 0 {
				if d := pair.Step("", w.Settled()); d != "" {
					t.Errorf("seed %d step %d, %d by %d: the settle write changed the screen: %s", seed, step, cols, rows, d)
					failed++
					break
				}
			}
		}
	}
	if inPlace < frames/10 || whole < frames/10 {
		t.Errorf("of %d frames' rows the writer wrote %d in place and %d whole: the frames do not exercise both", frames, inPlace, whole)
	}
}

// A cluster whose width terminals disagree on keeps its place in a row that is written in place. To the writer a cluster is of
// certain width when it is one code point that is printable ASCII, a Braille pattern (U+2800 to U+28FF), or a letter (General
// Category L) from U+00A1 to U+052F whose East Asian Width is Neutral or Narrow (certainWidth). Every other cluster a row may hold
// is of uncertain width: the characters of East Asian Width Ambiguous, Wide, Fullwidth and Halfwidth, every character above U+007E
// that is not a letter (but the Braille patterns), and every letter above U+052F. With another such cluster where one was, the
// cells after it lie in other columns in a terminal that draws it wider than counted, and tmux's line, which steps with the
// columns, is not the one the frame's erase and line leave.
//
// This is shown on the screens' width models, not on a real terminal. The case is a character of East Asian Width Ambiguous
// (U+00B1) that one of the models draws two columns wide, on a screen of 8 columns: there the second row's line has 8 cells when
// the row is written whole, and 4 when it is written over the first row.
func TestAClusterOfUncertainWidthKeepsItsPlace(t *testing.T) {
	w, pair := newTestWriter(8, 4), newTestPair(8, 4)
	for _, write := range []string{frame(line(2, "\u00b1 a")), frame(line(2, "x \u00b1")), frame(line(2, "x \u00b1!"))} {
		out := w.Translate(write)
		if d := pair.Step(write, out); d != "" {
			t.Errorf("after %q, sent as %q: %s", write, out, d)
		}
	}
}

// Every other way a row changes keeps the rule for writing it in place, or makes the writer forget the row: after each of them
// the next frames leave the lines the renderers' bytes leave, and a forgotten row is written whole. A row known to be blank, after
// a repaint or scrolled in, is written in place: the frame's erase of it frees nothing. A row written while the pen is not known
// (the first after another size or after the alternate screen is entered again) is written as the frame wrote it and stays unknown.
func TestEveryChangeOfARowKeepsItsLineOrForgetsTheRow(t *testing.T) {
	const erase = "\x1b[2K"
	// pad is a text row as the renderers write it: the text, and blanks to the screen's width.
	pad := func(text string, cols int) string { return text + strings.Repeat(" ", cols-len(text)) }
	type step struct {
		write string // a write, "settle" for the settle write, or "resize" for a screen of 30 by 6
		whole int    // the rows the writer writes whole for it; -1 when the write is sent as it came
	}
	for _, c := range []struct {
		name  string
		steps []step
	}{
		{"scroll", []step{
			{frame(line(2, pad("one", 20)), line(3, pad("a long line here", 20)), line(5, pad("x", 20))), 0},
			// The region scrolls up: the lines move with the rows, and the row scrolled in is a blank one.
			{frame("\x1b[2;5r"+at(5, 1)+"\n\x1b[r", line(5, pad("new", 20))), 0},
			{frame(line(2, pad("a long line here!", 20))), 0},
			{frame(line(4, pad("xy", 20))), 0},
			{frame(line(2, pad("a", 20))), 1},
			{frame("\x1b[2;5r"+at(2, 1)+"\x1bM\x1b[r", line(2, pad("top", 20))), 0},
			{frame(line(3, pad("ab", 20))), 0},
			{frame(line(5, pad("xyz", 20)), line(2, pad("t", 20))), 1},
		}},
		{"erase under a pen", []step{
			{frame(line(2, pad("two", 20)), line(3, pad("three", 20))), 0},
			// Row 2's line ends under a background: row 3 is erased under it, and the writer does not know the row any more.
			{frame(at(2, 1)+erase+"\x1b[44mblue", line(3, pad("three", 20))), 2},
			{frame(line(3, pad("three!", 20))), 1},
			{frame(line(3, pad("three!!", 20))), 0},
			{frame(line(2, pad("two", 20))), 1},
		}},
		{"resize", []step{
			{frame(line(2, pad("two", 20)), line(3, pad("three", 20))), 0},
			{frame(line(2, pad("two!", 20))), 0},
			{"resize", 0},
			{frame("\x1b[2J", line(2, pad("two!", 30)), line(3, pad("three", 30))), -1},
			// The repaint wrote row 2 while the pen was not known: the row is written whole once more. Row 3 is known and shows the line.
			{frame(line(2, pad("two!", 30)), line(3, pad("three", 30))), 1},
			{frame(line(2, pad("two!!", 30))), 0},
			{frame(line(2, pad("t", 30))), 1},
		}},
		{"alternate screen", []step{
			{frame(line(2, pad("two", 20)), line(3, pad("three", 20))), 0},
			{"\x1b[?1049l", -1},
			{"\x1b[?1049h\x1b[?7l", -1},
			// The first row is written while the pen is not known, and whole once more; then in place.
			{frame(line(2, pad("two!", 20))), 1},
			{frame(line(2, pad("two!!", 20))), 1},
			{frame(line(2, pad("two!!!", 20))), 0},
		}},
		{"settle write", []step{
			{frame(line(2, pad("count 1", 20)), line(3, pad("three", 20))), 0},
			{frame(line(2, pad("count 2", 20)), line(3, pad("three", 20))), 0},
			{frame(line(2, pad("count 2 and more", 20))), 0},
			// The settle write writes rows 2 and 3 whole, as the frames wrote them: the lines they have already.
			{"settle", 2},
			{frame(line(2, pad("count 3 and more", 20))), 0},
			{frame(line(2, pad("count", 20))), 1},
			{"settle", 0},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cols, rows := 20, 6
			w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
			pair := gridterm.NewPair(cols, rows)
			for _, write := range gridterm.SplitWrites(started) {
				pair.Step(write, w.Translate(write))
			}
			for i, s := range c.steps {
				in, out := s.write, ""
				switch s.write {
				case "resize":
					cols = 30
					pair.Resize(cols, rows)
					continue
				case "settle":
					in, out = "", w.Settled()
				default:
					out = w.Translate(in)
				}
				if d := pair.Step(in, out); d != "" {
					t.Fatalf("step %d: %s\n  frame  %q\n  writer %q", i, d, in, out)
				}
				if s.whole < 0 && out != in {
					t.Errorf("step %d: %q was sent as %q, want as it came", i, in, out)
				}
				if got := strings.Count(out, erase); s.whole >= 0 && got != s.whole {
					t.Errorf("step %d: the writer wrote %d rows whole, want %d\n  frame  %q\n  writer %q", i, got, s.whole, in, out)
				}
			}
		})
	}
}
