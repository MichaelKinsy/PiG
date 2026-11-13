package sessiongen

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// TestHexMatchesFmt holds hex32 and hex64 to the fmt verbs they replaced, over the edge values and 20,000 random ids.
func TestHexMatchesFmt(t *testing.T) {
	check := func(v uint64) {
		t.Helper()
		if got, want := hex32(uint32(v)), fmt.Sprintf("%08x", uint32(v)); got != want {
			t.Fatalf("hex32(%d) = %q, want %q", uint32(v), got, want)
		}
		if got, want := "toolu_"+hex64(v), fmt.Sprintf("toolu_%016x", v); got != want {
			t.Fatalf("hex64(%d) = %q, want %q", v, got, want)
		}
	}
	for _, v := range []uint64{0, 1, 9, 10, 15, 16, 0xab, 0xfff, 0x0abcdef0, 1<<31 - 1, 1 << 31, 1<<32 - 1, 1 << 32, 0x0123456789abcdef, 1<<63 - 1, 1 << 63, 1<<64 - 1} {
		check(v)
	}
	rng := rand.New(rand.NewPCG(7, 11))
	for range 20000 {
		check(rng.Uint64())
	}
}
