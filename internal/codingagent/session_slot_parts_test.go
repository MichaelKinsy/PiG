package codingagent

import (
	"reflect"
	"strings"
	"testing"
)

// Cold parts are numbered in order: each number names its own place, within its chunk's capacity, and the chunks fill one after another.
func TestColdPartNumbersFillTheChunksInOrder(t *testing.T) {
	type place struct {
		chunk int
		at    uint32
	}
	seen := map[place]bool{}
	last := place{0, 0}
	for ref := range uint32(20000) {
		chunk, at := coldChunkOf(ref)
		p := place{chunk, at}
		if seen[p] || int(at) >= coldChunkCap(chunk) {
			t.Fatalf("ref %d: chunk %d place %d (cap %d) taken or out of the chunk", ref, chunk, at, coldChunkCap(chunk))
		}
		next := chunk == last.chunk && at == last.at+1
		nextChunk := chunk == last.chunk+1 && at == 0 && int(last.at)+1 == coldChunkCap(last.chunk)
		if ref > 0 && !next && !nextChunk {
			t.Fatalf("ref %d: chunk %d place %d does not follow chunk %d place %d", ref, chunk, at, last.chunk, last.at)
		}
		seen[p], last = true, p
	}
	s := NewSession("cold", t.TempDir())
	slots := make([]entrySlot, 3000)
	for i := range slots {
		s.coldLocked(&slots[i]).off = int64(i)
	}
	for i := range slots {
		if got := s.coldPart(&slots[i]).off; got != int64(i) {
			t.Fatalf("slot %d reads cold part %d", i, got)
		}
	}
}

// The facts arena gives back the facts stored in it.
func TestFactsArenaRoundTrips(t *testing.T) {
	s := NewSession("facts", t.TempDir())
	cases := []*treeFacts{
		{},
		{suppressible: true},
		{callIDs: []string{"call_1"}},
		{suppressible: true, callIDs: []string{"a", "", strings.Repeat("x", 300), "é\x00\xff"}},
	}
	refs := make([]uint32, len(cases))
	for i, facts := range cases {
		refs[i] = s.storeFactsLocked(facts)
	}
	for i, facts := range cases {
		if got := s.factsLocked(refs[i]); !reflect.DeepEqual(got, facts) {
			t.Fatalf("facts %d read back as %+v, stored %+v", i, got, facts)
		}
	}
	if s.factsLocked(0) != nil {
		t.Fatal("ref 0 reads facts")
	}
}
