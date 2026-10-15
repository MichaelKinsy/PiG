// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build !race

package sqlhost

import (
	"runtime"
	"testing"
)

// rangeCallBytes is the bytes a model_context range of conversation 2 allocates after one more turn, at a history of h turns:
// the least of three runs of 8 calls. The turn's own commit is not counted.
func rangeCallBytes(t *testing.T, h int) uint64 {
	t.Helper()
	r := newRangeDB(t)
	r.conversation(2, "")
	for i := range h {
		r.turn(2, i)
	}
	if _, err := r.h.rangeContext(2, 0, uint32(r.next-1)); err != nil {
		t.Fatal(err)
	}
	best := ^uint64(0)
	for run := range 3 {
		var total uint64
		for call := range 8 {
			r.turn(2, h+run*8+call)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if _, err := r.h.rangeContext(2, 0, uint32(r.next-1)); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			total += after.TotalAlloc - before.TotalAlloc
		}
		best = min(best, total/8)
	}
	return best
}

// TestRangeContextCostDoesNotGrowWithTheHistory checks that a model request of a conversation whose context grew by one turn
// since its last request costs the new messages, not the history: a turn issues several model requests, and building the
// whole message list per request made a warm turn grow with H (complexity audit, Durable warm turn).
func TestRangeContextCostDoesNotGrowWithTheHistory(t *testing.T) {
	small, large := rangeCallBytes(t, 200), rangeCallBytes(t, 2000)
	t.Logf("bytes per range after one turn: H=200 %d, H=2000 %d", small, large)
	if large > 2*small+4096 {
		t.Fatalf("a range after one more turn allocates %d bytes at H=2000 and %d at H=200: it grows with the history", large, small)
	}
}
