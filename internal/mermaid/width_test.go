package mermaid

import "testing"

// TestStringWidthMatchesUpstream pins pig's width measurement to grok-mermaid's
// own stringWidth (src/width.ts): the values pi uses to size boxes. Covers the
// generated WIDTHS table, grapheme clustering, and the VS16 / regional-indicator
// / combining-mark rules of clusterWidth.
func TestStringWidthMatchesUpstream(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"abc", 3},
		{"日本語", 6},          // CJK wide: 3 clusters * 2
		{"café", 4},         // precomposed é
		{"á", 1},            // a + combining U+0301: one cluster, base width
		{"👨‍👩‍👧", 2},        // ZWJ family: one cluster
		{"🇯🇵", 2},           // regional-indicator pair (flag)
		{"x\u200bz", 2},     // zero-width space contributes 0
		{"⚡", 2},            // wide symbol
		{"①", 1},            // circled digit: narrow per unicode-width
		{"ﬀ", 1},            // ligature
		{"\u2764\ufe0f", 2}, // heart + VS16 emoji presentation forces 2
		{"\u2764", 1},       // heart without VS16 stays 1
		{"", 0},
	}
	for _, c := range cases {
		if got := stringWidth(c.s); got != c.want {
			t.Errorf("stringWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// TestCodePointWidthTableBounds spot-checks the generated table at run edges.
func TestCodePointWidthTableBounds(t *testing.T) {
	cases := []struct {
		cp   rune
		want int
	}{
		{'A', 1},
		{0x00ad, 0},  // soft hyphen
		{0x0301, 0},  // combining acute
		{0x1100, 2},  // Hangul choseong (wide)
		{0x30000, 2}, // CJK Ext G plane (wide)
	}
	for _, c := range cases {
		if got := codePointWidth(c.cp); got != c.want {
			t.Errorf("codePointWidth(%#x) = %d, want %d", c.cp, got, c.want)
		}
	}
}
