// SPDX-License-Identifier: MIT

package codingagent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// retainedHeap is the heap in use after two collections.
func retainedHeap() int64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return int64(stats.HeapAlloc)
}

// A read of scattered records joins the records within fileRunGap of each other into one read of up to fileRunMax bytes. An entry decoded
// from it must own its record: an entry that kept a slice of the joined read would pin the whole read, the records in the gaps included, for as
// long as the entry is resident. The bound is independent of the read path: the same entries read one at a time own exactly their records, and
// the joined read may retain no more than a small fraction over them.
func TestEntriesDecodedFromAJoinedReadDoNotPinTheRead(t *testing.T) {
	const (
		entries = 1500
		bodyLen = 20 << 10 // a record of 20 KB; every third one is read, so two thirds of a joined read are gaps within fileRunGap
	)
	dir := t.TempDir()
	live, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		if _, err := live.AppendMessage(mkUserMsg(fmt.Sprintf("%06d %s", i, strings.Repeat("x", bodyLen)))); err != nil {
			t.Fatal(err)
		}
	}
	open := func() *Session {
		session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	var want []int32
	for i := 0; i < entries-retainTailEntries; i += 3 {
		want = append(want, int32(i))
	}

	one := open()
	before := retainedHeap()
	singles := make([]SessionEntry, 0, len(want))
	one.mu.Lock()
	for _, i := range want {
		singles = append(singles, one.readSlotsLocked([]int32{i})[0])
	}
	one.mu.Unlock()
	alone := retainedHeap() - before

	joined := open()
	before = retainedHeap()
	joined.mu.Lock()
	together := joined.readSlotsLocked(want)
	joined.mu.Unlock()
	pinned := retainedHeap() - before

	runtime.KeepAlive(singles)
	runtime.KeepAlive(together)
	if alone <= int64(len(want))*bodyLen {
		t.Fatalf("the reads one at a time retain %d B for %d records of %d B: the measure is not seeing the entries", alone, len(want), bodyLen)
	}
	t.Logf("%d records: read one at a time retain %d B, read together %d B (%.2f x)", len(want), alone, pinned, float64(pinned)/float64(alone))
	if pinned > alone+alone/10 {
		t.Fatalf("entries decoded from one joined read retain %d B, %.2f x the %d B of the same entries read one at a time: they pin the read's buffer", pinned, float64(pinned)/float64(alone), alone)
	}
}
