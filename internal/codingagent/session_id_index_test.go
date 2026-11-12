package codingagent

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// idIndex finds the newest slot of every id as a map from id to slot does, through growth and repeated ids, and finds no slot for an id no
// slot has; its count is the number of distinct ids, as byId.size is.
func TestIDIndexMatchesAMap(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	s := NewSession("ids", "/project")
	var index idIndex
	want := map[string]int{}
	for i := range 5000 {
		id := fmt.Sprintf("%08x", rng.Uint32())
		if i > 0 && rng.IntN(5) == 0 {
			id = s.idAt(rng.IntN(len(s.slots))) // an id appended again: the newest slot wins
		}
		s.slots = append(s.slots, entrySlot{idRef: s.ids.add(id)})
		index.set(s, len(s.slots)-1)
		want[id] = len(s.slots) - 1
		if i%97 == 0 {
			for id, slot := range want {
				if got, ok := index.find(s.slots, &s.ids, id); !ok || got != slot {
					t.Fatalf("after %d appends: find(%s) = %d %v, want %d", i+1, id, got, ok, slot)
				}
			}
		}
	}
	for _, id := range []string{"", "missing", "0000000g"} {
		if got, ok := index.find(s.slots, &s.ids, id); ok {
			t.Fatalf("find(%q) = %d, want none", id, got)
		}
	}
	if index.n != len(want) {
		t.Fatalf("index counts %d ids, want %d", index.n, len(want))
	}
	var empty idIndex
	if _, ok := empty.find(nil, &idArena{}, "x"); ok {
		t.Fatal("an empty index found an id")
	}
}

// The arena gives back every id it was given: the empty id, ids of every length up to and past what a chunk holds a length for, and enough of them to
// fill and add chunks; an id read before more were added reads the same after.
func TestIDArenaRoundTrips(t *testing.T) {
	var arena idArena
	var ids []string
	refs := map[int]uint32{}
	add := func(id string) {
		refs[len(ids)] = arena.add(id)
		ids = append(ids, id)
	}
	for _, id := range []string{"", "a", "0198f3a0-0000-7000-8000-000000000001", strings.Repeat("x", maxArenaID), strings.Repeat("y", maxArenaID+1), strings.Repeat("z", 1<<17), "é\x00\xff"} {
		add(id)
	}
	first := arena.get(refs[2])
	for i := range 40000 {
		add(fmt.Sprintf("%08x", i*2654435761))
	}
	for i, id := range ids {
		if got := arena.get(refs[i]); got != id {
			t.Fatalf("id %d reads %q, want %q", i, got, id)
		}
	}
	if first != ids[2] {
		t.Fatalf("an id read before the arena grew changed to %q", first)
	}
	if len(arena.chunks) < 3 || len(arena.long) != 2 {
		t.Fatalf("arena has %d chunks and %d long ids, want several chunks and the two ids too long for one", len(arena.chunks), len(arena.long))
	}
}

// The resident store gives back the entries set and frees a page when its last entry goes.
func TestResidentStoreSetsAndClears(t *testing.T) {
	var store residentStore
	entries := map[int]SessionEntry{}
	for _, i := range []int{0, 1, 255, 256, 1000, 70000} {
		entries[i] = RawEntry{Type: "x", ID: fmt.Sprint(i)}
		store.set(i, entries[i])
	}
	for i, want := range entries {
		if got := store.get(i); got == nil || got.Base().ID != want.Base().ID {
			t.Fatalf("slot %d holds %v, want %v", i, got, want)
		}
	}
	if store.get(2) != nil || store.get(100000) != nil {
		t.Fatal("a slot nobody set holds an entry")
	}
	pages := func() (n int) {
		for _, page := range store.pages {
			if page != nil {
				n++
			}
		}
		return n
	}
	if got := pages(); got != 4 {
		t.Fatalf("%d pages hold six entries that lie on four pages", got)
	}
	store.set(0, nil)
	store.set(1, nil)
	store.set(1, nil)
	if got := pages(); got != 4 || store.get(0) != nil || store.get(255) == nil {
		t.Fatalf("after clearing two of a page's three entries: %d pages", got)
	}
	store.set(255, nil)
	store.set(300, nil)
	if got := pages(); got != 3 {
		t.Fatalf("after clearing a page's last entry: %d pages, want 3", got)
	}
}
