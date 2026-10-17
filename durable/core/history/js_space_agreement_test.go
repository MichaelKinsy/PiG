package history

import (
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The WTF-8 trim walker is kept apart from jsstring for the byte-level hot path. It must agree with the shared JavaScript whitespace predicate on every code point.
func TestIsJSSpaceAgreesWithJsstringIsSpace(t *testing.T) {
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		w := utf8.AppendRune(nil, r)
		got, size := isJSSpace(w, 0)
		if got != jsstring.IsSpace(r) || (got && size != len(w)) {
			t.Fatalf("isJSSpace(%U) = %v, %d; jsstring.IsSpace = %v", r, got, size, jsstring.IsSpace(r))
		}
	}
}
