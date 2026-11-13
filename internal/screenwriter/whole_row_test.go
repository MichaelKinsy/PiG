// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// The tests of this file are of one rule: a row as wide as the screen of which the write in place would send every cell is written
// whole, as the frame wrote it, and waits for no settle write. The screen is 20 by 5 cells unless a test says otherwise.

// padded is text, a column a character, followed by blanks up to cols columns.
func padded(text string, cols int) string {
	return text + strings.Repeat(" ", cols-utf8.RuneCountInString(text))
}

// stepper feeds a frame to the writer and both streams to the screens of every model, and fails the test where they differ.
func stepper(t *testing.T, w *Writer, pair *gridterm.Pair) func(in string) string {
	return func(in string) string {
		t.Helper()
		out := strings.Clone(w.Translate(in))
		if d := pair.Step(in, out); d != "" {
			t.Fatalf("%s\n  frame  %q\n  writer %q", d, in, out)
		}
		return out
	}
}

// settle takes the settle write and applies it to the writer's screens: it must change nothing the frames' bytes left.
func settle(t *testing.T, w *Writer, pair *gridterm.Pair) string {
	t.Helper()
	out := strings.Clone(w.Settled())
	if d := pair.Step("", out); d != "" {
		t.Fatalf("the settle write changed the screen: %s\n  settle %q", d, out)
	}
	return out
}

// A row as wide as the screen that changes through its last cell is written as the frame wrote it, and no settle write follows.
func TestAFullWidthRowWrittenThroughItsLastCellIsWrittenWholeAndWaitsForNoSettleWrite(t *testing.T) {
	w, pair := newTestWriter(20, 5), newTestPair(20, 5)
	step := stepper(t, w, pair)
	step(frame(line(2, padded("ab", 20))))
	settle(t, w, pair)
	after := frame(line(2, padded("abc", 20)))
	if got, want := step(after), syncBegin+line(2, padded("abc", 20))+at(1, 1)+syncEnd; got != want {
		t.Errorf("the writer sent\n  %q, want the row whole:\n  %q", got, want)
	}
	if got := w.Settled(); got != "" {
		t.Errorf("a settle write after a row written whole: %q", got)
	}
}

// A row as wide as the screen that another program wrote to is whole again with the frame that changes it, in what a terminal shows
// and in what tmux keeps of the line (the scribble made tmux's line longer than the frame's line makes it, which only the frame's
// erase undoes), and the settle write does not write it. The row the frame rewrote without a change waits for the settle write.
func TestAScribbledFullWidthRowHealsByTheFrameThatChangesIt(t *testing.T) {
	w, pair := newTestWriter(20, 5), newTestPair(20, 5)
	step := stepper(t, w, pair)
	step(frame(line(1, padded("one", 20)), line(2, padded("two", 20)), line(3, "three")))
	settle(t, w, pair)
	scribble := at(2, 1) + strings.Repeat("Y", 15) + at(1, 1)
	if d := pair.Step(scribble, scribble); d != "" {
		t.Fatal(d)
	}
	step(frame(line(1, padded("one", 20)), line(2, padded("two!", 20)), line(3, "three")))
	settled := settle(t, w, pair)
	if strings.Contains(settled, "two!") {
		t.Errorf("the settle write %q writes the row the frame's translation wrote whole", settled)
	}
	if !strings.Contains(settled, "\x1b[2K"+padded("one", 20)+reset) {
		t.Errorf("the settle write %q does not write the row the frame rewrote without a change", settled)
	}
}

// A row narrower than the screen that changes through its last cell is written in place as before, and the settle write writes it.
func TestARowNarrowerThanTheScreenWrittenThroughItsLastCellStillSettles(t *testing.T) {
	for _, c := range []struct{ name, before, after string }{
		{"text", "ab", "abc"},
		{"padded to one column short of the screen", padded("ab", 19), padded("abc", 19)},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, pair := newTestWriter(20, 5), newTestPair(20, 5)
			step := stepper(t, w, pair)
			step(frame(line(2, c.before)))
			settle(t, w, pair)
			if got, want := step(frame(line(2, c.after))), syncBegin+at(2, 1)+c.after+at(1, 1)+syncEnd; got != want {
				t.Errorf("the writer sent\n  %q, want the row in place:\n  %q", got, want)
			}
			if settled := settle(t, w, pair); !strings.Contains(settled, line(2, c.after)) {
				t.Errorf("the settle write %q does not write the row", settled)
			}
		})
	}
}

// Rows as wide as the screen whose cells are not a byte a column: each leaves every model's screen as the frame's bytes do, is
// written whole or in part as said, and waits for the settle write only when a part of it was written.
func TestFullWidthRowsOfWideJoinedAndLinkedCells(t *testing.T) {
	const day, book = "\u65e5", "\u672c" // two columns each in the renderers' count
	blanks := func(n int) string { return strings.Repeat(" ", n) }
	link := "\x1b]8;;http://a\x07"
	for _, c := range []struct {
		name          string
		before, after string
		whole         bool // the writer sends the row as the frame wrote it; else a part of it, and the settle write the row
	}{
		{"a wide character in the last two columns, a cell before it changed: a part, then the settle write",
			padded("ab", 18) + day, padded("ax", 18) + day, false},
		{"a wide character in the last two columns, changed: whole",
			padded("ab", 18) + day, padded("ab", 18) + book, true},
		{"a wide character at the row's start, the row changed through its last cell: whole",
			day + padded("ab", 18), day + padded("abc", 18), true},
		{"the first half of a wide character in the last column (a line a column too wide): as the frame wrote it",
			padded("ab", 19) + day, padded("abc", 19) + day, true},
		{"a combining mark: as the frame wrote it",
			"cafe\u0301" + blanks(16), "cafe\u0301!" + blanks(15), true},
		{"a joined emoji: as the frame wrote it",
			"\U0001F469\u200d\U0001F4BB" + blanks(18), "\U0001F469\u200d\U0001F4BB!" + blanks(17), true},
		{"a hyperlink that runs to the row's end: as the frame wrote it",
			"see " + link + strings.Repeat("l", 16), "sea " + link + strings.Repeat("l", 16), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, pair := newTestWriter(20, 5), newTestPair(20, 5)
			step := stepper(t, w, pair)
			step(frame(line(2, c.before)))
			settle(t, w, pair)
			out := step(frame(line(2, c.after)))
			if whole := strings.Contains(out, "\x1b[2K"+c.after+reset); whole != c.whole {
				t.Errorf("the writer sent %q: the row whole is %v, want %v", out, whole, c.whole)
			}
			if settled := settle(t, w, pair); strings.Contains(settled, "\x1b[2K"+c.after+reset) == c.whole {
				t.Errorf("the settle write is %q: it writes a row that was written whole, or not the row a part of which was written", settled)
			}
			// The frame after it finds the row as the frame before left it.
			step(frame(line(2, c.before)))
			settle(t, w, pair)
		})
	}
}

// Trailing cells under a background. The frame erases the row under the default pen when the line before it ended with a reset,
// and under that line's pen when it did not; the writer sends the same erase under the same pen, and every cell the erase clears
// the row's line paints. So a terminal that erases to the pen's background and one that erases to the default one end with the
// frame's cells, on every width model, and no settle write follows a row whose background changed through its last cell.
func TestAFullWidthRowUnderABackgroundOnTerminalsWithAndWithoutBackgroundColourErase(t *testing.T) {
	filled := func(text string, colour int) string {
		return text + fmt.Sprintf("\x1b[%dm", colour) + strings.Repeat(" ", 20-len(text))
	}
	open := func(first, second string) string {
		return syncBegin + at(1, 1) + "\x1b[2K\x1b[41m" + first + at(2, 1) + "\x1b[2K" + filled(second, 44) + reset + at(1, 1) + "\x1b[?25l" + syncEnd
	}
	var models []gridterm.Model
	for _, model := range gridterm.Models {
		without := model
		without.Name, without.NoBCE = model.Name+" without background-colour-erase", true
		models = append(models, model, without)
	}
	// The two kinds of terminal differ where an erase under a background is all a row gets.
	with, without := gridterm.New(20, 5, models[0]), gridterm.New(20, 5, models[1])
	for _, v := range []*gridterm.Terminal{with, without} {
		v.Write(started + at(2, 1) + "\x1b[44m\x1b[2K\x1b[0m")
	}
	if gridterm.Differ(with, without) == "" {
		t.Fatal("the screens with and without background-colour-erase do not differ after an erase under a background")
	}
	for _, c := range []struct {
		name   string
		frames []string
		quiet  bool // no settle write follows the last frame
	}{
		{"erased under the default pen", []string{frame(line(2, filled("ab", 44))), frame(line(2, filled("abc", 45)))}, true},
		{"erased under the pen the line before it left", []string{open("red", "ab"), open("red!", "abc"), open("red!", "abcd")}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newTestWriter(20, 5)
			var reference, translated []*gridterm.Terminal
			for _, model := range models {
				a, b := gridterm.New(20, 5, model), gridterm.New(20, 5, model)
				a.Write(started)
				b.Write(started)
				reference, translated = append(reference, a), append(translated, b)
			}
			feed := func(in, out string) {
				t.Helper()
				for i := range reference {
					reference[i].Write(in)
					translated[i].Write(out)
					if d := gridterm.Differ(reference[i], translated[i]); d != "" {
						t.Fatalf("%s: %s\n  frame  %q\n  writer %q", models[i].Name, d, in, out)
					}
				}
			}
			for i, in := range c.frames {
				feed(in, strings.Clone(w.Translate(in)))
				if i == 0 {
					feed("", strings.Clone(w.Settled()))
				}
			}
			settled := strings.Clone(w.Settled())
			feed("", settled)
			if c.quiet && settled != "" {
				t.Errorf("a settle write after a row written whole: %q", settled)
			}
			if w.Stats().Translated == 0 {
				t.Error("the writer translated no frame")
			}
		})
	}
}

// What follows a row written through the screen's last column, where a terminal with autowrap on would hold a wrap pending: the
// writer sends such a row as the frame did and, after it, what the frame sent after it: a cursor address (to the last column
// itself too), a line feed at the address the frame gave it, the cursor shown or hidden. The settle write of another row starts
// with that row's address. Every model's screens are the frame's after each.
func TestWhatFollowsARowWrittenThroughTheLastColumn(t *testing.T) {
	full := line(2, padded("abc", 20))
	t.Run("a cursor address at the last column", func(t *testing.T) {
		w, pair := newTestWriter(20, 5), newTestPair(20, 5)
		step := stepper(t, w, pair)
		step(frame(line(2, padded("ab", 20))))
		settle(t, w, pair)
		if got, want := step(syncBegin+full+at(2, 20)+"\x1b[?25l"+syncEnd), syncBegin+full+at(2, 20)+syncEnd; got != want {
			t.Errorf("the writer sent\n  %q, want the row and the address after it:\n  %q", got, want)
		}
	})
	t.Run("a line feed", func(t *testing.T) {
		w, pair := newTestWriter(20, 5), newTestPair(20, 5)
		step := stepper(t, w, pair)
		bottom := line(5, padded("abc", 20))
		step(frame(line(4, "above"), line(5, padded("ab", 20))))
		settle(t, w, pair)
		out := step(syncBegin + bottom + at(5, 1) + "\n" + line(5, "new") + at(1, 1) + "\x1b[?25l" + syncEnd)
		if !strings.Contains(out, bottom+at(5, 1)+"\n") {
			t.Errorf("the writer sent %q, want the row whole, then the frame's address and its line feed", out)
		}
		if got := pair.Reference().Row(3); got != "abc" {
			t.Errorf("row 4 shows %q after the scroll, want the row that was written through the last column", got)
		}
	})
	t.Run("the settle write of another row", func(t *testing.T) {
		w, pair := newTestWriter(20, 5), newTestPair(20, 5)
		step := stepper(t, w, pair)
		step(frame(line(2, padded("ab", 20)), line(3, "abcdefgh")))
		settle(t, w, pair)
		out := step(frame(full, line(3, "abXdefgh")))
		if want := syncBegin + full + at(3, 1) + "abX" + at(1, 1) + syncEnd; out != want {
			t.Errorf("the writer sent\n  %q, want\n  %q", out, want)
		}
		if got, want := settle(t, w, pair), syncBegin+line(3, "abXdefgh")+at(1, 1)+syncEnd; got != want {
			t.Errorf("the settle write is\n  %q, want the other row only:\n  %q", got, want)
		}
	})
	t.Run("the cursor shown, then hidden", func(t *testing.T) {
		w, pair := newTestWriter(20, 5), newTestPair(20, 5)
		step := stepper(t, w, pair)
		step(frame(line(2, padded("ab", 20))))
		settle(t, w, pair)
		if got, want := step(syncBegin+full+at(2, 20)+"\x1b[?25h"+syncEnd), syncBegin+full+at(2, 20)+"\x1b[?25h"+syncEnd; got != want {
			t.Errorf("the writer sent\n  %q, want\n  %q", got, want)
		}
		if got, want := step(syncBegin+full+at(2, 20)+"\x1b[?25l"+syncEnd), syncBegin+"\x1b[?25l"+syncEnd; got != want {
			t.Errorf("the writer sent\n  %q, want only the cursor hidden:\n  %q", got, want)
		}
		if got := w.Settled(); got == "" {
			t.Error("no settle write for the row the second frame rewrote without a change")
		}
	})
}

// printsInTheLastColumnInPlace reports whether out, the translation of a frame of one-column cells, prints in the last of cols
// columns on a row it did not erase first.
func printsInTheLastColumnInPlace(out string, cols int) bool {
	col, erased := 0, false
	for i := 0; i < len(out); {
		kind, end := nextToken(out, i)
		seq := out[i:end]
		i = end
		switch {
		case kind == tokenCSI && seq[len(seq)-1] == 'H':
			_, c, _ := twoParams(seq[2 : len(seq)-1])
			col, erased = c-1, false
		case seq == "\x1b[2K":
			erased = true
		case kind == tokenText:
			if col += len(seq); col >= cols && !erased {
				return true
			}
		}
	}
	return false
}

// The write in place never prints in the screen's last column: over random rows of one-column cells under pens, as wide as the
// screen and narrower, every row the writer sends through the last column it sends as the frame did, the erase first.
func TestTheWriteInPlaceNeverPrintsInTheLastColumn(t *testing.T) {
	const cols, rows = 12, 4
	pens := []string{"\x1b[1m", "\x1b[44m", "\x1b[7m", "\x1b[0m"}
	frames, inPlace, whole := 0, 0, 0
	for seed := range uint64(40) {
		rng := rand.New(rand.NewPCG(seed, 13))
		w, pair := newTestWriter(cols, rows), newTestPair(cols, rows)
		step := stepper(t, w, pair)
		for range 120 {
			var text strings.Builder
			width := rng.IntN(cols + 1)
			if rng.IntN(2) == 0 {
				width = cols
			}
			letters := rng.IntN(width + 1)
			for col := range width {
				if rng.IntN(6) == 0 {
					text.WriteString(pens[rng.IntN(len(pens))])
				}
				if col < letters {
					text.WriteByte("abc"[rng.IntN(3)])
				} else {
					text.WriteByte(' ')
				}
			}
			out := step(frame(line(1+rng.IntN(rows), text.String())))
			if printsInTheLastColumnInPlace(out, cols) {
				t.Fatalf("seed %d: the writer printed in the last column of a row it did not erase: %q", seed, out)
			}
			frames++
			switch {
			case strings.Contains(out, "\x1b[2K"):
				whole++
			case out != "":
				inPlace++
			}
			if rng.IntN(8) == 0 {
				settle(t, w, pair)
			}
		}
	}
	if inPlace < frames/10 || whole < frames/10 {
		t.Errorf("of %d frames %d were written in place and %d whole: the rows do not exercise both", frames, inPlace, whole)
	}
}

// A terminal that changes size between a frame and the settle timer: the settle write writes nothing, as before, and the next
// frame's rows are written whole. Without the change of size the settle write writes the row that had a part written and not the
// row as wide as the screen, which the frame's translation wrote whole.
func TestAResizeAfterAFullWidthRowWasWrittenWhole(t *testing.T) {
	for _, resized := range []bool{false, true} {
		t.Run(fmt.Sprintf("resized %v", resized), func(t *testing.T) {
			cols, rows := 20, 5
			w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
			for _, write := range gridterm.SplitWrites(started) {
				w.Translate(write)
			}
			w.Translate(frame(line(2, padded("ab", 20)), line(3, "abcdefgh")))
			w.Settled()
			out := w.Translate(frame(line(2, padded("abc", 20)), line(3, "abXdefgh")))
			if want := syncBegin + line(2, padded("abc", 20)) + at(3, 1) + "abX" + at(1, 1) + syncEnd; out != want {
				t.Fatalf("the writer sent\n  %q, want\n  %q", out, want)
			}
			if !resized {
				if got, want := w.Settled(), syncBegin+line(3, "abXdefgh")+at(1, 1)+syncEnd; got != want {
					t.Errorf("the settle write is\n  %q, want\n  %q", got, want)
				}
				return
			}
			cols = 12
			for range 2 {
				if got := w.Settled(); got != "" {
					t.Fatalf("the settle write after a resize is %q", got)
				}
			}
			next := frame(line(2, padded("abc", 12)), line(3, "abXdefgh"))
			if got := w.Translate(next); got != next {
				t.Errorf("after the resize the writer sent\n  %q, want every row as the frame wrote it:\n  %q", got, next)
			}
		})
	}
}

// A frame that holds an image (a Kitty graphics command, a sixel string, an iTerm2 inline image) is not read: the row's line ends
// before the image's sequence (lineEnd), the frame has no rule for the sequence (frame), so the frame is sent as it came and every
// row is forgotten (unread). No row of it is written in place or whole by the writer, and none waits for a settle write. The rows
// as wide as the screen that later frames write over the image's cells are written whole, as the frames wrote them: a terminal
// that keeps an image in the cells it covers loses it there with the writer as without it.
func TestImageRowsAreNeverWrittenByTheWriter(t *testing.T) {
	for _, c := range []struct{ name, image string }{
		{"Kitty", "\x1b_Ga=T,f=100,q=2,C=1,c=3,r=2,i=7;AAAA\x1b\\"},
		{"sixel", "\x1bPq#0;2;0;0;0#0~~\x1b\\"},
		{"iTerm2", "\x1b]1337;File=inline=1;width=3;height=2:AAAA\x07"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, pair := newTestWriter(20, 5), newTestPair(20, 5)
			step := stepper(t, w, pair)
			step(frame(line(2, padded("ab", 20)), line(3, padded("cd", 20))))
			settle(t, w, pair)
			image := syncBegin + line(1, "a") + at(2, 1) + "\x1b[2K" + padded("abc", 17) + c.image + reset + at(1, 1) + "\x1b[?25l" + syncEnd
			unread := w.Stats().Unread
			if got := step(image); got != image {
				t.Fatalf("the frame with the image was sent as\n  %q, want as it came:\n  %q", got, image)
			}
			if w.Stats().Unread != unread+1 {
				t.Errorf("the frame with the image was read")
			}
			if got := w.Settled(); got != "" {
				t.Errorf("a settle write after the frame with the image: %q", got)
			}
			for _, text := range []string{"x", "xy", "xyz"} {
				out := step(frame(line(2, padded("ab"+text, 20)), line(3, padded("cd"+text, 20))))
				if n := strings.Count(out, "\x1b[2K"); n != 2 {
					t.Errorf("the writer sent %q: %d rows whole over the image's cells, want both", out, n)
				}
				if got := w.Settled(); got != "" {
					t.Errorf("a settle write for rows written whole: %q", got)
				}
			}
		})
	}
}
