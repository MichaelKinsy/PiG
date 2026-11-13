package tui

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func textRowCacheFragments() []string {
	return []string{
		"alpha ", "beta", " ", "  ", "\x1b[31m", "\x1b[0m", "\x1b[1m", "\x1b[22m", "\x1b[38;5;123m", "\x1b[39m", "\x1b[48;2;1;2;3m",
		"\x1b]8;;https://e.com\x07", "\x1b]8;;\x07", "漢字", "漢", "e\u0301", "x", "wordwordwordwordwordwordword", "\n", "\n", "\n",
		"\r\n", "\t", "\t\t", "😀", "...", "export function step(a, b) { return a + b; }",
	}
}

// A Text that is drawn again after SetText changes its text gives the lines a fresh Text of that text gives: whatever the
// ANSI state its unchanged lines start in, the width and the padding it is drawn with, the invalidations between the draws and
// a background function.
func TestTextRenderAfterSetTextMatchesAFreshText(t *testing.T) {
	fragments := textRowCacheFragments()
	rng := rand.New(rand.NewPCG(3, 5))
	random := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteString(fragments[rng.IntN(len(fragments))])
		}
		return b.String()
	}
	bg := func(s string) string { return "\x1b[44m" + s + "\x1b[49m" }
	for trial := range 300 {
		text := random(1 + rng.IntN(40))
		live := NewPaddedText(text, rng.IntN(3), rng.IntN(2), nil)
		width := 4 + rng.IntN(40)
		for step := range 14 {
			var want []string
			fresh := NewPaddedText(live.Content, live.PaddingX, live.PaddingY, live.CustomBgFn)
			want = fresh.Render(width)
			if got := live.Render(width); !slices.Equal(got, want) {
				t.Fatalf("trial %d step %d width %d paddingX %d text %q\n got %q\nwant %q", trial, step, width, live.PaddingX, live.Content, got, want)
			}
			switch rng.IntN(10) {
			case 0, 1, 2: // grow at the end
				text += random(1 + rng.IntN(5))
				live.SetText(text)
			case 3: // edit in the middle
				at := rng.IntN(len(text) + 1)
				for at > 0 && at < len(text) && text[at]&0xC0 == 0x80 {
					at--
				}
				text = text[:at] + random(1+rng.IntN(3)) + text[at:]
				live.SetText(text)
			case 4:
				width = 1 + rng.IntN(50)
			case 5:
				live.SetPaddingX(rng.IntN(4))
			case 6: // a field assigned without a setter, then a new text
				live.PaddingX = rng.IntN(4)
				text += random(1)
				live.SetText(text)
			case 7:
				live.Invalidate()
			case 8: // the background function changes, through the setter or the field
				if rng.IntN(2) == 0 {
					live.SetCustomBgFn(bg)
				} else {
					live.CustomBgFn = nil
					live.Invalidate()
				}
			case 9:
				live.CustomBgFn = bg
				text += random(1)
				live.SetText(text)
			}
		}
	}
}

// The frame of a streaming card changes the end of its text: wrapping and padding the lines before it again is the cost of the
// frame. A Text whose last line changes costs a fraction of a fresh Text of the same lines.
func TestTextRenderAfterSetTextKeepsTheRowsOfUnchangedLines(t *testing.T) {
	var lines []string
	for i := range 12 {
		lines = append(lines, "\x1b[38;5;"+string(rune('1'+i%8))+"mexport function step(input: number[]): number { return input.reduce((a, b) => a + b * 7, 1); }\x1b[39m")
	}
	head := strings.Join(lines, "\n") + "\n... (3 more lines, "
	const width = 160
	text := NewPaddedText("", 0, 0, nil)
	text.SetText(head + "1 total)")
	text.Render(width)
	n := 0
	changed := testing.AllocsPerRun(50, func() {
		n++
		text.SetText(head + strings.Repeat("9", n%40) + " total)")
		text.Render(width)
	})
	fresh := testing.AllocsPerRun(50, func() {
		n++
		NewPaddedText(head+strings.Repeat("9", n%40)+" total)", 0, 0, nil).Render(width)
	})
	if changed*3 > fresh {
		t.Errorf("a Text whose last line changed allocates %.0f, a fresh Text of the same lines %.0f: unchanged lines are wrapped and padded again", changed, fresh)
	}
}

// A Text that is drawn once and never changes holds no rows beyond its own cache, and one that changes keeps those of its last text only.
func TestTextKeepsRowsOnlyAfterItsTextChanged(t *testing.T) {
	text := NewText("one\ntwo")
	text.Render(20)
	if text.wrapCache != nil {
		t.Fatal("a Text drawn once keeps wrapped rows")
	}
	text.SetText("one\ntwo\nthree")
	if text.wrapCache == nil {
		t.Fatal("a drawn Text whose text changed keeps no rows")
	}
	text.Invalidate()
	if text.wrapCache != nil {
		t.Fatal("Invalidate keeps the rows")
	}
}

// A width and a padding that keep the width of the content (20 columns with no margin, 22 with a margin of one) still draw other rows:
// the rows a Text kept for its unchanged lines are those of the width and padding they were made for.
func TestTextRowsFollowAWidthAndPaddingThatKeepTheContentWidth(t *testing.T) {
	text := NewPaddedText("first line\nsecond line", 0, 0, nil)
	text.Render(20)
	text.SetText("first line\nsecond line\nthird")
	text.Render(20)
	text.PaddingX = 1
	text.SetText("first line\nsecond line\nthird line")
	want := NewPaddedText("first line\nsecond line\nthird line", 1, 0, nil).Render(22)
	if got := text.Render(22); !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
