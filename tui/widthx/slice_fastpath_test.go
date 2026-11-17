package widthx

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// sliceWithWidthReference is SliceWithWidth as it was before the printable ASCII stretches were placed by arithmetic: every non-ANSI run goes
// through the grapheme iterator, and the end of a run is found one byte at a time. The differential test below holds the fast path to it.
func sliceWithWidthReference(line string, startCol, length int, strict bool) SliceResult {
	if length <= 0 {
		return SliceResult{}
	}
	endCol := startCol + length
	var result strings.Builder
	resultWidth := 0
	currentCol := 0
	pendingAnsi := ""

	for i := 0; i < len(line); {
		if n := ExtractAnsi(line, i); n > 0 {
			code := line[i : i+n]
			if currentCol >= startCol && currentCol < endCol {
				result.WriteString(pendingAnsi)
				result.WriteString(code)
				pendingAnsi = ""
			} else if currentCol < startCol && !strings.HasPrefix(code, "\x1b_") {
				pendingAnsi += code
			}
			i += n
			continue
		}
		end := i
		for end < len(line) {
			if ExtractAnsi(line, end) > 0 {
				break
			}
			end++
		}
		run := line[i:end]
		gs := newGraphemeIter(run)
		broken := false
		for gs.Next() {
			seg := gs.Str()
			w := graphemeWidthFast(seg)
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strict || currentCol+w <= endCol
			if inRange && fits {
				if pendingAnsi != "" {
					result.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				result.WriteString(seg)
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				broken = true
				break
			}
		}
		i = end
		if broken {
			break
		}
	}
	return SliceResult{Text: result.String(), Width: resultWidth}
}

// slicePieces are the building blocks of the random lines: printable ASCII, escape sequences (SGR, OSC 8 with BEL, APC cursor marker, an unfinished
// escape that is no code), wide and combining characters, variation selectors, joiners, emoji sequences, regional indicators, Hangul, tabs and controls.
var slicePieces = []string{
	"a", "b", "Z", "0", " ", "~", "!", "hello", "the quick brown fox", "  ", "│", "─", "┃", "é", "ß", "→", "…",
	"\x1b[0m", "\x1b[31m", "\x1b[1;38;5;196m", "\x1b[48;2;1;2;3m", "\x1b[K", "\x1b[2G", "\x1b]8;;https://example.test\x07", "\x1b]8;;\x07", "\x1b_pi:c\x07",
	"\x1b", "\x1b[", "\x1b[1;2", "\x1bX", "\x1b]0;title",
	"中", "文", "あ", "ｱ", "ＡＢ", "한", "\u1100\u1161\u11a8", "e\u0301", "a\u0300\u0301", "\u0301", "\u200d", "a\u200db", "\u2764\ufe0f", "\u2764", "1\ufe0f\u20e3", "#\ufe0f",
	"\U0001F468\u200d\U0001F469\u200d\U0001F467", "\U0001F600", "\U0001F1FA\U0001F1F8", "\U0001F1FA", "\U0001F3FD", "\U0001F44D\U0001F3FD",
	"\t", "\r", "\n", "\x00", "\x7f", "\u00ad", "\u200b", "\u2028", "\xff", "\xc3",
}

func randomSliceLine(r *rand.Rand) string {
	var b strings.Builder
	for range r.IntN(24) {
		if r.IntN(3) == 0 {
			// A long printable run, where the fast path does its work.
			for range 1 + r.IntN(60) {
				b.WriteByte(byte(0x20 + r.IntN(0x5f)))
			}
			continue
		}
		b.WriteString(slicePieces[r.IntN(len(slicePieces))])
	}
	return b.String()
}

func TestSliceWithWidthFastPathEqualsTheGraphemeIterator(t *testing.T) {
	r := rand.New(rand.NewPCG(20261116, 11968))
	const cases = 60000
	for n := range cases {
		line := randomSliceLine(r)
		width := VisibleWidth(line)
		start := r.IntN(width+6) - 3
		length := r.IntN(width+6) - 1
		strict := r.IntN(2) == 0
		got := SliceWithWidth(line, start, length, strict)
		want := sliceWithWidthReference(line, start, length, strict)
		if got != want {
			t.Fatalf("case %d: SliceWithWidth(%q, %d, %d, %v) = %q/%d, reference %q/%d", n, line, start, length, strict, got.Text, got.Width, want.Text, want.Width)
		}
	}
}

func TestSliceWithWidthFastPathEdges(t *testing.T) {
	for _, line := range []string{"", "plain ascii", "a\u0301bc", "ab\u0301", "ab\u200dc", "ab\ufe0f", "abc\x1b[31mdef\x1b[0mghi", "x\x1b", "\x1b[31m", "中ab", "ab中"} {
		for start := -2; start <= VisibleWidth(line)+2; start++ {
			for length := -1; length <= VisibleWidth(line)+3; length++ {
				for _, strict := range []bool{false, true} {
					if got, want := SliceWithWidth(line, start, length, strict), sliceWithWidthReference(line, start, length, strict); got != want {
						t.Fatalf("SliceWithWidth(%q, %d, %d, %v) = %q/%d, reference %q/%d", line, start, length, strict, got.Text, got.Width, want.Text, want.Width)
					}
				}
			}
		}
	}
}
