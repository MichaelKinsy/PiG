package hljs

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// randomWTF8 is a text of ASCII, two-, three- and four-byte characters, lone surrogates in WTF-8, the halves of a surrogate pair
// as two WTF-8 units, and stray bytes: every kind of sequence a cut can fall inside.
func randomWTF8(rng *rand.Rand, n int) string {
	pieces := []string{"a", "b", "\n", " ", "é", "ß", "€", "日本", "😀", "\xed\xa0\x80", "\xed\xbf\xbf", "\xed\xa0\xbd\xed\xb8\x80", "\x80", "\xe2\x82", "\xf0\x9f", "\xc3", "\xff", "\x1b[31m", "\\u00e9"}
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(pieces[rng.Intn(len(pieces))])
	}
	return b.String()
}

// A text grown through any chain of cuts, inside a character or not, holds the units of a conversion of its whole code.
func TestTextGrownThroughCutsHasTheUnitsOfAFreshConversion(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := range 300 {
		code := randomWTF8(rng, 1+rng.Intn(400))
		text := &Text{}
		for end := 0; end < len(code); {
			end += 1 + rng.Intn(9)
			end = min(end, len(code))
			text.Grow(code[:end])
			if text.Code() != code[:end] {
				t.Fatalf("round %d: the text holds %q, want %q", round, text.Code(), code[:end])
			}
			if want := stringToUnits(code[:end]); !slices.Equal(text.units, want) {
				t.Fatalf("round %d: units of %q after a cut at %d:\n got %v\nwant %v", round, code[:end], end, text.units, want)
			}
		}
	}
}

// Code that does not start with the text's code is converted whole.
func TestTextGrowToUnrelatedCodeConvertsItWhole(t *testing.T) {
	text := NewText("héllo wörld 😀")
	text.Grow("日本語 and more")
	if want := stringToUnits("日本語 and more"); !slices.Equal(text.units, want) {
		t.Fatalf("units %v, want %v", text.units, want)
	}
}

// A cut inside a sequence leaves it out of the settled part, and the next growth converts it again as the character it became.
func TestTextSettledPartStopsBeforeAnIncompleteSequence(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"ab", 2}, {"ab\xe2", 2}, {"ab\xe2\x82", 2}, {"ab\xe2\x82\xac", 5}, {"ab\xf0\x9f\x98", 2}, {"ab\xed\xa0", 2}, {"ab\xed\xa0\x80", 5}, {"ab\x80", 3}, {"ab\xc3", 2}, {"ab\xc3\xa9", 4}, {"", 0},
	}
	for _, c := range cases {
		if got := settledLen(c.code); got != c.want {
			t.Errorf("settledLen(%q) = %d, want %d", c.code, got, c.want)
		}
	}
}
