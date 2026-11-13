// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Every test of this package that gives the writer frames requires that at least one of them was translated: a comparison of what
// the writer sends with what the frames wrote is satisfied by a writer that sends every frame as it came. To see that this holds,
// make Translate send every frame as it came (take its branch for autowrap that is not known to be off always) and run the
// package: the tests that still pass must be the ones named here. They give the writer no frames: TestNextToken, TestTwoParams and
// TestApplySGR read sequences; TestOutsideTable and TestOutsideReportsAutowrap classify writes that are not frames;
// TestGridKeepsALinesLength is of the tests' screens. And the subject of TestRegularModeIsSentAsItCame is that nothing is
// translated on the main screen: it requires a run that holds synchronized updates and that every write of it is sent unchanged.

const reset = "\x1b[0m\x1b]8;;\x07" // widthx.SegmentReset: what the renderers end a line with

// started is what a session writes first: the alternate screen, then two repaints. After the first the pen is known (a line's reset
// ended it), after the second every row is known to be blank.
var started = "\x1b[?1049h\x1b[?7l" + frame("\x1b[2J", line(1, "")) + frame("\x1b[2J", line(1, ""))

// newTestWriter is a writer of a cols by rows terminal that took started, without a settle timer.
func newTestWriter(cols, rows int) *Writer {
	w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
	for _, write := range gridterm.SplitWrites(started) {
		w.Translate(write)
	}
	return w
}

// newTestPair are the screens of a cols by rows terminal that took started.
func newTestPair(cols, rows int) *gridterm.Pair {
	pair := gridterm.NewPair(cols, rows)
	pair.Step(started, started)
	return pair
}

func at(row, col int) string { return fmt.Sprintf("\x1b[%d;%dH", row, col) }

// line is a frame's write of a row: the cursor at its first column, the erase, the text and the renderers' reset.
func line(row int, text string) string { return at(row, 1) + "\x1b[2K" + text + reset }

// frame is a synchronized update of parts that ends, as the renderers' frames do, with the cursor's position and with the cursor hidden.
func frame(parts ...string) string {
	return syncBegin + strings.Join(parts, "") + at(1, 1) + "\x1b[?25l" + syncEnd
}

func TestNextToken(t *testing.T) {
	for _, c := range []struct {
		data      string
		kind, end int
	}{
		{"abc\x1b[m", tokenText, 3},
		{"\x1b[38;5;66mx", tokenCSI, 10},
		{"\x1b[?2026h", tokenCSI, 8},
		{"\x1b[4:3m", tokenCSI, 6},
		{"\x1b[ q", tokenCSI, 4},
		{"\x1b[12", tokenBad, 4},
		{"\n", tokenC0, 1},
		{"\x7f", tokenC0, 1},
		{"\x1b]8;;\x07x", tokenOSC, 6},
		{"\x1b]8;;http://a\x1b\\x", tokenOSC, 15},
		{"\x1b]0;title", tokenBad, 9},
		{"\x1b]0;a\x1bx\x07", tokenBad, 8},
		{"\x1b_Gf=100;AAAA\x1b\\x", tokenString, 15},
		{"\x1bPq#0\x1b\\", tokenString, 7},
		{"\x1bPq#0", tokenBad, 5},
		{"\x1bM", tokenEscape, 2},
		{"\x1b(B", tokenEscape, 3},
		{"\x1b", tokenBad, 1},
	} {
		if kind, end := nextToken(c.data, 0); kind != c.kind || end != c.end {
			t.Errorf("nextToken(%q) = %d, %d, want %d, %d", c.data, kind, end, c.kind, c.end)
		}
	}
}

func TestTwoParams(t *testing.T) {
	for _, c := range []struct {
		params string
		a, b   int
		ok     bool
	}{{"", 1, 1, true}, {"3", 3, 1, true}, {"3;7", 3, 7, true}, {";7", 1, 7, true}, {"0;0", 1, 1, true}, {"3;", 3, 1, true}, {"1;2;3", 0, 0, false}, {"1:2", 0, 0, false}, {"99999", 0, 0, false}, {"?1", 0, 0, false}} {
		if a, b, ok := twoParams(c.params); a != c.a || b != c.b || ok != c.ok {
			t.Errorf("twoParams(%q) = %d, %d, %v, want %d, %d, %v", c.params, a, b, ok, c.a, c.b, c.ok)
		}
	}
}

func TestApplySGR(t *testing.T) {
	for _, c := range []struct {
		from   pen
		params string
		want   pen
		ok     bool
	}{
		{pen{}, "", pen{}, true},
		{pen{fg: 1<<24 | 31, attrs: attrBold}, "0", pen{}, true},
		{pen{}, "1;2;3;4;5;7;8;9;53", pen{attrs: attrBold | attrDim | attrItalic | attrUnderline | attrBlink | attrInverse | attrHidden | attrStrike | attrOverline}, true},
		{pen{attrs: attrBold | attrDim | attrItalic}, "22", pen{attrs: attrItalic}, true},
		{pen{attrs: attrItalic | attrUnderline | attrBlink | attrInverse | attrHidden | attrStrike | attrOverline}, "23;24;25;27;28;29;55", pen{}, true},
		{pen{}, "31;102", pen{fg: 1<<24 | 31, bg: 1<<24 | 102}, true},
		{pen{}, "38;5;66", pen{fg: 2<<24 | 66}, true},
		{pen{}, "48;5;237;38;2;1;2;3", pen{fg: 3<<24 | 1<<16 | 2<<8 | 3, bg: 2<<24 | 237}, true},
		{pen{fg: 2<<24 | 66, bg: 2<<24 | 237}, "39", pen{bg: 2<<24 | 237}, true},
		{pen{fg: 2<<24 | 66, bg: 2<<24 | 237}, "49", pen{fg: 2<<24 | 66}, true},
		{pen{}, "2;38;5;3", pen{fg: 2<<24 | 3, attrs: attrDim}, true},
		// Outside the set every terminal reads alike.
		{pen{}, "4:3", pen{}, false},
		{pen{}, "38:5:66", pen{}, false},
		{pen{}, "58;5;1", pen{}, false},
		{pen{}, "21", pen{}, false},
		{pen{}, "38;5", pen{}, false},
		{pen{}, "38;2;1;2", pen{}, false},
		{pen{}, "38;5;256", pen{}, false},
		{pen{}, "1;2;3;4;5;7;8;9;22;23;24;25;27", pen{}, false},
		{pen{}, ">4;2", pen{}, false},
	} {
		got := c.from
		if ok := got.applySGR(c.params); ok != c.ok || ok && got != c.want {
			t.Errorf("applySGR(%q) from %+v = %+v, %v, want %+v, %v", c.params, c.from, got, ok, c.want, c.ok)
		}
	}
}

// Every sequence the terminal's start, stop and exit paths and the interactive mode write outside frames has a row here: what keeps the
// screen the writer knows and what forgets it. A sequence without a row forgets.
func TestOutsideTable(t *testing.T) {
	for _, c := range []struct {
		data           string
		screen, forget bool
		cursor         int
	}{
		{"\x1b[?2004h", true, false, 0},
		{"\x1b[?2004l\x1b[?1004l\x1b[?1002l\x1b[?1003l\x1b[?1006l", true, false, 0},
		{"\x1b[?1000h\x1b[?1002h\x1b[?1006h", true, false, 0},
		{"\x1b[?2031h\x1b[?2048h\x1b[?2027h", true, false, 0},
		{"\x1b[>7u\x1b[<u\x1b[?u\x1b[=1;1u", true, false, 0},
		{"\x1b[>4;2m\x1b[>4;0m", true, false, 0},
		{"\x1b[c\x1b[>c\x1b[>q\x1b[6n\x1b[?996n\x1b[?2026$p\x1b[14t\x1b[16t\x1b[18t", true, false, 0},
		{"\x1b[5 q\x1b[0 q", true, false, 0},
		{"\x1b]0;pi\x07\x1b]2;pi\x1b\\", true, false, 0},
		{"\x1b]9;4;3\x07\x1b]9;done\x07\x1b]52;c;AAAA\x07\x1b]7501;busy\x07\x1b]133;A\x07\x1b]7;file://h/p\x07", true, false, 0},
		{"\x1b]10;?\x07\x1b]11;?\x07\x1b]12;?\x07", true, false, 0},
		{"\x07", true, false, 0},
		{"\x1b=\x1b>", true, false, 0},
		{"\x1b[?25l", true, false, -1},
		{"\x1b[?25h", true, false, 1},
		{"\x1b[?1049l", false, true, 0},
		{"\x1b[?1049l\x1b[?1049h", true, true, 0},
		{"\x1b[?1049h\x1b[?25l\x1b[?1049l", false, true, -1},
		// Everything else may change the screen.
		{"text", true, true, 0},
		{"\r\n", true, true, 0},
		{"\x1b[2J", true, true, 0},
		{"\x1b[H", true, true, 0},
		{"\x1b[5A", true, true, 0},
		{"\x1b[31m", true, true, 0},
		{"\x1b[?7l\x1b[?7h", true, false, 0},
		{"\x1b[?6h", true, true, 0},
		{"\x1b[?1047h", true, true, 0},
		{"\x1b[1;10r", true, true, 0},
		{"\x1bc", true, true, 0},
		{"\x1b7", true, true, 0},
		{"\x1b]8;;http://example.com\x07", true, true, 0},
		{"\x1b]1337;File=inline=1:AAAA\x07", true, true, 0},
		{"\x1b]4;1;?\x07\x1b]4;1;#ff0000\x07", true, false, 0},
		{"\x1b]104\x07", true, true, 0},
		{"\x1b_Gf=100,a=T;AAAA\x1b\\", true, true, 0},
		{"\x1bPq#0;2;0;0;0#0~~\x1b\\", true, true, 0},
		{"\x1b[?2026h", true, false, 0},
		{"\x1b[", true, true, 0},
	} {
		screen, cursor, forget, _ := outside(c.data, true)
		if screen != c.screen || cursor != c.cursor || forget != c.forget {
			t.Errorf("outside(%q) = screen %v, cursor %d, forget %v, want %v, %d, %v", c.data, screen, cursor, forget, c.screen, c.cursor, c.forget)
		}
	}
}

// The rules, one case each: the frames the writer is given, and what it sends for the last one.
func TestRules(t *testing.T) {
	const wide = "\u65e5\u672c" // two CJK characters, two columns each in the renderers' count
	for _, c := range []struct {
		name   string
		frames []string
		want   string
	}{
		{"a row rewritten with the cells it shows is not written, and a frame that changes nothing is not written",
			[]string{frame(line(1, "hello")), frame(line(1, "hello"))}, ""},
		{"the same cells in other bytes are not written",
			[]string{frame(line(1, "\x1b[1mhi\x1b[22m")), frame(line(1, "\x1b[0;1mhi\x1b[0m"))}, ""},
		{"text added at a row's end: the row from its first cell",
			[]string{frame(line(2, "hello")), frame(line(2, "hello world"))}, syncBegin + at(2, 1) + "hello world" + at(1, 1) + syncEnd},
		{"a cell changed before an unchanged tail: through the changed cell, the tail stays",
			[]string{frame(line(2, "abcdefgh")), frame(line(2, "abXdefgh"))}, syncBegin + at(2, 1) + "abX" + at(1, 1) + syncEnd},
		{"a pen changed before an unchanged tail: the line's own SGR bytes, then the reset",
			[]string{frame(line(2, "abcdefgh")), frame(line(2, "a\x1b[31mb\x1b[39mcdefgh"))}, syncBegin + at(2, 1) + "a\x1b[31mb" + at(1, 1) + "\x1b[0m" + syncEnd},
		{"a row that starts with a pen: the line's own first bytes",
			[]string{frame(line(2, "\x1b[38;5;66m|\x1b[39m ab")), frame(line(2, "\x1b[38;5;66m|\x1b[39m xb"))}, syncBegin + at(2, 1) + "\x1b[38;5;66m|\x1b[39m x" + at(1, 1) + syncEnd},
		{"a shorter row is written whole, as the frame wrote it: the line erased, then the row",
			[]string{frame(line(2, "hello world")), frame(line(2, "help"))}, syncBegin + line(2, "help") + at(1, 1) + syncEnd},
		{"a row that lost its end is written whole",
			[]string{frame(line(2, "hello world")), frame(line(2, "hello"))}, syncBegin + line(2, "hello") + at(1, 1) + syncEnd},
		{"an emptied row is written whole: the frame's erase of the line",
			[]string{frame(line(2, "hello")), frame(line(2, ""))}, syncBegin + line(2, "") + at(1, 1) + syncEnd},
		{"a blank row is written from its first cell",
			[]string{frame(line(1, "a")), frame(line(2, "new"))}, syncBegin + at(2, 1) + "new" + at(1, 1) + syncEnd},
		{"only the cursor moved: its address as the frame wrote it",
			[]string{frame(line(1, "a")), syncBegin + at(1, 2) + "\x1b[?25l" + syncEnd}, syncBegin + at(1, 2) + syncEnd},
		{"a cursor address past the edge is sent as written",
			[]string{frame(line(1, "a")), syncBegin + at(1, 99) + "\x1b[?25l" + syncEnd}, syncBegin + at(1, 99) + syncEnd},
		{"the cursor shown: only the change",
			[]string{frame(line(1, "a")), syncBegin + at(1, 1) + "\x1b[?25h" + syncEnd}, syncBegin + "\x1b[?25h" + syncEnd},
		{"an open hyperlink: the row whole, as the frame wrote it",
			[]string{frame(line(2, "see \x1b]8;;http://a\x07link\x1b]8;;\x07 here")), frame(line(2, "see \x1b]8;;http://a\x07link\x1b]8;;\x07 there"))},
			syncBegin + line(2, "see \x1b]8;;http://a\x07link\x1b]8;;\x07 there") + at(1, 1) + syncEnd},
		{"a combining mark: the row whole",
			[]string{frame(line(2, "cafe\u0301 a")), frame(line(2, "cafe\u0301 b"))}, syncBegin + line(2, "cafe\u0301 b") + at(1, 1) + syncEnd},
		{"an emoji with a variation selector: the row whole",
			[]string{frame(line(2, "\u2764\ufe0f a")), frame(line(2, "\u2764\ufe0f b"))}, syncBegin + line(2, "\u2764\ufe0f b") + at(1, 1) + syncEnd},
		{"a joined emoji: the row whole",
			[]string{frame(line(2, "\U0001F468\u200d\U0001F469 a")), frame(line(2, "\U0001F468\u200d\U0001F469 b"))}, syncBegin + line(2, "\U0001F468\u200d\U0001F469 b") + at(1, 1) + syncEnd},
		{"the same wide characters before the change: a part of the row",
			[]string{frame(line(2, wide+" abc")), frame(line(2, wide+" abd"))}, syncBegin + at(2, 1) + wide + " abd" + at(1, 1) + syncEnd},
		{"a wide character replaced before an unchanged tail: the row whole, the tail's column is the terminal's to count",
			[]string{frame(line(2, wide+" abc")), frame(line(2, "\u65e5\u6708 abc"))}, syncBegin + line(2, "\u65e5\u6708 abc") + at(1, 1) + syncEnd},
		{"an ambiguous-width character replaced before an unchanged tail: the row whole",
			[]string{frame(line(2, "\u00b1 abc")), frame(line(2, "\u00d7 abc"))}, syncBegin + line(2, "\u00d7 abc") + at(1, 1) + syncEnd},
		{"box drawing replaced before an unchanged tail: the row whole",
			[]string{frame(line(2, "\u2500\u2500 abc")), frame(line(2, "\u2500\u2502 abc"))}, syncBegin + line(2, "\u2500\u2502 abc") + at(1, 1) + syncEnd},
		{"a colon SGR form: the row whole",
			[]string{frame(line(2, "\x1b[4:3mwavy\x1b[24m a")), frame(line(2, "\x1b[4:3mwavy\x1b[24m b"))}, syncBegin + line(2, "\x1b[4:3mwavy\x1b[24m b") + at(1, 1) + syncEnd},
		{"a control character in a line: the frame as it came",
			[]string{frame(line(2, "a\tb")), frame(line(2, "a\tc"))}, frame(line(2, "a\tc"))},
		{"a line wider than the screen: the row whole",
			[]string{frame(line(2, strings.Repeat("x", 21))), frame(line(2, strings.Repeat("y", 21)))}, syncBegin + line(2, strings.Repeat("y", 21)) + at(1, 1) + syncEnd},
		{"a frame that erases the screen is sent as it came",
			[]string{frame(line(1, "a")), frame("\x1b[2J", line(1, "a"))}, frame("\x1b[2J", line(1, "a"))},
		{"a Kitty image in a frame: the frame as it came",
			[]string{frame(line(1, "a")), frame(line(1, "a"), at(2, 1), "\x1b_Gf=100,a=T;AAAA\x1b\\")}, frame(line(1, "a"), at(2, 1), "\x1b_Gf=100,a=T;AAAA\x1b\\")},
		{"a sixel image in a frame: the frame as it came",
			[]string{frame(line(1, "a")), frame(line(1, "a"), at(2, 1), "\x1bPq#0;2;0;0;0#0~~\x1b\\")}, frame(line(1, "a"), at(2, 1), "\x1bPq#0;2;0;0;0#0~~\x1b\\")},
		{"an iTerm2 image in a frame: the frame as it came",
			[]string{frame(line(1, "a")), frame(line(1, "a"), at(2, 1), "\x1b]1337;File=inline=1:AAAA\x07")}, frame(line(1, "a"), at(2, 1), "\x1b]1337;File=inline=1:AAAA\x07")},
		{"an escape sequence without a rule in a frame: the frame as it came",
			[]string{frame(line(1, "a")), frame(line(1, "b"), "\x1b[3S")}, frame(line(1, "b"), "\x1b[3S")},
		{"a row no cursor address follows: the row whole, which leaves the cursor where its line ends",
			[]string{frame(line(1, "a"), line(2, "b")), syncBegin + line(1, "a") + line(2, "c") + "\x1b[?25l" + syncEnd}, syncBegin + line(2, "c") + syncEnd},
		{"an unchanged row no cursor address follows is written whole all the same",
			[]string{frame(line(1, "a"), line(2, "b")), syncBegin + line(1, "a") + line(2, "b") + syncEnd}, syncBegin + line(2, "b") + syncEnd},
		{"a cursor address after the frame's last row, then the cursor's: only the last one is sent",
			[]string{frame(line(1, "a")), syncBegin + line(1, "a") + at(3, 4) + at(2, 2) + syncEnd}, syncBegin + at(2, 2) + syncEnd},
		{"after a frame sent as it came every row is written whole",
			[]string{frame(line(1, "a"), line(2, "b")), frame(line(1, "a"), "\x1b[3S"), frame(line(1, "a"), line(2, "b"))}, syncBegin + line(1, "a") + line(2, "b") + at(1, 1) + "\x1b[?25l" + syncEnd},
		{"after text written outside a frame every row is written whole",
			[]string{frame(line(1, "a")), "scribble", frame(line(1, "a"))}, syncBegin + line(1, "a") + at(1, 1) + "\x1b[?25l" + syncEnd},
		{"after an image written outside a frame every row is written whole",
			[]string{frame(line(1, "a")), "\x1b_Gf=100,a=T;AAAA\x1b\\", frame(line(1, "a"))}, syncBegin + line(1, "a") + at(1, 1) + "\x1b[?25l" + syncEnd},
		{"a title, a clipboard write and a mode switch outside a frame keep the rows",
			[]string{frame(line(1, "a")), "\x1b]0;t\x07", "\x1b]52;c;AAAA\x07", "\x1b[?2004h", frame(line(1, "a"))}, ""},
		{"the cursor shown outside a frame is followed",
			[]string{frame(line(1, "a")), "\x1b[?25h", frame(line(1, "a"))}, syncBegin + "\x1b[?25l" + syncEnd},
		{"a scroll region: the frame's own region, scroll and reset, and the row that came in from its first cell",
			[]string{frame(line(1, "a"), line(2, "b"), line(3, "c")), frame("\x1b[1;3r", at(3, 1), "\n", "\x1b[r", line(3, "d"))},
			syncBegin + "\x1b[1;3r" + at(3, 1) + "\n" + "\x1b[r" + at(3, 1) + "d" + at(1, 1) + syncEnd},
		{"the rows a scroll moved are known where they are now",
			[]string{frame(line(1, "a"), line(2, "b"), line(3, "c")), frame("\x1b[1;3r", at(3, 1), "\n", "\x1b[r", line(3, "d")), frame(line(1, "b"), line(2, "c"), line(3, "d"))}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newTestWriter(20, 5)
			pair := newTestPair(20, 5)
			got := ""
			for i, f := range c.frames {
				got = strings.Clone(w.Translate(f))
				if d := pair.Step(f, got); d != "" {
					t.Errorf("frame %d: %s\n  frame  %q\n  writer %q", i, d, f, got)
				}
			}
			if got != c.want {
				t.Errorf("the writer sent\n  %q, want\n  %q", got, c.want)
			}
		})
	}
}

// A row that got shorter leaves a terminal with no cell of the longer row: the writer erases the line, as the frame did, and never
// from the row's new end on. tmux keeps a line's length across an erase from a column on (capture-pane -N shows the rest of the old
// line as blanks), which the screens' line lengths follow; the first case is the one found there.
func TestAShorterRowKeepsNoCellOfTheLongerRow(t *testing.T) {
	for _, c := range []struct{ name, before, after string }{
		{"shorter", "the same line           ", "added 0.16.4"},
		{"lost its end", "hello world", "hello"},
		{"emptied", "hello", ""},
		{"shorter under a pen", "\x1b[1m\x1b[42mlonger row", "\x1b[1m\x1b[40mshort"},
		{"as wide as the screen, then shorter", strings.Repeat("x", 49), "x"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, pair := newTestWriter(49, 13), newTestPair(49, 13)
			for _, write := range []string{frame(line(8, c.before)), frame(line(9, "below")), frame(line(8, c.after)), frame(line(8, c.after+"!"))} {
				out := w.Translate(write)
				if strings.Contains(out, "\x1b[K") {
					t.Errorf("the writer erased to the end of a line: %q", out)
				}
				if diff := pair.Step(write, out); diff != "" {
					t.Errorf("after %q, sent as %q: %s", write, out, diff)
				}
			}
			if w.Stats().Translated == 0 {
				t.Errorf("the writer translated no frame")
			}
		})
	}
}

// A line's length in the screens: printing extends it, an erase from a column on keeps it, an erase of the whole line ends it, and
// a scroll moves it with its row.
func TestGridKeepsALinesLength(t *testing.T) {
	screen := func(writes ...string) *gridterm.Terminal {
		v := gridterm.New(20, 5, gridterm.Models[0])
		for _, write := range writes {
			v.Write(write)
		}
		return v
	}
	whole := screen(at(2, 1) + "hello world" + at(2, 1) + "\x1b[2K" + "help")
	for name, other := range map[string]*gridterm.Terminal{
		"erased from the new end on": screen(at(2, 1) + "hello world" + at(2, 1) + "help" + "\x1b[K"),
		"erased to the cursor":       screen(at(2, 1) + "hello world" + at(2, 12) + "\x1b[1K" + at(2, 1) + "help"),
	} {
		if diff := gridterm.Differ(whole, other); !strings.Contains(diff, "a line of 11 cells, not of 4") {
			t.Errorf("%s: the screens differ as %q, want the line's length", name, diff)
		}
	}
	for name, other := range map[string]*gridterm.Terminal{
		"written once":             screen(at(2, 1) + "help"),
		"erased by the screen":     screen(at(2, 1) + "hello world" + "\x1b[2J" + at(2, 1) + "help"),
		"scrolled up from row 3":   screen("\x1b[2;5r" + at(3, 1) + "help" + at(5, 1) + "\n" + "\x1b[r" + at(2, 5)),
		"scrolled down from row 1": screen(at(1, 1) + "help" + at(1, 1) + "\x1bM" + at(2, 5)),
	} {
		if diff := gridterm.Differ(whole, other); diff != "" {
			t.Errorf("%s: %s", name, diff)
		}
	}
}

// The writer makes no sequence of its own but a cursor address and the pen's reset: every other sequence it sends stands in the
// frame it translates. It never erases to the end of a line, which no frame does.
func TestWriterMakesNoSequenceOfItsOwn(t *testing.T) {
	for name, stream := range testStreams(t) {
		w := newTestWriter(160, 48)
		for _, write := range gridterm.SplitWrites(stream) {
			out := w.Translate(write)
			for i := 0; i < len(out); {
				kind, end := nextToken(out, i)
				seq := out[i:end]
				i = end
				if kind == tokenText || seq == "\x1b[0m" || kind == tokenCSI && seq[len(seq)-1] == 'H' || strings.Contains(write, seq) {
					continue
				}
				t.Fatalf("%s: the writer sent %q, which is not in the frame %q", name, seq, write)
			}
		}
		if w.Stats().Translated == 0 {
			t.Errorf("%s: the writer translated no frame: it sent nothing of its own to look at", name)
		}
	}
}

// testStreams are what PiG wrote (PI_TUI_WRITE_LOG) in the benchmark's long-answer and 50-file-write steps on a 160 by 48 terminal.
func testStreams(t testing.TB) map[string]string {
	streams := map[string]string{}
	for _, name := range []string{"race-md20000", "race-write50"} {
		file, err := os.Open("testdata/" + name + ".stream.gz")
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		streams[name] = "\x1b[?1049h\x1b[?7l" + string(data)
	}
	return streams
}

// The recorded streams leave the same screen after every write, with and without the writer, on every width model; the settle write
// changes no cell; and the writer never writes more than the frames do.
func TestRecordedStreamsShowTheSameScreens(t *testing.T) {
	for name, stream := range testStreams(t) {
		w := newTestWriter(160, 48)
		pair := gridterm.NewPair(160, 48)
		writes := gridterm.SplitWrites(stream)
		for i, write := range writes {
			out := w.Translate(write)
			if d := pair.Step(write, out); d != "" {
				t.Fatalf("%s write %d: %s\n  frame  %q\n  writer %q", name, i, d, write, out)
			}
			// The settle write comes when the frames pause: a few times in a step of the benchmark.
			if i%400 == 399 || i == len(writes)-1 {
				if d := pair.Step("", w.Settled()); d != "" {
					t.Fatalf("%s after write %d: the settle write changed the screen: %s", name, i, d)
				}
			}
		}
		stats := w.Stats()
		t.Logf("%s: %d writes, %d frames: %d translated, %d not written, %d repaints and %d without a rule sent as they came, %d settle writes; %d bytes in, %d out (%.1f %%)",
			name, len(writes), stats.Frames, stats.Translated, stats.Unchanged, stats.Repaints, stats.Unread, stats.Settles, stats.BytesIn, stats.BytesOut, 100*float64(stats.BytesOut)/float64(stats.BytesIn))
		if stats.Translated == 0 {
			t.Errorf("%s: the writer translated no frame: the screens are the same because the bytes are", name)
		}
		// The streams hold nothing the frame grammar does not cover: the repaints of the start (the first of them under a pen not yet known).
		if stats.Unread != 0 || stats.Repaints > 4 || stats.BytesOut > stats.BytesIn {
			t.Errorf("%s: %d of %d frames have no rule, %d are repaints, and %d bytes were written for %d: the writer does not take the renderers' frames, or writes more than they do", name, stats.Unread, stats.Frames, stats.Repaints, stats.BytesOut, stats.BytesIn)
		}
	}
}

// A terminal that ignores synchronized updates shows every byte as it arrives. The writer never blanks a row it does not write whole,
// so at no byte of a frame are more rows blank than with the renderers' bytes.
func TestWriterBlanksNoMoreRowsInsideAFrame(t *testing.T) {
	blankRows := func(v *gridterm.Terminal, data string) (worst int) {
		for i := 0; i < len(data); {
			_, end := nextToken(data, i)
			v.Write(data[i:end])
			i = end
			blank := 0
			for row := range 48 {
				if v.Row(row) == "" {
					blank++
				}
			}
			worst = max(worst, blank)
		}
		return worst
	}
	stream := testStreams(t)["race-md20000"]
	w := newTestWriter(160, 48)
	plain, translated := gridterm.New(160, 48, gridterm.Models[0]), gridterm.New(160, 48, gridterm.Models[0])
	more, fewer := 0, 0
	for i, write := range gridterm.SplitWrites(stream) {
		a, b := blankRows(plain, write), blankRows(translated, w.Translate(write))
		if b > a {
			more++
			t.Errorf("write %d: %d rows blank at some byte of the writer's frame, %d of the renderer's", i, b, a)
		}
		if b < a {
			fewer++
		}
	}
	if fewer == 0 {
		t.Error("no frame of the stream blanks fewer rows with the writer: the stream does not exercise the rule")
	}
	t.Logf("%d frames blank fewer rows at their worst byte with the writer", fewer)
}

// Random edits of rows drawn from alphabets terminals disagree about (wide, ambiguous, box drawing, combining marks, emoji with
// selectors and joiners, links, styles) leave the same screen with and without the writer after every frame and after the settle
// write, on every width model.
func TestRandomEditsShowTheSameScreens(t *testing.T) {
	alphabet := []string{"a", "b", "c", " ", " ", "x", "é", "Ж", "\u00b1", "\u00d7", "\u2500", "\u2502", "\u2588", "\u28ff", "\u65e5", "\u672c", "\uff21",
		"e\u0301", "\u0e01\u0e47", "\u2764\ufe0f", "\U0001F600", "\U0001F468\u200d\U0001F469", "\U0001F1EF\U0001F1F5", "\u200b",
		"\x1b[1m", "\x1b[22m", "\x1b[31m", "\x1b[39m", "\x1b[48;5;237m", "\x1b[49m", "\x1b[38;2;1;2;3m", "\x1b[0m", "\x1b[7m", "\x1b[27m", "\x1b[4:3m",
		"\x1b]8;;http://a\x07", "\x1b]8;;\x07"}
	const cols, rows = 24, 6
	frames, translated := 0, 0
	for seed := range uint64(60) {
		rng := rand.New(rand.NewPCG(seed, 7))
		w := newTestWriter(cols, rows)
		pair := newTestPair(cols, rows)
		texts := make([][]string, rows)
		for step := range 80 {
			var parts []string
			if step == 0 || rng.IntN(40) == 0 {
				parts = append(parts, "\x1b[2J")
				for r := range texts {
					texts[r] = nil
				}
			}
			for range 1 + rng.IntN(3) {
				r := rng.IntN(rows)
				switch text := texts[r]; {
				case len(text) > 0 && rng.IntN(5) == 0:
					texts[r] = text[:rng.IntN(len(text))]
				case len(text) > 0 && rng.IntN(3) == 0:
					text[rng.IntN(len(text))] = alphabet[rng.IntN(len(alphabet))]
				case len(text) < cols/2:
					for range 1 + rng.IntN(4) {
						texts[r] = append(texts[r], alphabet[rng.IntN(len(alphabet))])
					}
				default:
					text[rng.IntN(len(text))] = alphabet[rng.IntN(len(alphabet))]
				}
				parts = append(parts, line(r+1, strings.Join(texts[r], "")))
			}
			parts = append(parts, at(1+rng.IntN(rows), 1+rng.IntN(cols)))
			if rng.IntN(2) == 0 {
				parts = append(parts, "\x1b[?25h")
			} else {
				parts = append(parts, "\x1b[?25l")
			}
			in := syncBegin + strings.Join(parts, "") + syncEnd
			out := w.Translate(in)
			if d := pair.Step(in, out); d != "" {
				t.Fatalf("seed %d step %d: %s\n  frame  %q\n  writer %q", seed, step, d, in, out)
			}
			if rng.IntN(10) == 0 {
				if d := pair.Step("", w.Settled()); d != "" {
					t.Fatalf("seed %d step %d: the settle write changed the screen: %s", seed, step, d)
				}
			}
			frames++
		}
		translated += w.Stats().Translated
	}
	if translated < frames/3 {
		t.Errorf("%d of %d frames were translated: the edits do not exercise the writer", translated, frames)
	}
}

// A terminal another program scribbled on heals as it does without the writer: every row the frames rewrote since is whole again
// once the settle write is out, and the rows no frame rewrote keep the scribble in both. A row that got shorter is whole at once: the
// frame's translation writes it whole.
func TestScribbledRowsHealByTheSettleWrite(t *testing.T) {
	w := newTestWriter(20, 5)
	pair := newTestPair(20, 5)
	step := func(in, out string) {
		t.Helper()
		if d := pair.Step(in, out); d != "" {
			t.Fatalf("%s\n  frame  %q\n  writer %q", d, in, out)
		}
	}
	first := frame(line(1, "one"), line(2, "two"), line(3, "three"), line(4, "four"))
	step(first, w.Translate(first))
	step("", w.Settled())
	// The other program writes to the terminal past the writer: both terminals receive it, the writer does not see it.
	scribble := at(1, 1) + "XXXXXXXX" + at(2, 1) + "YYYYYYYY" + at(3, 1) + "ZZZZZZZZ" + at(4, 1) + "WWWWWWWW" + at(1, 1)
	step(scribble, scribble)
	second := frame(line(1, "one"), line(2, "two!"), line(3, "thr"))
	out := strings.Clone(w.Translate(second))
	if !strings.Contains(out, line(3, "thr")) || strings.Count(out, "\x1b[2K") != 1 {
		t.Fatalf("the writer sent %q, want row 3, which got shorter, written whole and no other row whole", out)
	}
	// Until the settle write rows 1 and 2, not written or written in part, still show a part of the scribble; the renderers' bytes
	// healed them at once.
	pair.Step(second, out)
	if pair.Differences != 1 {
		t.Fatalf("%d differences before the settle write, want the one the scribble leaves", pair.Differences)
	}
	settled := w.Settled()
	for _, want := range []string{"\x1b[2Kone" + reset, line(2, "two!")} {
		if !strings.Contains(settled, want) {
			t.Errorf("the settle write %q does not write %q", settled, want)
		}
	}
	if strings.Contains(settled, "four") || strings.Contains(settled, "thr") {
		t.Errorf("the settle write %q writes a row no frame rewrote, or the row the frame's translation wrote whole", settled)
	}
	// Step carried on from the renderers' screen after the difference: apply the settle write to a screen that took the writer's bytes.
	healed, reference := gridterm.New(20, 5, gridterm.Models[0]), gridterm.New(20, 5, gridterm.Models[0])
	for _, s := range []string{started, first, scribble, second} {
		reference.Write(s)
	}
	for _, s := range []string{started, first, scribble, out, settled} {
		healed.Write(s)
	}
	if d := gridterm.Differ(reference, healed); d != "" {
		t.Errorf("after the settle write: %s", d)
	}
	if reference.Row(3) != "WWWWWWWW" {
		t.Errorf("row 4 shows %q: no frame rewrote it, so it keeps the scribble in both", reference.Row(3))
	}
	if again := w.Settled(); again != "" {
		t.Errorf("a second settle write: %q", again)
	}
}

// A terminal that changed size keeps nothing the writer knew: the next frame's rows are written whole, and a frame laid out for the
// old size that addresses a row past the new one is sent as it came.
func TestResizeForgetsTheScreen(t *testing.T) {
	cols, rows := 20, 5
	w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
	w.Translate("\x1b[?1049h\x1b[?7l")
	w.Translate(frame(line(1, "one"), line(5, "five")))
	rows = 3
	old := frame(line(1, "one"), line(5, "five!"))
	if got := w.Translate(old); got != old {
		t.Errorf("a frame for the old size was translated: %q", got)
	}
	repaint := frame("\x1b[2J", line(1, "one"), line(3, "five!"))
	if got := w.Translate(repaint); got != repaint {
		t.Errorf("the repaint was translated: %q", got)
	}
	w.Translate(frame(line(1, "one"), line(3, "five!")))
	if got := w.Translate(frame(line(1, "one"), line(3, "five!"))); got != "" {
		t.Errorf("the frame after the repaint wrote %q", got)
	}
	cols = 30
	if got, want := w.Translate(frame(line(1, "one"))), syncBegin+line(1, "one")+at(1, 1)+"\x1b[?25l"+syncEnd; got != want {
		t.Errorf("after a width change the writer sent %q, want %q", got, want)
	}
}

// The settle write after the terminal changed size writes nothing: rows of the old width written now would wrap on the new screen, and
// the repaint the resize brings is the next frame. Without a resize it writes as before.
func TestSettleAfterAResizeWritesNothing(t *testing.T) {
	cols, rows := 20, 5
	w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
	w.Translate("\x1b[?1049h\x1b[?7l")
	w.Translate(frame("\x1b[2J", line(1, "one"), line(2, "two")))
	w.Translate(frame(line(1, "one"), line(2, "two!")))
	cols = 12
	if got := w.Settled(); got != "" {
		t.Fatalf("the settle write after a resize is %q", got)
	}
	if got := w.Settled(); got != "" {
		t.Fatalf("a second settle write after a resize is %q", got)
	}
	repaint := frame("\x1b[2J", line(1, "one"), line(2, "two!"))
	if got := w.Translate(repaint); got != repaint {
		t.Errorf("the repaint was translated: %q", got)
	}
	w.Translate(frame(line(1, "one"), line(2, "two!!")))
	if got := w.Settled(); got == "" {
		t.Error("no settle write after the repaint and a partial write")
	}
}

// Outside the alternate screen every byte is sent as it came, frames included; leaving it ends the settle write; entering it again
// starts from nothing.
func TestOutsideTheAlternateScreenEveryByteIsSentAsItCame(t *testing.T) {
	w := New(Options{Size: func() (int, int) { return 20, 5 }, Settle: -1})
	inline := frame(line(1, "inline"))
	for range 2 {
		if got := w.Translate(inline); got != inline {
			t.Fatalf("before the alternate screen the writer sent %q", got)
		}
	}
	w.Translate("\x1b[?1049h\x1b[?7l")
	w.Translate(frame(line(1, "one")))
	if got, in := w.Translate(frame(line(1, "one!"))), frame(line(1, "one!")); got == in || w.Stats().Translated == 0 {
		t.Fatalf("in the alternate screen the frame was sent as it came (%d translated): nothing below would test the writer", w.Stats().Translated)
	}
	exit := "\x1b[?25h\x1b[?2004l\x1b[?1049l"
	if got := w.Translate(exit); got != exit {
		t.Fatalf("the exit was sent as %q", got)
	}
	if got := w.Settled(); got != "" {
		t.Errorf("a settle write after the alternate screen was left: %q", got)
	}
	for range 2 {
		if got := w.Translate(inline); got != inline {
			t.Fatalf("after the alternate screen the writer sent %q", got)
		}
	}
	// A resume enters the screen again and repaints: nothing of the last session is assumed.
	w.Translate("\x1b[?1049h\x1b[?7l")
	if got, want := w.Translate(frame(line(1, "one!"))), syncBegin+line(1, "one!")+at(1, 1)+"\x1b[?25l"+syncEnd; got != want {
		t.Errorf("after entering the screen again the writer sent %q, want %q", got, want)
	}
}

// A write that reached the terminal past the writer forgets the rows unless the table says it changes nothing.
func TestOutsideWrites(t *testing.T) {
	w := newTestWriter(20, 5)
	w.Translate(frame(line(1, "one")))
	w.Outside("\x1b]52;c;AAAA\x07")
	if got := w.Translate(frame(line(1, "one"))); got != "" {
		t.Errorf("after a clipboard write past the writer it sent %q", got)
	}
	w.Outside("Launching external editor\n")
	if got, want := w.Translate(frame(line(1, "one"))), syncBegin+line(1, "one")+at(1, 1)+"\x1b[?25l"+syncEnd; got != want {
		t.Errorf("after text written past the writer it sent %q, want %q", got, want)
	}
}

// The settle timer writes under the terminal's lock, once for a settled frame and not before the settle time has passed since it, and not after
// the screen is left.
func TestSettleTimer(t *testing.T) {
	const settle = 20 * time.Millisecond
	locked := make(chan struct{}, 1)
	emitted := make(chan string, 4)
	var settleRecords atomic.Int32
	w := New(Options{
		Size:   func() (int, int) { return 20, 5 },
		Settle: settle,
		Lock:   func() { locked <- struct{}{} },
		Unlock: func() { <-locked },
		Emit:   func(data string) { emitted <- strings.Clone(data) },
		Record: func(kind byte, _, _ string) {
			if kind == RecordSettle {
				settleRecords.Add(1)
			}
		},
	})
	write := func(data string) {
		locked <- struct{}{}
		w.Translate(data)
		<-locked
	}
	for _, data := range gridterm.SplitWrites(started) {
		write(data)
	}
	written := time.Now()
	write(frame(line(2, "hello world")))
	if got, want := <-emitted, syncBegin+line(2, "hello world")+at(1, 1)+syncEnd; got != want {
		t.Errorf("the settle write is %q, want %q", got, want)
	}
	if since := time.Since(written); since < settle {
		t.Errorf("the settle write came %v after the frame, before the settle time of %v", since, settle)
	}
	// One settle write for a settled frame: while no frame comes, none follows it, and the record log holds one record of its kind.
	select {
	case again := <-emitted:
		t.Errorf("a second settle write for the same frame: %q", again)
	case <-time.After(5 * settle):
	}
	if n := settleRecords.Load(); n != 1 {
		t.Errorf("%d records of the settle write's kind for one settled frame, want 1", n)
	}
	write(frame(line(2, "hello world!")))
	write("\x1b[?1049l")
	select {
	case got := <-emitted:
		t.Errorf("a settle write after the screen was left: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// Stop cancels the settle timer under the terminal's lock: a timer waiting writes nothing after it, and a frame written after it arms
// none; the writer still translates.
func TestStopEndsTheSettleTimer(t *testing.T) {
	locked := make(chan struct{}, 1)
	emitted := make(chan string, 4)
	w := New(Options{
		Size:   func() (int, int) { return 20, 5 },
		Settle: 20 * time.Millisecond,
		Lock:   func() { locked <- struct{}{} },
		Unlock: func() { <-locked },
		Emit:   func(data string) { emitted <- strings.Clone(data) },
	})
	write := func(data string) string {
		locked <- struct{}{}
		defer func() { <-locked }()
		return strings.Clone(w.Translate(data))
	}
	for _, data := range gridterm.SplitWrites(started) {
		write(data)
	}
	write(frame(line(2, "hello world")))
	w.Stop()
	w.Stop()
	select {
	case got := <-emitted:
		t.Fatalf("a settle write after Stop: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
	if got := write(frame(line(2, "hello world!"))); !strings.HasPrefix(got, syncBegin) || !strings.Contains(got, "!") {
		t.Errorf("a frame after Stop translated to %q, want the changed cells", got)
	}
	select {
	case got := <-emitted:
		t.Fatalf("a settle write for a frame written after Stop: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// Drop ends the writer: every later write comes back as it was given, a frame that changes nothing included; Active is false on
// the alternate screen too; no row waits for a settle write; and nothing more is recorded.
func TestDropSendsEveryWriteAsItCame(t *testing.T) {
	records := 0
	locks := 0
	w := New(Options{
		Size:   func() (int, int) { return 20, 5 },
		Settle: -1,
		Lock:   func() { locks++ },
		Unlock: func() { locks-- },
		Record: func(byte, string, string) { records++ },
	})
	for _, data := range gridterm.SplitWrites(started) {
		w.Translate(data)
	}
	w.Translate(frame(line(2, "hello world")))
	if got := w.Translate(frame(line(2, "hello world!"))); got == frame(line(2, "hello world!")) {
		t.Fatalf("before Drop the writer sent the frame as it came: the test needs a partial write")
	}
	if !w.Active() {
		t.Fatalf("not active on the alternate screen before Drop")
	}
	w.Drop()
	if locks != 0 {
		t.Fatalf("Drop left the terminal's lock held %d times", locks)
	}
	if w.Active() {
		t.Errorf("active after Drop")
	}
	if got := w.Settled(); got != "" {
		t.Errorf("a settle write after Drop: %q", got)
	}
	before := records
	for _, data := range []string{frame(line(2, "hello world!")), frame(line(2, "hello world!")), "\x1b[?1049l", "\x1b[?1049h", frame(line(1, "again"))} {
		if got := w.Translate(data); got != data {
			t.Errorf("after Drop %q was sent as %q", data, got)
		}
		if w.Active() {
			t.Errorf("active after Drop and %q", data)
		}
	}
	if records != before {
		t.Errorf("%d writes recorded after Drop", records-before)
	}
}

// The records of a log read back as they were appended.
func TestRecords(t *testing.T) {
	var log []byte
	log = AppendRecord(log, RecordOther, "\x1b[?1049h", "\x1b[?1049h")
	log = AppendRecord(log, RecordFrame, "in", "")
	log = AppendRecord(log, RecordSettle, "", "out")
	var got []string
	for {
		kind, in, out, rest, ok := NextRecord(log)
		if !ok {
			break
		}
		got = append(got, fmt.Sprintf("%c %q %q", kind, in, out))
		log = rest
	}
	if want := []string{`R "\x1b[?1049h" "\x1b[?1049h"`, `F "in" ""`, `S "" "out"`}; strings.Join(got, "|") != strings.Join(want, "|") || len(log) != 0 {
		t.Errorf("records %q and %d bytes left, want %q", got, len(log), want)
	}
	if _, _, _, _, ok := NextRecord(bytes.Repeat([]byte{'F'}, 12)); ok {
		t.Error("a record cut short was read")
	}
}

// The word-wise scans find what the byte-wise ones do.
func TestTextScans(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		// Short texts mostly, and some longer than the sixty-four bytes commonSuffix compares at a time.
		size := rng.IntN(40)
		if rng.IntN(4) == 0 {
			size = rng.IntN(300)
		}
		b := make([]byte, size)
		for i := range b {
			switch rng.IntN(12) {
			case 0:
				b[i] = byte(rng.IntN(0x20))
			case 1:
				b[i] = 0x7f
			case 2:
				b[i] = 0x80 + byte(rng.IntN(0x80))
			case 3:
				b[i] = []byte{0x1f, 0x20, 0x7e, 0x80, 0xff}[rng.IntN(5)]
			default:
				b[i] = 0x20 + byte(rng.IntN(0x5f))
			}
		}
		s := string(b)
		from := rng.IntN(len(s) + 1)
		text, ascii := from, from
		for text < len(s) && s[text] >= 0x20 && s[text] != 0x7f {
			text++
		}
		for ascii < len(s) && s[ascii] >= 0x20 && s[ascii] < 0x7f {
			ascii++
		}
		if got := textEnd(s, from); got != text {
			t.Fatalf("textEnd(%q, %d) = %d, want %d", s, from, got, text)
		}
		if got := asciiEnd(s, from); got != ascii {
			t.Fatalf("asciiEnd(%q, %d) = %d, want %d", s, from, got, ascii)
		}
		other := []byte(s)
		if len(other) > 0 && rng.IntN(2) == 0 {
			other[rng.IntN(len(other))] ^= 1
		}
		prefix, suffix := 0, 0
		for prefix < len(b) && b[prefix] == other[prefix] {
			prefix++
		}
		for suffix < len(b) && b[len(b)-1-suffix] == other[len(b)-1-suffix] {
			suffix++
		}
		if got := commonPrefix(b, other); got != prefix {
			t.Fatalf("commonPrefix(%q, %q) = %d, want %d", b, other, got, prefix)
		}
		if got := commonSuffix(b, other[min(1, len(other)):]); len(other) > 0 && got != min(suffix, len(other)-1) {
			t.Fatalf("commonSuffix(%q, %q) = %d, want %d", b, other[1:], got, min(suffix, len(other)-1))
		}
	}
}

// lineEndByTokens is lineEnd token by token.
func lineEndByTokens(body string, j int) int {
	for j < len(body) {
		k, e := nextToken(body, j)
		if k == tokenText || k == tokenCSI && body[e-1] == 'm' && body[j+2] != '>' && body[j+2] != '?' || k == tokenOSC && strings.HasPrefix(body[j:e], "\x1b]8;") {
			j = e
			continue
		}
		break
	}
	return j
}

var lineAlphabet = []string{"a", "b", "c", " ", " ", "x", "é", "Ж", "\u00b1", "\u00d7", "\u2500", "\u2500", "\u2502", "\u2588", "\u28ff", "\u65e5", "\u672c", "\uff21",
	"e\u0301", "\u0e01\u0e47", "\u2764\ufe0f", "\U0001F600", "\U0001F468\u200d\U0001F469", "\U0001F1EF\U0001F1F5", "\u200b",
	"\x1b[1m", "\x1b[22m", "\x1b[31m", "\x1b[39m", "\x1b[48;5;237m", "\x1b[49m", "\x1b[38;2;1;2;3m", "\x1b[0m", "\x1b[7m", "\x1b[27m", "\x1b[4:3m",
	"\x1b]8;;http://a\x07", "\x1b]8;;\x07", "\x1b]8;;\x1b\\"}

// randomLine is a line of pieces of lineAlphabet, sometimes with something a line cannot hold.
func randomLine(rng *rand.Rand, pieces int) string {
	var b strings.Builder
	for range pieces {
		if rng.IntN(60) == 0 {
			b.WriteString([]string{"\t", "\x1b[5A", "\x1b[>4m", "\x1b[?25l", "\x1b]0;t\x07", "\x1b", "\x1b[", "\x1b[1 m", "\x7f", "\xe2\x94", "\xff"}[rng.IntN(11)])
		}
		b.WriteString(lineAlphabet[rng.IntN(len(lineAlphabet))])
	}
	return b.String()
}

func TestLineEnd(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 20000 {
		body := randomLine(rng, rng.IntN(12)) + []string{"", "\x1b[3;1H", "\n", "\x1b[?25l"}[rng.IntN(4)] + randomLine(rng, rng.IntN(3))
		if got, want := lineEnd(body, 0), lineEndByTokens(body, 0); got != want {
			t.Fatalf("lineEnd(%q) = %d, want %d", body, got, want)
		}
	}
}

// referenceCell is a cell as the plain reading of a line gives it.
type referenceCell struct {
	off, end, col, width, over int
	pen                        pen
	certain                    bool
}

// referenceParse reads a line cell by cell from its start, sharing nothing with another line: what parse must give whatever it takes
// over from the row's last line.
func referenceParse(line string, cols int) (cells []referenceCell, width int, end pen, ok bool) {
	var p pen
	col, over, pens := 0, 0, 1
	for i := 0; i < len(line); {
		c := line[i]
		if c == 0x1b {
			kind, e := nextToken(line, i)
			seq := line[i:e]
			switch {
			case kind == tokenCSI && seq[len(seq)-1] == 'm':
				before := p
				if !p.applySGR(seq[2 : len(seq)-1]) {
					return nil, 0, pen{}, false
				}
				if p != before {
					if pens++; pens > maxPens {
						return nil, 0, pen{}, false
					}
				}
			case kind == tokenOSC && (seq == linkCloseBEL || seq == linkCloseST):
			default:
				return nil, 0, pen{}, false
			}
			i = e
			continue
		}
		if c < 0x20 || c == 0x7f {
			return nil, 0, pen{}, false
		}
		cluster := line[i : i+1]
		if c >= 0x80 || i+1 < len(line) && line[i+1] >= 0x80 {
			cluster, _ = widthx.FirstGrapheme(line[i:])
		}
		wd := widthx.GraphemeWidth(cluster)
		if len(cluster) == 0 || wd < 1 || wd > 2 || hasC1(cluster) || utf8.RuneCountInString(cluster) > 1 {
			return nil, 0, pen{}, false
		}
		certain := wd == 1 && certainWidth(cluster)
		if !certain {
			over += 2 - wd
		}
		cells = append(cells, referenceCell{i, i + len(cluster), col, wd, over, p, certain})
		i += len(cluster)
		if col += wd; col > cols {
			return nil, 0, pen{}, false
		}
	}
	return cells, col, p, true
}

// Whatever a line shares with the row's last line, at its start, at its end or as a whole with another row, parse gives the cells
// the plain reading of the line gives.
func TestParseMatchesThePlainReading(t *testing.T) {
	check := func(name, line string, old *row, cols int, symbols *symbolWidths) *row {
		t.Helper()
		got := &row{}
		shared, same, ok := got.parse(line, old, cols, 0, symbols)
		want, width, end, wantOK := referenceParse(line, cols)
		if len(line) > maxLine {
			wantOK = false
		}
		if ok != wantOK {
			t.Fatalf("%s: parse(%q) ok %v, want %v", name, line, ok, wantOK)
		}
		if !ok {
			return nil
		}
		if len(got.cells) != len(want) || got.width != width || got.endPen != end || string(got.line) != line {
			t.Fatalf("%s: parse(%q) gave %d cells, width %d, end pen %+v, want %d, %d, %+v", name, line, len(got.cells), got.width, got.endPen, len(want), width, end)
		}
		uncertain := len(want)
		for i, w := range want {
			c := got.cells[i]
			if c.off() != w.off || c.end() != w.end || c.col() != w.col || c.width() != w.width || c.over() != w.over || c.certain() != w.certain || got.pens[c.pen()] != w.pen {
				t.Fatalf("%s: parse(%q) after %q: cell %d is off %d end %d col %d width %d over %d certain %v pen %+v, want %+v (shared %d, same %d)", name, line, old.line, i, c.off(), c.end(), c.col(), c.width(), c.over(), c.certain(), got.pens[c.pen()], w, shared, same)
			}
			if !w.certain && uncertain == len(want) {
				uncertain = i
			}
		}
		if got.uncertain != uncertain {
			t.Fatalf("%s: parse(%q) after %q: first uncertain cell %d, want %d", name, line, old.line, got.uncertain, uncertain)
		}
		if ink := got.inkBefore(len(got.cells)); got.inked != ink {
			t.Fatalf("%s: parse(%q) after %q: %d cells through the last that is not a default blank, the cells say %d", name, line, old.line, got.inked, ink)
		}
		if old != nil && old.known {
			for i := range shared {
				if !sameCell(old, i, got, i) {
					t.Fatalf("%s: parse(%q) after %q: cell %d is not the row's, of %d shared", name, line, old.line, i, shared)
				}
			}
			for k := range same {
				if !sameCell(old, len(old.cells)-1-k, got, len(got.cells)-1-k) {
					t.Fatalf("%s: parse(%q) after %q: cell %d from the end is not the row's, of %d the same", name, line, old.line, k, same)
				}
			}
		}
		got.known = true
		return got
	}
	// The lines of the recorded streams, each after the line the same row held before.
	var symbols symbolWidths
	spliced := 0
	for name, stream := range testStreams(t) {
		rows := map[string]*row{}
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
				old := rows[address]
				if old == nil {
					old = &row{}
				}
				if parsed := check(name, write[i:j], old, 160, &symbols); parsed != nil {
					if _, same, _ := (&row{}).parse(write[i:j], old, 160, 0, &symbols); same > 0 {
						spliced++
					}
					rows[address] = parsed
				}
				i = j
			}
		}
	}
	if spliced < 500 {
		t.Errorf("%d lines of the streams took cells over at their end: the streams do not exercise it", spliced)
	}
	// Random lines, each after a line it shares a start, an end, both or nothing with.
	rng := rand.New(rand.NewPCG(5, 6))
	for range 30000 {
		cols := []int{8, 24, 80}[rng.IntN(3)]
		head, middle, tail := randomLine(rng, rng.IntN(8)), randomLine(rng, rng.IntN(6)), randomLine(rng, rng.IntN(8))
		old := check("random", head+middle+tail, &row{}, cols, &symbols)
		if old == nil {
			old = &row{}
		}
		for _, line := range []string{head + randomLine(rng, rng.IntN(6)) + tail, head + tail, head + middle + tail + randomLine(rng, 2), randomLine(rng, 2) + middle + tail, head + middle + tail, randomLine(rng, 5)} {
			check("random", line, old, cols, &symbols)
		}
	}
}
