package tui

import "sort"

// markedCJKRanges is the character class marked-cjk-friendly 0.1.2 calls CJK (src/index.ts), sorted: the code points its
// emphasis rules treat as punctuation next to a delimiter run.
var markedCJKRanges = [...][2]rune{
	{0x1100, 0x11ff}, {0x20a9, 0x20a9}, {0x2329, 0x232a}, {0x2630, 0x2637}, {0x268a, 0x268f}, {0x2e80, 0x2e99}, {0x2e9b, 0x2ef3},
	{0x2f00, 0x2fd5}, {0x2ff0, 0x303e}, {0x3041, 0x3096}, {0x3099, 0x30ff}, {0x3105, 0x312f}, {0x3131, 0x318e}, {0x3190, 0x31e5},
	{0x31ef, 0x321e}, {0x3220, 0x3247}, {0x3250, 0xa48c}, {0xa490, 0xa4c6}, {0xa960, 0xa97c}, {0xac00, 0xd7a3}, {0xd7b0, 0xd7c6},
	{0xd7cb, 0xd7fb}, {0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe52}, {0xfe54, 0xfe66}, {0xfe68, 0xfe6b}, {0xff01, 0xffbe},
	{0xffc2, 0xffc7}, {0xffca, 0xffcf}, {0xffd2, 0xffd7}, {0xffda, 0xffdc}, {0xffe0, 0xffe6}, {0xffe8, 0xffee},
	{0x16fe0, 0x16fe4}, {0x16ff0, 0x16ff6}, {0x17000, 0x18cda}, {0x18cff, 0x18d20}, {0x18d80, 0x18df2}, {0x18e00, 0x19191},
	{0x191a0, 0x191d2}, {0x1aff0, 0x1aff3}, {0x1aff5, 0x1affb}, {0x1affd, 0x1affe}, {0x1b000, 0x1b128}, {0x1b132, 0x1b132},
	{0x1b150, 0x1b152}, {0x1b155, 0x1b155}, {0x1b164, 0x1b168}, {0x1b170, 0x1b2fb}, {0x1d300, 0x1d356}, {0x1d360, 0x1d376},
	{0x1f1ae, 0x1f1ae}, {0x1f200, 0x1f200}, {0x1f202, 0x1f202}, {0x1f210, 0x1f219}, {0x1f21b, 0x1f22e}, {0x1f230, 0x1f231},
	{0x1f237, 0x1f237}, {0x1f23b, 0x1f23b}, {0x1f240, 0x1f248}, {0x1f260, 0x1f265}, {0x1f7da, 0x1f7da}, {0x20000, 0x3fffd},
}

// markedCJK is marked-cjk-friendly's cjkTest for one code point.
func markedCJK(r rune) bool {
	if r < markedCJKRanges[0][0] {
		return false
	}
	// The first range whose end is at or after r holds r when it starts at or before r.
	i := sort.Search(len(markedCJKRanges), func(i int) bool { return markedCJKRanges[i][1] >= r })
	return i < len(markedCJKRanges) && markedCJKRanges[i][0] <= r
}

// markedPrevCharCJK is marked-cjk-friendly's isPrevCharCjk: whether the character before the delimiter run is CJK.
// prevChar is JavaScript's raw.slice(-1), a lone low surrogate after an astral character, and a variation selector may follow
// the base character, so the character is recovered from the masked source, whose tail of srcLen characters is the source
// being tokenized.
func markedPrevCharCJK(prevChar rune, masked []rune, srcLen int) bool {
	if markedCJK(prevChar) {
		return true
	}
	at := len(masked) - srcLen - 1
	if at < 0 {
		return false
	}
	before := masked[at]
	// A variation selector (U+FE00 to U+FE0E) belongs to the character before it.
	if before >= 0xfe00 && before <= 0xfe0e && at >= 1 {
		before = masked[at-1]
	}
	if before > 0xffff && before >= 0xe0100 && before <= 0xe01ef {
		// An ideographic variation selector only follows an ideograph.
		return true
	}
	return markedCJK(before)
}
