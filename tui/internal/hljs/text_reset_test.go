package hljs

import (
	"slices"
	"testing"
)

// TestTextResetIsTheTextOfTheCode: a Text that held another code, longer or shorter, is after Reset the Text a new conversion of the code gives (its units and the part a longer text cannot change), in the array it had when that is large enough.
func TestTextResetIsTheTextOfTheCode(t *testing.T) {
	codes := []string{"", "a", "x = 1\n", "naïve café 日本語 😀", "tail \xf0\x9f\x98", "tail \xe6\x97", "\xed\xa0\xbd", "line\r\nline\n" + string(make([]byte, 300))}
	for _, before := range codes {
		for _, code := range codes {
			used := NewText(before + before)
			room := used.Cap()
			got, want := used.Reset(code), NewText(code)
			if got.code != want.code || !slices.Equal(got.units, want.units) || got.settled != want.settled || got.settledUnits != want.settledUnits {
				t.Errorf("Reset(%q) after %q: units %v settled %d/%d, a new Text has %v settled %d/%d", code, before+before, got.units, got.settled, got.settledUnits, want.units, want.settled, want.settledUnits)
			}
			// Grow makes room for a unit per byte of the code.
			if len(code) <= room && got.Cap() != room {
				t.Errorf("Reset(%q) after %q: room for %d units, had %d: the array was not used again", code, before+before, got.Cap(), room)
			}
		}
	}
}
