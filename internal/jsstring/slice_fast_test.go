package jsstring

import (
	"math/rand/v2"
	"testing"
)

// Slice and Splice take byte offsets when the text is valid UTF-8; every result equals the UTF-16 unit computation, including offsets inside a
// surrogate pair, negative and out-of-range offsets, WTF-8 surrogate halves and invalid bytes.
func TestSliceAndSpliceByteOffsetsMatchTheUnitComputation(t *testing.T) {
	pieces := []string{"", "a", "é", "中", "😀", "👍🏽", "\xed\xa0\xbd", "\xed\xb8\x80", "\xff", "\ufffd", "e\u0301", "[paste #1]", "\n", "ab"}
	rng := rand.New(rand.NewPCG(1, 2))
	text := func() string {
		s := ""
		for range rng.IntN(6) {
			s += pieces[rng.IntN(len(pieces))]
		}
		return s
	}
	for range 20000 {
		value, insert := text(), text()
		length := Length(value)
		a, b := rng.IntN(length+5)-2-length/2, rng.IntN(length+5)-2
		if got, want := Slice(value, a, b), sliceUnits(value, a, b); got != want {
			t.Fatalf("Slice(%q, %d, %d) = %q, want %q", value, a, b, got, want)
		}
		if got, want := Slice(value, a), sliceUnits(value, a); got != want {
			t.Fatalf("Slice(%q, %d) = %q, want %q", value, a, got, want)
		}
		if got, want := Splice(value, a, b, insert), spliceUnits(value, a, b, insert); got != want {
			t.Fatalf("Splice(%q, %d, %d, %q) = %q, want %q", value, a, b, insert, got, want)
		}
	}
}

func TestSliceAndSpliceOfValidTextAllocateOnlyTheResult(t *testing.T) {
	line := "a line of the draft with several words in it, é and 😀"
	if got := testing.AllocsPerRun(100, func() { _ = Slice(line, 3, 40) }); got != 0 {
		t.Errorf("Slice allocates %v times", got)
	}
	if got := testing.AllocsPerRun(100, func() { _ = Splice(line, 10, 10, "x") }); got != 1 {
		t.Errorf("Splice allocates %v times, want the result only", got)
	}
}
