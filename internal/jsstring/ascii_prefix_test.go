package jsstring

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzASCIIPrefix(f *testing.F) {
	for _, text := range lengthCases {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		n := ASCIIPrefix(text)
		if n > len(text) || strings.IndexFunc(text[:n], func(r rune) bool { return r >= utf8.RuneSelf }) >= 0 || (n < len(text) && text[n] < utf8.RuneSelf) {
			t.Fatalf("ASCIIPrefix(%q) = %d", text, n)
		}
	})
}
