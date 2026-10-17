//go:build !race

package codingagent

import (
	"runtime"
	"testing"
)

// Opening a session indexes each line for what the session needs (its base members, accounting, location and an assistant's /tree facts);
// the /tree search texts are read when /tree opens, so an open makes no allocation for them and keeps none (PE-F8).
func TestOpenLeavesSearchTextsToTree(t *testing.T) {
	const turns = 300
	path, size := writeBulkSession(t, turns)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	s, _, err := loadPersistedSession(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if s.snapshot != nil {
		defer s.snapshot.close()
	}
	lines := float64(3 * turns)
	allocs, allocated := float64(after.Mallocs-before.Mallocs)/lines, float64(after.TotalAlloc-before.TotalAlloc)/float64(size)
	t.Logf("open of %d lines, %d bytes: %.1f allocations per line, %.3f bytes allocated per file byte", 3*turns, size, allocs, allocated)
	if allocs > openAllocsPerLine {
		t.Errorf("open made %.1f allocations per line, want at most %.1f", allocs, openAllocsPerLine)
	}
	for i := range s.slots {
		if s.coldPart(&s.slots[i]).searchAt != 0 {
			t.Fatalf("slot %d (%s) has a search text after the open", i, s.slots[i].id)
		}
	}
	if s.searchTexts.size != 0 {
		t.Fatalf("the open stored %d bytes of search text", s.searchTexts.size)
	}
	roots, lt := s.lazyTree()
	if len(roots) == 0 {
		t.Fatal("no tree")
	}
	lt.searchTexts(treeSearchableText)
	indexed := 0
	for i := range s.slots {
		if s.coldPart(&s.slots[i]).searchAt != 0 {
			indexed++
		}
	}
	if indexed == 0 {
		t.Fatal("the first search indexed no search text")
	}
}

// openAllocsPerLine bounds the allocations an open makes per line of the session file: 30.5 measured, 39.6 when the open also read each
// line's /tree search text.
const openAllocsPerLine = 33.0
