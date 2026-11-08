package widthx

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// referenceApplyLineReset is ApplyLineReset as ported from Pi main 25490ea49 (fb4d32eb1c): the oracle for the single-pass implementation. It
// strips the markers with strings.ReplaceAll, normalizes the result and appends the reset, each on its own string.
func referenceApplyLineReset(line string) string {
	if IsImageLine(line) {
		return line
	}
	return NormalizeTerminalOutput(strings.ReplaceAll(line, CursorMarker, "")) + SegmentReset
}

// lineResetPieces are the fragments the random and fuzz lines are made of: whole and broken cursor markers, image prefixes, SGR, the characters
// NormalizeTerminalOutput rewrites, and the halves of a UTF-8 sequence that removing a marker can join.
var lineResetPieces = []string{
	CursorMarker, CursorMarker, "\x1b_pi:", "\x1b_pi:c", "c\x07", "\x07", "\x1b", "\x1b_", "\x1b]", "\x1b_G", "\x1b_Gimage;payload\x1b\\",
	"\x1b]1337;File=inline=1:AAAA\x07", "\x1b[31m", "\x1b[0m", "\x1b]8;;https://example.com\x07", FakeCursorStart, FakeCursorEnd,
	"\t", "a", "hello world ", " ", "日本語", "🙂", "é", "ำ", "ຳ", "\xe0\xb8", "\xe0\xba", "\xb3", "\xb8\xb3", "\xe0", "\xff",
	"ำ" + CursorMarker, "\xe0\xb8" + CursorMarker + "\xb3", "\xe0\xba" + CursorMarker + "\xb3",
}

func assertApplyLineResetMatchesReference(t *testing.T, line string) {
	t.Helper()
	if got, want := ApplyLineReset(line), referenceApplyLineReset(line); got != want {
		t.Fatalf("ApplyLineReset(%q)\n got  %q\n want %q", line, got, want)
	}
}

func TestApplyLineResetMatchesReference(t *testing.T) {
	overlapping := "\x1b_pi:" + CursorMarker + "c\x07" // stripping the inner marker leaves a new one, which a single pass keeps
	cases := []string{
		"", "plain", "\x1b[31mred\x1b[0m", CursorMarker, CursorMarker + CursorMarker, "a" + CursorMarker, CursorMarker + "a",
		"a" + CursorMarker + "b" + CursorMarker + "c", "\x1b[7m" + CursorMarker + " \x1b[27m" + CursorMarker, overlapping, overlapping + overlapping,
		"\x1b_pi:c", "\x1b_pi:c\x1b_pi:c\x07", "\x1b", "x\x1b", "\x1b_", "\x1b_p", "\x1b_pi:cX",
		"tab\there" + CursorMarker, CursorMarker + "\tx\x1b]8;;a\tb\x07", "\x1b[31m\t\x1b[0m",
		"นำ", "ນຳ", "นำ" + CursorMarker, CursorMarker + "ນຳ", "\xe0\xb8" + CursorMarker + "\xb3", "\xe0\xba" + CursorMarker + "\xb3\t",
		"\xe0" + CursorMarker + "\xb8" + CursorMarker + "\xb3", "\xff" + CursorMarker + "\xfe",
		"\x1b_Gimage;payload\x1b\\", "\x1b_Gimage;payload\x1b\\" + CursorMarker, CursorMarker + "\x1b_Gx", "up\x1b[1A\x1b_Ga",
		"\x1b]1337;File=inline=1:AAAA\x07", CursorMarker + "\x1b]1337;File=inline=1:AAAA\x07", "\x1b]1337;File",
		"\x1b_pi:" + CursorMarker + "_G", // not an image line as given; stripped it holds "\x1b_pi:_G", still not one
		"\x1b" + CursorMarker + "_G",     // the strip would make an image prefix: the line as given is not an image line
		strings.Repeat("\x1b[32mhello wor\x1b[0mld ", 90) + CursorMarker, strings.Repeat("日本語のテキスト", 40) + CursorMarker + "ำ",
	}
	for _, c := range cases {
		assertApplyLineResetMatchesReference(t, c)
	}
	rng := rand.New(rand.NewPCG(25490, 0xea49))
	for range 200000 {
		var b strings.Builder
		for range 1 + rng.IntN(12) {
			b.WriteString(lineResetPieces[rng.IntN(len(lineResetPieces))])
		}
		assertApplyLineResetMatchesReference(t, b.String())
	}
}

func FuzzApplyLineResetMatchesReference(f *testing.F) {
	for _, p := range lineResetPieces {
		f.Add(p)
		f.Add("\x1b[31mab" + p + "cd\x1b[0m")
		f.Add(CursorMarker + p + CursorMarker)
	}
	f.Add("\x1b_pi:" + CursorMarker + "c\x07")
	f.Add("\xe0\xb8" + CursorMarker + "\xb3\t")
	f.Fuzz(assertApplyLineResetMatchesReference)
}

// TestApplyLineResetAllocations pins the cost the single pass won back: one allocation for the line and its reset, with or without a marker.
func TestApplyLineResetAllocations(t *testing.T) {
	styled := "\x1b[31m" + strings.Repeat("hello world ", 6) + "\x1b[0m tail"
	wide := "\x1b[1m" + strings.Repeat("日本語のテキスト", 4) + "\x1b[0m🙂"
	for name, line := range map[string]string{
		"plain": "hello", "styled": styled, "marker": styled + CursorMarker, "markers2": CursorMarker + styled + CursorMarker,
		"wide": wide, "wide marker": CursorMarker + wide, "image": "\x1b_Gx\x1b\\" + CursorMarker,
	} {
		want := 1.0
		if name == "image" {
			want = 0
		}
		if got := testing.AllocsPerRun(100, func() { _ = ApplyLineReset(line) }); got > want {
			t.Errorf("%s: ApplyLineReset allocates %v times, want at most %v", name, got, want)
		}
	}
}

// TestIndexESCNearMatchesIndexByte puts ESC bytes (and bytes that differ from it by one bit, which a careless zero-byte test confuses with it) at every
// offset of strings of every length around the eight-byte block size of the scan.
func TestIndexESCNearMatchesIndexByte(t *testing.T) {
	for n := 0; n <= 40; n++ {
		for _, fill := range []string{"a", "\x1a", "\x1c", "\x9b", "\x00", "\xff"} {
			base := strings.Repeat(fill, n)
			if got, want := indexESCNear(base), strings.IndexByte(base, 0x1B); got != want {
				t.Fatalf("indexESCNear(%q) = %d, want %d", base, got, want)
			}
			for at := 0; at < n; at++ {
				for _, second := range []int{-1, 0, 7, 8, 9, n - 1} {
					b := []byte(base)
					b[at] = 0x1B
					if second >= 0 && second < n {
						b[second] = 0x1B
					}
					if got, want := indexESCNear(string(b)), strings.IndexByte(string(b), 0x1B); got != want {
						t.Fatalf("indexESCNear(%q) = %d, want %d", b, got, want)
					}
				}
			}
		}
	}
}
