package chordjson

import (
	"slices"
	"testing"
)

// JavaScript treats a key as an array index only up to 2^32-2 (ECMA-262 array index: a canonical numeric string below 2^32-1):
// "4294967294" orders among the indices, while "4294967295" is an ordinary string key and keeps its insertion place after them.
// Measured in Node: Object.keys({"4294967295":1, "0x":2, "4294967294":3, "7":4}) = ["7", "4294967294", "4294967295", "0x"].
func TestOwnKeysArrayIndexStopsBelowTwoToTheThirtyTwoMinusOne(t *testing.T) {
	got := OwnKeys(ObjectOf("4294967295", 1.0, "0x", 2.0, "4294967294", 3.0, "7", 4.0))
	if want := []string{"7", "4294967294", "4294967295", "0x"}; !slices.Equal(got, want) {
		t.Fatalf("OwnKeys = %q, want %q", got, want)
	}
	// A Go map has no insertion order, so its other keys follow in ascending order.
	got = MapOwnKeys(map[string]any{"4294967295": 1, "0x": 2, "4294967294": 3, "7": 4})
	if want := []string{"7", "4294967294", "0x", "4294967295"}; !slices.Equal(got, want) {
		t.Fatalf("MapOwnKeys = %q, want %q", got, want)
	}
}
