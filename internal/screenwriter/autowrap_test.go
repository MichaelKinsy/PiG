// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// What a write leaves known of autowrap.
func TestOutsideReportsAutowrap(t *testing.T) {
	for data, want := range map[string]int{
		"\x1b[?7l":                   1,
		"\x1b[?7h":                   -1,
		"\x1b[?7h\x1b[?7l":           1,
		"\x1b[?7l\x1b[?7h":           -1,
		"\x1b[?1049h\x1b[?7l\x1b[2J": 1,
		"\x1b[?7l\x1b[?1049h":        -1,
		"\x1b[?1049l":                -1,
		"\x1b[?1047h":                -1,
		"\x1b[?47l":                  -1,
		"\x1b[?25;7l":                1,
		"\x1b[?7r":                   -1,
		"\x1b[?r":                    -1,
		"\x1b[!p":                    -1,
		"\x1bc":                      -1,
		"\x1b[?25l\x1b]0;title\x07":  0,
		"\x1b[2;5r":                  0,
		"text":                       0,
	} {
		if _, _, _, wrap := outside(data, true); wrap != want {
			t.Errorf("outside(%q) leaves autowrap %d, want %d", data, wrap, want)
		}
	}
}

// Frames are translated only while autowrap is known to be off. Whatever makes it unknown, the frames after it are sent as they
// came, and the screens stay the renderers'. The fullscreen renderer's stop turns autowrap on in a synchronized update of its own
// and leaves the alternate screen in its next write, so no frame is written between the two; the case here is the hypothetical one
// of a frame that came between them.
func TestFramesAreTranslatedOnlyWhileAutowrapIsKnownToBeOff(t *testing.T) {
	const cols, rows = 20, 5
	const enter = "\x1b[?1049h\x1b[?7l"
	stop := syncBegin + "\x1b[?1006l\x1b[?1004l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[?7h" + syncEnd
	for _, c := range []struct {
		name   string
		before []string // the writes before the frames; one that starts with "past:" reaches the terminal past the writer
		off    bool     // autowrap is known to be off after them
	}{
		{"the alternate screen entered and autowrap turned off", []string{enter}, true},
		{"nothing written before the frames", nil, false},
		{"the alternate screen entered and nothing said of autowrap", []string{"\x1b[?1049h"}, false},
		{"autowrap turned on", []string{enter, "\x1b[?7h"}, false},
		{"hypothetical: the renderer's stop turned autowrap on and the alternate screen is still shown", []string{enter, stop}, false},
		{"the terminal reset", []string{enter, "\x1bc"}, false},
		{"the terminal reset softly", []string{enter, "\x1b[!p"}, false},
		{"saved modes restored", []string{enter, "\x1b[?7r"}, false},
		{"the alternate screen left and entered again", []string{enter, "\x1b[?1049l", "\x1b[?1049h"}, false},
		{"another alternate screen switched to", []string{enter, "\x1b[?1047h"}, false},
		{"autowrap turned on past the writer", []string{enter, "past:\x1b[?7h"}, false},
		{"autowrap turned off past the writer", []string{"\x1b[?1049h", "past:\x1b[?7l"}, false},
		{"autowrap turned on, then off", []string{enter, "\x1b[?7h", "\x1b[?7l"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
			pair := gridterm.NewPair(cols, rows)
			for _, write := range c.before {
				if past, ok := strings.CutPrefix(write, "past:"); ok {
					w.Outside(past)
					pair.Step(past, past)
					continue
				}
				if out := w.Translate(write); out != write {
					t.Fatalf("%q, which is no frame, was sent as %q", write, out)
				}
				pair.Step(write, write)
			}
			asItCame := 0
			for i := range 5 {
				in := frame(line(2, fmt.Sprintf("count %d", i)+strings.Repeat(" ", cols-7)))
				if i == 0 {
					in = frame("\x1b[2J", line(1, ""), line(2, "count 0"+strings.Repeat(" ", cols-7)))
				}
				out := strings.Clone(w.Translate(in))
				if d := pair.Step(in, out); d != "" && w.Active() {
					t.Fatalf("frame %d: %s\n  frame  %q\n  writer %q", i, d, in, out)
				}
				if out == in {
					asItCame++
				}
			}
			stats := w.Stats()
			switch {
			case c.off && (stats.Wrapping != 0 || stats.Translated == 0):
				t.Errorf("autowrap is known to be off, and of 5 frames %d were translated and %d sent as they came for autowrap", stats.Translated, stats.Wrapping)
			case !c.off && w.Active() && (asItCame != 5 || stats.Wrapping != 5 || stats.Translated != 0):
				t.Errorf("autowrap is not known to be off, and of 5 frames %d were sent as they came (%d for autowrap, %d translated)", asItCame, stats.Wrapping, stats.Translated)
			case !c.off && !w.Active() && asItCame != 5:
				t.Errorf("the alternate screen is not shown, and of 5 frames %d were sent as they came", asItCame)
			}
		})
	}
}

// Every sequence that changes autowrap is read where it stands between sequences the writer does not recognise, in a write that
// passes through the writer and in one that passes it by: the frames after it are sent as they came. The unrecognised sequences
// alone change nothing of what is known of autowrap: the frames after them are translated, their rows written whole first.
func TestAutowrapIsReadAmongUnrecognisedSequences(t *testing.T) {
	const cols, rows = 20, 5
	const enter = "\x1b[?1049h\x1b[?7l"
	// A kitty graphics command, a media-copy sequence and a device control string: none is in the writer's tables.
	before, after := "\x1b_Ga=d\x1b\\"+"\x1b[5i", "\x1bP1$r0m\x1b\\"+"\x1b[4i"
	frames := func(w *Writer) (asItCame int) {
		for i := range 5 {
			in := frame(line(2, fmt.Sprintf("count %d", i)+strings.Repeat(" ", cols-7)))
			if w.Translate(in) == in {
				asItCame++
			}
		}
		return asItCame
	}
	for _, past := range []bool{false, true} {
		for _, sequence := range []string{"", "\x1b[?7h", "\x1b[?1049l\x1b[?1049h", "\x1b[?47h", "\x1b[?1047l", "\x1b[?7r", "\x1b[?r", "\x1b[!p", "\x1bc"} {
			w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
			w.Translate(enter)
			w.Translate(frame("\x1b[2J", line(1, "")))
			w.Translate(frame("\x1b[2J", line(1, "")))
			if write := before + sequence + after; past {
				w.Outside(write)
			} else if out := w.Translate(write); out != write {
				t.Fatalf("%q was sent as %q", write, out)
			}
			asItCame, stats := frames(w), w.Stats()
			switch {
			case sequence == "" && (stats.Wrapping != 0 || stats.Translated == 0):
				t.Errorf("past the writer %v: after unrecognised sequences alone, of 5 frames %d were translated and %d sent as they came for autowrap", past, stats.Translated, stats.Wrapping)
			case sequence != "" && (asItCame != 5 || stats.Translated != 0):
				t.Errorf("past the writer %v: after %q among unrecognised sequences, of 5 frames %d were sent as they came and %d translated", past, sequence, asItCame, stats.Translated)
			}
		}
	}
}

// When autowrap is turned off again the writer knows no row: the next frame's rows are written whole, as the frame wrote them, and
// none in place onto a row written while autowrap was on.
func TestTranslationResumesWithWholeRows(t *testing.T) {
	const cols, rows = 20, 5
	text := func(s string) string { return s + strings.Repeat(" ", cols-len(s)) }
	w := New(Options{Size: func() (int, int) { return cols, rows }, Settle: -1})
	pair := gridterm.NewPair(cols, rows)
	step := func(in string) string {
		t.Helper()
		out := strings.Clone(w.Translate(in))
		if d := pair.Step(in, out); d != "" {
			t.Fatalf("%s\n  write  %q\n  writer %q", d, in, out)
		}
		return out
	}
	for _, write := range gridterm.SplitWrites(started) {
		step(write)
	}
	step(frame(line(2, text("two")), line(3, text("three"))))
	if out := step(frame(line(2, text("tWo")), line(3, text("three")))); strings.Contains(out, "\x1b[2K") {
		t.Fatalf("with autowrap off the changed row was written whole: %q", out)
	}
	step("\x1b[?7h")
	wrapped := frame(line(2, text("tWo!")), line(3, text("three")))
	if out := step(wrapped); out != wrapped {
		t.Fatalf("with autowrap on the frame was sent as %q", out)
	}
	step("\x1b[?7l")
	if out := step(frame(line(2, text("tWo!!")), line(3, text("three")))); strings.Count(out, "\x1b[2K") != 2 {
		t.Errorf("the first frame after autowrap was turned off wrote %d of its 2 rows whole: %q", strings.Count(out, "\x1b[2K"), out)
	}
	step(frame(line(2, text("tWo!!")), line(3, text("three"))))
	if out := step(frame(line(2, text("tWO!!")), line(3, text("three")))); strings.Contains(out, "\x1b[2K") || out == "" {
		t.Errorf("translation did not resume in place: %q", out)
	}
}
