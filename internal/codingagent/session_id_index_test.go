package codingagent

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// idIndex finds the newest slot of every id as a map from id to slot does, through growth and repeated ids, and finds no slot for an id no
// slot has; its count is the number of distinct ids, as byId.size is.
func TestIDIndexMatchesAMap(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	var slots []entrySlot
	var index idIndex
	want := map[string]int{}
	for i := range 5000 {
		id := fmt.Sprintf("%08x", rng.Uint32())
		if i > 0 && rng.IntN(5) == 0 {
			id = slots[rng.IntN(len(slots))].id // an id appended again: the newest slot wins
		}
		slots = append(slots, entrySlot{id: id})
		index.set(slots, len(slots)-1)
		want[id] = len(slots) - 1
		if i%97 == 0 {
			for id, slot := range want {
				if got, ok := index.find(slots, id); !ok || got != slot {
					t.Fatalf("after %d appends: find(%s) = %d %v, want %d", i+1, id, got, ok, slot)
				}
			}
		}
	}
	for _, id := range []string{"", "missing", "0000000g"} {
		if got, ok := index.find(slots, id); ok {
			t.Fatalf("find(%q) = %d, want none", id, got)
		}
	}
	if index.n != len(want) {
		t.Fatalf("index counts %d ids, want %d", index.n, len(want))
	}
	var empty idIndex
	if _, ok := empty.find(nil, "x"); ok {
		t.Fatal("an empty index found an id")
	}
}
