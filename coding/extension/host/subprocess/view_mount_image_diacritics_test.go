package subprocess

import "testing"

// pig additive (D614): the shape of the generated table of placeholder diacritics: the 297 code points the protocol
// numbers rows and columns with, in rising order from U+0305 to U+1D244. That it is what the generator writes from
// Unicode 6.0.0 is the generator's own test (automation/ci/kittydiacritics).
func TestKittyDiacriticsTableShape(t *testing.T) {
	if len(kittyDiacritics) != 297 || kittyDiacritics[0] != 0x0305 || kittyDiacritics[len(kittyDiacritics)-1] != 0x1D244 {
		t.Fatalf("the table holds %d code points from %U to %U, want 297 from U+0305 to U+1D244", len(kittyDiacritics), kittyDiacritics[0], kittyDiacritics[len(kittyDiacritics)-1])
	}
	for i := 1; i < len(kittyDiacritics); i++ {
		if kittyDiacritics[i] <= kittyDiacritics[i-1] {
			t.Fatalf("entry %d, %U, does not follow %U", i, kittyDiacritics[i], kittyDiacritics[i-1])
		}
	}
}
