package compaction

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// jsLengthByRune is jsLength before its ASCII fast path: the reference it must equal, invalid bytes included.
func jsLengthByRune(s string) int {
	units := 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}

var jsLengthCases = []string{
	"", "a", "abcdefgh", "abcdefghi", "é", "abcdefgé", "abcdefghé", "😀", "abcdefgh😀abcdefgh",
	"\xed\xa0\x80", "abcdefgh\xed\xa0\x80xyz", "\xff", "abcdefgh\xffabcdefgh", "abc\xe2\x82abcdefghijk",
	strings.Repeat("x", 1000) + "€" + strings.Repeat("y", 1000),
}

func TestJSLengthMatchesTheRuneLoop(t *testing.T) {
	for _, text := range jsLengthCases {
		if got, want := jsLength(text), jsLengthByRune(text); got != want {
			t.Errorf("jsLength(%q) = %d, want %d", text, got, want)
		}
	}
}

func FuzzJSLengthMatchesTheRuneLoop(f *testing.F) {
	for _, text := range jsLengthCases {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, want := jsLength(text), jsLengthByRune(text); got != want {
			t.Fatalf("jsLength(%q) = %d, want %d", text, got, want)
		}
	})
}
