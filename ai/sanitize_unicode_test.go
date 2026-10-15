package ai

import (
	"strings"
	"testing"
)

// Pi utils/sanitize-unicode.ts:21-25 removes only unpaired UTF-16 code units. WTF-8 preserves those code units at the Go string boundary; valid UTF-8 and literal replacement characters stay unchanged.
func TestSanitizeSurrogatesCodeUnits(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "", ""},
		{"ordinary", "hello", "hello"},
		{"emoji", "Hello 🙈 World", "Hello 🙈 World"},
		{"literal replacement", "a\ufffdb", "a\ufffdb"},
		{"unpaired high", "a\xed\xa0\xbdb", "ab"},
		{"unpaired low", "a\xed\xb9\x88b", "ab"},
		{"paired code units", "a\xed\xa0\xbd\xed\xb9\x88b", "a🙈b"},
		{"high high low", "\xed\xa0\xbd\xed\xa0\xbd\xed\xb9\x88", "🙈"},
		{"BMP boundaries", "\ud7ff\ue000", "\ud7ff\ue000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeSurrogates(tc.in); got != tc.want {
				t.Fatalf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func BenchmarkSanitizeSurrogates(b *testing.B) {
	for _, tc := range []struct{ name, text string }{
		{"empty", ""},
		{"ordinary", strings.Repeat("tool text 🙈 こんにちは\n", 256)},
		{"unpaired", strings.Repeat("tool text \xed\xa0\xbd\n", 256)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.text)))
			b.ReportAllocs()
			for b.Loop() {
				_ = sanitizeSurrogates(tc.text)
			}
		})
	}
}

// utils/sanitize-unicode.ts: a high surrogate at the top of its range (U+DBFF) followed by a low one is a valid pair and survives as the astral scalar; an unpaired one is dropped.
func TestSanitizeSurrogatesPairsTheWholeHighSurrogateRange(t *testing.T) {
	const highMax, lowMin = "\xed\xaf\xbf", "\xed\xb0\x80" // WTF-8 of U+DBFF and U+DC00
	if got := sanitizeSurrogates("a" + highMax + lowMin + "b"); got != "a\U0010FC00b" {
		t.Errorf("paired U+DBFF U+DC00 = %q, want a\\U0010FC00b", got)
	}
	if got := sanitizeSurrogates("a" + highMax + "b"); got != "ab" {
		t.Errorf("unpaired U+DBFF = %q, want ab", got)
	}
}
