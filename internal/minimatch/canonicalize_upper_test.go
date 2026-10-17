package minimatch

import (
	"slices"
	"testing"
)

// ECMAScript Canonicalize (non-unicode `i` flag) maps a character to its toUpperCase when that is a single character, else to itself. U+1F80 uppercases
// to the two characters U+1F08 U+0399 in JavaScript, so it is its own class; Go's simple mapping would send it to U+1F88 and merge the two.
func TestCanonicalizeUsesJavaScriptUpperCase(t *testing.T) {
	if got := canonicalize(0x1f80); got != 0x1f80 {
		t.Fatalf("canonicalize(U+1F80) = %U, want itself", got)
	}
	if got := canonicalMembers(0x1f80); !slices.Equal(got, []rune{0x1f80}) {
		t.Fatalf("canonicalMembers(U+1F80) = %U, want only U+1F80", got)
	}
}
