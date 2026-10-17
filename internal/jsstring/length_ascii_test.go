package jsstring

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// lengthByRune is Length before its ASCII fast path: the reference it must equal.
func lengthByRune(text string) int {
	count := 0
	for len(text) > 0 {
		if len(text) >= 3 && text[0] == 0xed && text[1] >= 0xa0 && text[1] <= 0xbf && text[2] >= 0x80 && text[2] <= 0xbf {
			text = text[3:]
			count++
			continue
		}
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]
		count++
		if r > 0xffff {
			count++
		}
	}
	return count
}

var lengthCases = []string{
	"", "a", "abcdefg", "abcdefgh", "abcdefghi", "abcdefghijklmnopq",
	"é", "abcdefgé", "abcdefghé", "éabcdefghijklmnop", "abcdefgh\u00e9ijklmnopqrstuvwx",
	"😀", "abcdefgh😀abcdefgh", "\xed\xa0\x80", "abcdefgh\xed\xa0\x80xyz", "\xed\xb0\x80\xed\xa0\x80",
	"\xff", "abcdefgh\xffabcdefgh", "\xe2\x82", "abc\xe2\x82abcdefghijk", "\x7f\x80\x7f",
	strings.Repeat("x", 1000) + "€" + strings.Repeat("y", 1000),
}

func TestLengthMatchesTheRuneLoop(t *testing.T) {
	for _, text := range lengthCases {
		if got, want := Length(text), lengthByRune(text); got != want {
			t.Errorf("Length(%q) = %d, want %d", text, got, want)
		}
	}
}

func FuzzLengthMatchesTheRuneLoop(f *testing.F) {
	for _, text := range lengthCases {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, want := Length(text), lengthByRune(text); got != want {
			t.Fatalf("Length(%q) = %d, want %d", text, got, want)
		}
	})
}

var lengthSink int

func BenchmarkJSLength50KBASCII(b *testing.B) {
	text := strings.Repeat("export const value = items.map((item) => item.id);\n", 1000)
	b.SetBytes(int64(len(text)))
	for range b.N {
		lengthSink += Length(text)
	}
}

func BenchmarkJSLength50KBMixed(b *testing.B) {
	text := strings.Repeat("const café = \"naïve\"; // 😀 emoji and ascii\n", 1100)
	b.SetBytes(int64(len(text)))
	for range b.N {
		lengthSink += Length(text)
	}
}
