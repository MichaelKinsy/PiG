package sessiongen

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestWriteIsByteStable pins the bytes Write produces for a fixed seed: a change of the generator that changes a fixture shows here.
func TestWriteIsByteStable(t *testing.T) {
	const want = "3a1124c2a7712b2563554b9ac298e7ccc8c1d901d967750641780999bc7cd9e6"
	sum := sha256.New()
	result, err := Write(sum, Options{Tokens: 200000, Seed: 1, CompactEvery: 25, ForkEvery: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		t.Fatalf("sha256 of the session = %s, want %s (%+v)", got, want, result)
	}
}
