package jsstring

import (
	"strings"
	"unicode/utf8"
)

// FromLatin1 is the string Node's fetch (undici) returns from Headers.get for the raw header bytes s: each byte is one code point (Latin-1), so the
// UTF-8 bytes of U+FEFF read as "ï»¿" and a lone 0xA0 byte reads as U+00A0.
func FromLatin1(s string) string {
	ascii := true
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	b.Grow(2 * len(s))
	for i := range len(s) {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}
