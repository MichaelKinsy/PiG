package jsstring

import (
	"regexp"
	"testing"
)

// Node 24: a response header sent as raw bytes reads back from fetch's Headers.get one code point per byte.
func TestFromLatin1DecodesEachByteAsOneCodePoint(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"Bearer x", "Bearer x"},
		{"a\xef\xbb\xbfb", "a\u00ef\u00bb\u00bfb"},
		{"a\xc2\xa0b", "a\u00c2\u00a0b"},
		{"a\xa0b", "a\u00a0b"},
		{"\xff", "\u00ff"},
	} {
		if got := FromLatin1(tc.raw); got != tc.want {
			t.Errorf("FromLatin1(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// SpaceClass and IsSpace agree on every code point of the Basic Multilingual Plane (JavaScript's \s has no characters above it).
func TestSpaceClassMatchesExactlyIsSpace(t *testing.T) {
	class := regexp.MustCompile(`^[` + SpaceClass + `]$`)
	for r := rune(0); r <= 0xFFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if got := class.MatchString(string(r)); got != IsSpace(r) {
			t.Errorf("U+%04X: SpaceClass matches %v, IsSpace %v", r, got, IsSpace(r))
		}
	}
}
