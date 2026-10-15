package tui

import (
	"iter"
	"testing"
	"unicode/utf16"
)

// pi: packages/tui/src/word-navigation.ts

// Ports packages/tui/test/word-navigation.test.ts. Cursor positions are UTF-16 code units, as in Pi.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func TestFindWordBackwardMatchesPi(t *testing.T) {
	cases := []struct {
		text string
		from []int // each entry is a cursor; the next entry is the expected result
	}{
		{"hello world", []int{11, 6, 0}},
		{"foo.bar", []int{7, 4, 3, 0}},
		{"foo:bar", []int{7, 4, 3, 0}},
		{"path/to/file", []int{12, 8, 7, 5, 4, 0}},
		{"你好世界 test", []int{9, 5, 2, 0}},
		{"  hello  ", []int{9, 2, 0}},
		{"foo...bar", []int{9, 6, 3, 0}},
	}
	for _, c := range cases {
		for i := 0; i+1 < len(c.from); i++ {
			if got := FindWordBackward(c.text, c.from[i]); got != c.from[i+1] {
				t.Errorf("FindWordBackward(%q, %d) = %d, want %d", c.text, c.from[i], got, c.from[i+1])
			}
		}
	}
	if got := FindWordBackward("hello", 0); got != 0 {
		t.Errorf("cursor at 0 returned %d", got)
	}
}

func TestFindWordForwardMatchesPi(t *testing.T) {
	cases := []struct {
		text string
		pos  []int
	}{
		{"hello world", []int{0, 5, 11}},
		{"foo.bar", []int{0, 3, 4, 7}},
		{"foo:bar", []int{0, 3, 4, 7}},
		{"path/to/file", []int{0, 4, 5, 7, 8, 12}},
		{"  hello  ", []int{0, 7, 9}},
		{"foo...bar", []int{0, 3, 6, 9}},
	}
	for _, c := range cases {
		for i := 0; i+1 < len(c.pos); i++ {
			if got := FindWordForward(c.text, c.pos[i]); got != c.pos[i+1] {
				t.Errorf("FindWordForward(%q, %d) = %d, want %d", c.text, c.pos[i], got, c.pos[i+1])
			}
		}
	}
	if got := FindWordForward("hello", 5); got != 5 {
		t.Errorf("cursor at end returned %d", got)
	}
	// CJK mixed: the first step lands inside the CJK run and walking forward reaches the end.
	text := "你好世界 test"
	if first := FindWordForward(text, 0); first <= 0 || first > 4 {
		t.Errorf("first CJK step = %d, want in (0, 4]", first)
	}
	pos, end := 0, utf16Len(text)
	for pos < end {
		next := FindWordForward(text, pos)
		if next == pos {
			break
		}
		pos = next
	}
	if pos != end {
		t.Errorf("walk ended at %d, want %d", pos, end)
	}
}

// The atomic-segment cases: the supplied segmenter returns the pre-split segments of exactly the substrings Pi's map names.
func TestFindWordNavigationAtomicSegmentsMatchPi(t *testing.T) {
	marker := "[paste #1 +5 lines]"
	text := "hello " + marker + " world"
	seg := func(s string, index int, word bool) SegmentData {
		return SegmentData{Segment: s, Index: index, IsWordLike: word}
	}
	full := []SegmentData{seg("hello", 0, true), seg(" ", 5, false), seg(marker, 6, true), seg(" ", 25, false), seg("world", 26, true)}
	utf16Prefix := func(s string, n int) string { return string(utf16.Decode(utf16.Encode([]rune(s))[:n])) }
	segMap := map[string][]SegmentData{
		text:                  full,
		utf16Prefix(text, 26): full[:4],
		string(utf16.Decode(utf16.Encode([]rune(text))[6:])): {seg(marker, 0, true), seg(" ", 19, false), seg("world", 20, true)},
	}
	opts := WordNavigationOptions{
		Segment: func(input string) iter.Seq[SegmentData] {
			return func(yield func(SegmentData) bool) {
				for _, s := range segMap[input] {
					if !yield(s) {
						return
					}
				}
			}
		},
		IsAtomicSegment: func(s string) bool { return s == marker },
	}
	if got := FindWordBackward(text, utf16Len(text), opts); got != 26 {
		t.Errorf("backward from end = %d, want 26 (word skipped, stops before the marker's trailing space)", got)
	}
	if got := FindWordBackward(text, 26, opts); got != 6 {
		t.Errorf("backward from 26 = %d, want 6 (marker skipped as one unit)", got)
	}
	if got := FindWordForward(text, 6, opts); got != 6+utf16Len(marker) {
		t.Errorf("forward from 6 = %d, want %d", got, 6+utf16Len(marker))
	}
}
