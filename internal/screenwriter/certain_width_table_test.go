// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"testing"
	"unicode"

	"golang.org/x/text/width"
)

// TestCertainWidthAgreesWithXTextWidth is the table behind certainWidth's use of go-runewidth: for every letter of U+00A1..U+052F, the only
// code points certainWidth asks an East Asian Width question about, the answer equals golang.org/x/text/width's (Neutral or
// EastAsianNarrow: one column on every terminal). x/text/width is imported by this test only, so the pig binary does not link its table.
func TestCertainWidthAgreesWithXTextWidth(t *testing.T) {
	letters, certain := 0, 0
	for r := rune(0xa1); r < 0x530; r++ {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		kind := width.LookupRune(r).Kind()
		want := kind == width.Neutral || kind == width.EastAsianNarrow
		if want {
			certain++
		}
		if got := certainWidth(string(r)); got != want {
			t.Errorf("certainWidth(U+%04X) = %v, x/text/width says %v (kind %v)", r, got, want, kind)
		}
	}
	// Both answers must occur, or the loop compared nothing: the range holds ambiguous letters (U+00E0) and neutral ones (U+00C0).
	if letters == 0 || certain == 0 || certain == letters {
		t.Fatalf("letters = %d, certain = %d: the table is empty or one-sided", letters, certain)
	}
	if certainWidth("\u00e0") || !certainWidth("\u00c0") {
		t.Fatalf("certainWidth(U+00E0) = %v, certainWidth(U+00C0) = %v, want false and true", certainWidth("\u00e0"), certainWidth("\u00c0"))
	}
}
