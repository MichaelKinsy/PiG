package ai

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// utf16LengthByRune is utf16Length before it became jsstring.Length (PF1 N6): a rune loop over valid UTF-8, jsstring.Length otherwise.
func utf16LengthByRune(value string) int {
	if !utf8.ValidString(value) {
		return jsstring.Length(value)
	}
	length := 0
	for _, r := range value {
		length += utf16Units(r)
	}
	return length
}

func FuzzUTF16LengthMatchesTheRuneLoop(f *testing.F) {
	for _, text := range []string{"", "abcdefgh", "abcdefghé", "😀x", "abcdefgh\xed\xa0\x80", "\xff\xfe", strings.Repeat("ab", 40) + "€"} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, want := utf16Length(text), utf16LengthByRune(text); got != want {
			t.Fatalf("utf16Length(%q) = %d, want %d", text, got, want)
		}
	})
}
