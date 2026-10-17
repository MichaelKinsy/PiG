package session

import (
	"slices"
	"testing"
)

// An orderedSet past one chunk keeps JavaScript Set semantics (insertion order, re-adding moves a key to the end, Range
// visits keys added during the iteration) across chunk boundaries, through deletes and compaction, and never holds a
// slot slice longer than a chunk: U-harness-scheduler-retention's 20,000-deep chain trapped the 96 MiB TinyGo core
// growing one set's slots to 512 KiB in a fragmented heap.
func TestAnOrderedSetPastOneChunkKeepsSetOrderAndBoundsItsBlocks(t *testing.T) {
	const n = 3*setChunk + 17
	set := newOrderedSet[ID]()
	for i := range n {
		set.Add(ID(i + 1))
	}
	set.Add(ID(5)) // present: no change
	if set.Len() != n || !set.Has(ID(n)) || set.Has(ID(n+1)) {
		t.Fatalf("len %d, has last %v, has missing %v", set.Len(), set.Has(ID(n)), set.Has(ID(n+1)))
	}
	// No block above 1,024 slots (24 KiB of tree nodes), whatever the set's size.
	for _, chunk := range set.chunks {
		if cap(chunk) > 1024 {
			t.Fatalf("a chunk holds %d slots, more than 1024", cap(chunk))
		}
	}
	for _, index := range set.index {
		if len(index) > 1024 {
			t.Fatalf("an index map holds %d keys, more than 1024", len(index))
		}
	}
	// Delete the first two chunks' keys but one, which compacts, then re-add a deleted key: it goes to the end.
	for i := range 2 * setChunk {
		if i != 700 {
			set.Delete(ID(i + 1))
		}
	}
	set.Add(ID(1))
	want := []ID{701}
	for i := 2 * setChunk; i < n; i++ {
		want = append(want, ID(i+1))
	}
	want = append(want, 1)
	if got := set.Values(); !slices.Equal(got, want) {
		t.Fatalf("values: %d keys, first %v, last %v; want %d keys, first %v, last %v", len(got), got[:3], got[len(got)-1], len(want), want[:3], want[len(want)-1])
	}
	// Delete compacts once more than half the slots are dead (and more than 16), so dead slots never outnumber live ones.
	if dead := set.slots - set.Len(); dead > set.Len() {
		t.Fatalf("%d dead slots for %d keys: the dead slots were not compacted", dead, set.Len())
	}
	for _, k := range want {
		if !set.Has(k) {
			t.Fatalf("lost %d after compaction", k)
		}
	}
	// Keys added during Range are visited, across a chunk boundary.
	grow := newOrderedSet[ID]()
	grow.Add(1)
	var seen []ID
	grow.Range(func(k ID) bool {
		seen = append(seen, k)
		if k < setChunk+2 {
			grow.Add(k + 1)
		}
		return true
	})
	if len(seen) != setChunk+2 || seen[len(seen)-1] != setChunk+2 {
		t.Fatalf("Range visited %d keys, last %d; want %d", len(seen), seen[len(seen)-1], setChunk+2)
	}
	grow.Clear()
	if grow.Len() != 0 || grow.Has(1) || len(grow.Values()) != 0 {
		t.Fatalf("Clear left %d keys", grow.Len())
	}
}
