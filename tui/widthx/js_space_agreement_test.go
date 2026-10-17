package widthx

import (
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// widthx keeps its own table-driven IsJSSpace for the render path. It must agree with the shared JavaScript whitespace predicate on every code point.
func TestIsJSSpaceAgreesWithJsstringIsSpace(t *testing.T) {
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if IsJSSpace(r) != jsstring.IsSpace(r) {
			t.Fatalf("IsJSSpace(%U) = %v, jsstring.IsSpace = %v", r, IsJSSpace(r), jsstring.IsSpace(r))
		}
	}
}
