package widthx

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// finishRow stands for a caller's per-row padding: a pure function of the row.
func finishRow(row string) string { return "<" + row + "|" + string(rune('a'+len(row)%26)) + ">" }

func wantWrapped(text string, width int, finish func(string) string) []string {
	rows := WrapTextWithAnsi(text, width)
	if finish == nil {
		return rows
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = finish(row)
	}
	return out
}

// A text wrapped through the cache gives the rows WrapTextWithAnsi gives, whatever the last text and width were: lines that
// open an SGR or hyperlink state that a later line closes (or fails to close), edits in the middle that change the state the
// lines after them start in, wide and combining characters at the wrap column, tabs, lone and paired carriage returns, and a
// width that changes between calls.
func TestLineWrapCacheMatchesWrapTextWithAnsi(t *testing.T) {
	fragments := []string{
		"alpha ", "beta", " ", "  ", "\x1b[31m", "\x1b[0m", "\x1b[1m", "\x1b[22m", "\x1b[38;5;123m", "\x1b[39m", "\x1b[48;2;1;2;3m",
		"\x1b]8;;https://e.com\x07", "\x1b]8;;\x07", "\x1b]8;id=1;https://f.org\x1b\\", "\x1b]8;;\x1b\\",
		"漢字", "漢", "e\u0301", "\u200d", "x", "wordwordwordwordwordwordword", "\n", "\n", "\n", "\r\n", "\r", "\t", "😀", "👨‍👩‍👧", "...",
	}
	rng := rand.New(rand.NewPCG(7, 11))
	random := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteString(fragments[rng.IntN(len(fragments))])
		}
		return b.String()
	}
	for trial := range 400 {
		var cache LineWrapCache
		finish := finishRow
		if trial%3 == 0 {
			finish = nil
		}
		text := random(1 + rng.IntN(40))
		width := 3 + rng.IntN(30)
		for step := range 12 {
			got := cache.Wrap(text, width, finish)
			want := wantWrapped(text, width, finish)
			if !slices.Equal(got, want) {
				t.Fatalf("trial %d step %d width %d: text %q\n got %q\nwant %q", trial, step, width, text, got, want)
			}
			switch rng.IntN(6) {
			case 0, 1: // grow at the end, as a streamed call does
				text += random(1 + rng.IntN(6))
			case 2: // edit in the middle
				at := rng.IntN(len(text) + 1)
				for at > 0 && at < len(text) && text[at]&0xC0 == 0x80 {
					at--
				}
				text = text[:at] + random(1+rng.IntN(3)) + text[at:]
			case 3: // cut the tail
				at := rng.IntN(len(text) + 1)
				for at > 0 && at < len(text) && text[at]&0xC0 == 0x80 {
					at--
				}
				text = text[:at]
			case 4: // the width changes
				width = 1 + rng.IntN(40)
			case 5: // the same text again
			}
		}
	}
}

// The cache serves the lines a change at the end of the text leaves alone: the cost of a call is that of the changed lines, not
// of the text, and a text whose first line changes costs what the first wrap did.
func TestLineWrapCacheWrapsOnlyTheLinesThatChanged(t *testing.T) {
	var lines []string
	for i := range 120 {
		lines = append(lines, "\x1b[38;5;"+string(rune('0'+i%10))+"mexport function step(input) { return input.reduce((a, b) => a + b, 0); }\x1b[39m")
	}
	base := strings.Join(lines, "\n")
	const width = 80
	var cache LineWrapCache
	cache.Wrap(base+"\ntail 0", width, finishRow)
	n := 0
	tail := func() float64 {
		return testing.AllocsPerRun(20, func() {
			n++
			cache.Wrap(base+"\ntail "+strings.Repeat("x", n%50), width, finishRow)
		})
	}()
	whole := testing.AllocsPerRun(20, func() {
		for _, row := range WrapTextWithAnsi(base+"\ntail 0", width) {
			_ = finishRow(row)
		}
	})
	if tail*10 > whole {
		t.Errorf("a change in the last line allocates %.0f, a whole wrap %.0f: unchanged lines are wrapped again", tail, whole)
	}
	// Alternating a text with the same text after one more line shifts every line to another index: nothing is served.
	shifted := testing.AllocsPerRun(20, func() {
		n++
		if n%2 == 0 {
			cache.Wrap(base, width, finishRow)
		} else {
			cache.Wrap("one more line\n"+base, width, finishRow)
		}
	})
	if shifted*4 < whole {
		t.Errorf("a text that shares no line index with the last one allocates %.0f, a whole wrap %.0f", shifted, whole)
	}
}

// A different width, and a Reset, serve nothing from the last text.
func TestLineWrapCacheWidthAndResetServeNothing(t *testing.T) {
	text := "one two three four five six seven\neight nine ten eleven twelve"
	var cache LineWrapCache
	cache.Wrap(text, 10, finishRow)
	if got, want := cache.Wrap(text, 12, finishRow), wantWrapped(text, 12, finishRow); !slices.Equal(got, want) {
		t.Fatalf("width change: got %q want %q", got, want)
	}
	flip := strings.ToUpper
	cache.Reset()
	if got, want := cache.Wrap(text, 12, flip), wantWrapped(text, 12, flip); !slices.Equal(got, want) {
		t.Fatalf("after Reset: got %q want %q", got, want)
	}
}
