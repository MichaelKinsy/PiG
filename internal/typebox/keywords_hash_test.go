package typebox

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// hashNames are object member names whose UTF-16 order differs from their code point order, with lone surrogates (WTF-8) among them.
var hashNames = []string{
	"", "a", "ab", "b", "é", "e\u0301", "\U0001d49c", "\uff5e", "\ue000", "\uffff", "ab\uffff", "ab\U00010000", "\U0001F600", "\U0001F601", "x\U0001F600y", "x\uffffy",
	jsstring.FromUTF16([]uint16{0xD800}), jsstring.FromUTF16([]uint16{0xDC00}), jsstring.FromUTF16([]uint16{0x61, 0xD83D}), jsstring.FromUTF16([]uint16{0xD83D, 0x61}), jsstring.FromUTF16([]uint16{0xDE00, 0xD83D}),
	"日本語", "日本", "日本語テキスト",
}

// compareUTF16 is slices.Compare of the UTF-16 units, the order of Array.prototype.sort.
func TestCompareUTF16MatchesTheUnitOrder(t *testing.T) {
	for _, a := range hashNames {
		for _, b := range hashNames {
			if got, want := compareUTF16(a, b), slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b)); got != want {
				t.Errorf("compareUTF16(%q, %q) = %d, units compare %d", a, b, got, want)
			}
		}
	}
}
