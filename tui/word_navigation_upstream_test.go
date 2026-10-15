//go:build !pig_strip_word_dictionaries

package tui

import (
	"iter"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestUpstreamWordNavigation(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		forward    bool
		pairs      [][2]int
	}{
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:6.
		{"backward/basic words: hello world", "hello world", false, [][2]int{{11, 6}, {6, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:12.
		{"backward/dotted: foo.bar", "foo.bar", false, [][2]int{{7, 4}, {4, 3}, {3, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:19.
		{"backward/colon: foo:bar", "foo:bar", false, [][2]int{{7, 4}, {4, 3}, {3, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:26.
		{"backward/path: path/to/file", "path/to/file", false, [][2]int{{12, 8}, {8, 7}, {7, 5}, {5, 4}, {4, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:36.
		{"backward/CJK mixed", "你好世界 test", false, [][2]int{{9, 5}, {5, 2}, {2, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:44.
		{"backward/whitespace at boundaries", "  hello  ", false, [][2]int{{9, 2}, {2, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:50.
		{"backward/punctuation run: foo...bar", "foo...bar", false, [][2]int{{9, 6}, {6, 3}, {3, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:57.
		{"backward/cursor at 0 returns 0", "hello", false, [][2]int{{0, 0}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:63.
		{"forward/basic words: hello world", "hello world", true, [][2]int{{0, 5}, {5, 11}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:69.
		{"forward/dotted: foo.bar", "foo.bar", true, [][2]int{{0, 3}, {3, 4}, {4, 7}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:76.
		{"forward/colon: foo:bar", "foo:bar", true, [][2]int{{0, 3}, {3, 4}, {4, 7}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:83.
		{"forward/path: path/to/file", "path/to/file", true, [][2]int{{0, 4}, {4, 5}, {5, 7}, {7, 8}, {8, 12}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:107.
		{"forward/whitespace at boundaries", "  hello  ", true, [][2]int{{0, 7}, {7, 9}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:113.
		{"forward/punctuation run: foo...bar", "foo...bar", true, [][2]int{{0, 3}, {3, 6}, {6, 9}}},
		// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:120.
		{"forward/cursor at end returns end", "hello", true, [][2]int{{5, 5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, pair := range tc.pairs {
				fn := FindWordBackward
				if tc.forward {
					fn = FindWordForward
				}
				if got := fn(tc.text, pair[0]); got != pair[1] {
					t.Fatalf("%q from %d = %d, want %d", tc.text, pair[0], got, pair[1])
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:92.
	t.Run("forward/CJK mixed", func(t *testing.T) {
		text := "你好世界 test"
		end := FindWordForward(text, 0)
		if end <= 0 || end > 4 {
			t.Fatal(end)
		}
		pos := 0
		for pos < jsstring.Length(text) {
			next := FindWordForward(text, pos)
			if next == pos {
				break
			}
			pos = next
		}
		if pos != jsstring.Length(text) {
			t.Fatalf("walk stopped at %d", pos)
		}
	})
}

func TestUpstreamWordNavigationAtomicSegments(t *testing.T) {
	marker := "[paste #1 +5 lines]"
	text := "hello " + marker + " world"
	full := []SegmentData{{Segment: "hello", Index: 0, Input: text, IsWordLike: true}, {Segment: " ", Index: 5, Input: text}, {Segment: marker, Index: 6, Input: text, IsWordLike: true}, {Segment: " ", Index: 25, Input: text}, {Segment: "world", Index: 26, Input: text, IsWordLike: true}}
	segmentMap := map[string][]SegmentData{
		text:      full,
		text[:26]: {{Segment: "hello", Index: 0, Input: text, IsWordLike: true}, {Segment: " ", Index: 5, Input: text}, {Segment: marker, Index: 6, Input: text, IsWordLike: true}, {Segment: " ", Index: 25, Input: text}},
		text[6:]:  {{Segment: marker, Index: 0, Input: text, IsWordLike: true}, {Segment: " ", Index: 19, Input: text}, {Segment: "world", Index: 20, Input: text, IsWordLike: true}},
	}
	// The original Map repeats the full-text key through text.slice(0,text.length); retain its replacement semantics.
	segmentMap[text[:len(text)]] = full
	opts := WordNavigationOptions{Segment: func(input string) iter.Seq[SegmentData] { return slices.Values(segmentMap[input]) }, IsAtomicSegment: func(s string) bool { return s == marker }}
	// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:180.
	t.Run("backward skips word then stops before atomic marker", func(t *testing.T) {
		if got := FindWordBackward(text, len(text), opts); got != 26 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:184.
	t.Run("backward skips whitespace then atomic marker as one unit", func(t *testing.T) {
		if got := FindWordBackward(text, 26, opts); got != 6 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/word-navigation.test.ts:188.
	t.Run("forward skips atomic marker as one unit", func(t *testing.T) {
		if got := FindWordForward(text, 6, opts); got != 6+len(marker) {
			t.Fatal(got)
		}
	})
}
