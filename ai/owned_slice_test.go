package ai

import (
	"strings"
	"testing"
)

// Pi utils/text.ts:48-50 ownedSlice: `text.slice(start, end)` as a string of its own, so a retained excerpt does not keep
// its long source alive.
func TestOwnedSliceCopiesItsExcerpt(t *testing.T) {
	source := strings.Repeat("abcdefghij", 1000)
	got := OwnedSlice(source, 0, 500)
	if got != source[:500] {
		t.Fatalf("OwnedSlice = %q, want the first 500 units", got)
	}
	if aliases(source, got) {
		t.Error("OwnedSlice result shares the source's backing array")
	}
	if rest := OwnedSlice(source, 9990); rest != "abcdefghij" || aliases(source, rest) {
		t.Errorf("OwnedSlice with the end omitted = %q (aliases %v)", rest, aliases(source, rest))
	}
	if OwnedSlice(source, 3, 3) != "" || OwnedSlice("abc", -2) != "bc" {
		t.Error("OwnedSlice does not follow String.slice offsets")
	}
}
