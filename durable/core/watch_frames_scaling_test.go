// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// allocated is the bytes fn allocates.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A task commit publishes the graph's ops, not the graph: the bytes one commit allocates for an open graph watch do not grow with
// the live tasks.
func TestGraphWatchCommitCostDoesNotGrowWithLiveTasks(t *testing.T) {
	cost := func(n int) uint64 {
		h := graphHandler(t, n)
		h.graph = &taskGraph{value: h.buildTaskGraph(), watches: []uint32{1}}
		next := int64(2*n + 10)
		return allocated(func() {
			for range 20 {
				rec := parsed(t, `{"id":`+itoa(next)+`,"conversationId":1,"kind":"k","background":false,"abortRequested":false,"state":{"status":"pending","checkpoint":{"phase":"start"}}}`)
				next++
				h.advanceTaskGraph(&session.Publication{Changes: []session.Change{{Kind: session.ChangeTable, Table: session.WriteTask, Record: rec}}})
				h.b.Reset()
			}
		})
	}
	small, large := cost(1000), cost(8000)
	t.Logf("20 task commits: 1000 live tasks %d B, 8000 live tasks %d B", small, large)
	if float64(large) > 1.5*float64(small) {
		t.Fatalf("8x the live tasks allocates %.1fx the bytes per commit", float64(large)/float64(small))
	}
}

// An entry commit of an open view allocates the same with a window of 2,000 entries as with one of 8,000.
func TestViewWatchCommitCostDoesNotGrowWithTheWindow(t *testing.T) {
	cost := func(n int) uint64 {
		w := &viewWatch{conv: 1, min: n / 2, max: n, docs: map[string]viewDoc{}}
		for i := range n {
			w.ids = append(w.ids, float64(i+1))
		}
		next := n + 1
		commit := func() {
			rec := parsed(t, `{"id":`+itoa(int64(next))+`,"conversationId":1,"kind":"m"}`)
			next++
			w.advance(&session.Publication{Changes: []session.Change{{Kind: session.ChangeTable, Table: session.WriteEntry, Record: rec}}})
		}
		commit() // the first append drops the oldest down to min; the rest are amortized
		return allocated(func() {
			for range 20 {
				commit()
			}
		})
	}
	small, large := cost(2000), cost(8000)
	t.Logf("20 entry commits: a window of 2000 entries %d B, of 8000 entries %d B", small, large)
	if float64(large) > 1.5*float64(small) {
		t.Fatalf("4x the window allocates %.1fx the bytes per commit", float64(large)/float64(small))
	}
}
