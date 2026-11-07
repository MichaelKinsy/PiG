//go:build !race

package jsstring

import (
	"strings"
	"testing"
)

// ToLower of text with a non-ASCII character decodes up to 32 code points on the stack, as []rune(s) did, so its only allocation is the result: that is all the cost of the lowercase
// memo of a /tree search over a session-sized tree (BenchmarkTreeRecompute: 11328 allocs/op in the allocation baseline). Decoding into a heap slice and converting a byte slice with
// string(...) (d71c4e2361, which kept lone surrogates) cost two more allocations per text and raised that benchmark to 16254 allocs/op.
func TestToLowerOfShortNonASCIITextAllocatesItsResultOnly(t *testing.T) {
	for _, text := range []string{"Éclair au Chocolat", "ΣΑΣ ÄÖÜ", "toolResult: ÄÖÜ \xed\xa0\xbd ERROR", "plain ASCII except é", "İ"} {
		want := 1.0
		if strings.ContainsRune(text, 'İ') {
			want = 2 // U+0130 lowercases to three bytes where it takes two, so the result outgrows its buffer once
		}
		if allocs := testing.AllocsPerRun(100, func() { _ = ToLower(text) }); allocs > want {
			t.Errorf("ToLower(%q) allocs = %v, want at most %v", text, allocs, want)
		}
	}
}

// Text of more than 32 code points decodes into one heap slice besides the result.
func TestToLowerOfLongNonASCIITextAllocatesTwice(t *testing.T) {
	text := strings.Repeat("Éclair ", 12)
	if allocs := testing.AllocsPerRun(100, func() { _ = ToLower(text) }); allocs > 3 {
		t.Errorf("ToLower of %d bytes allocs = %v, want at most 3 (the slice's growth to 32 and beyond, and the result)", len(text), allocs)
	}
}

// UpperRune of one character without a special expansion converts the rune once.
func TestUpperRuneOfAPlainCharacterAllocatesOnce(t *testing.T) {
	var sink string
	for _, r := range []rune{'é', 'ж', 'ǆ', 0xd800} {
		if allocs := testing.AllocsPerRun(100, func() { sink = UpperRune(r) }); allocs > 1 {
			t.Errorf("UpperRune(%U) allocs = %v, want at most 1", r, allocs)
		}
	}
	_ = sink
}
