//go:build !race

package delta

import (
	"fmt"
	"runtime"
	"testing"
)

// Pi f715a15c (#9265): an append op's text is a copy of its own, so ops built from appends to a growing text do not each keep the whole text they were cut from alive. A Go substring shares its parent's bytes, so the emit sites clone. The heap is measured after GC with the ops still held, so the race detector's allocation inflation is kept out.

const retentionAppend = "12345678"

func heapAfterGC() uint64 {
	var stats runtime.MemStats
	for range 3 {
		runtime.GC()
	}
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// retainedTrackerOps is the heap the ops of n commits hold, each commit appending 8 bytes to a text by Set.
func retainedTrackerOps(t testing.TB, n int) (int64, [][]Op) {
	before := heapAfterGC()
	tracker := Track(JsonObjectOf("text", ""))
	kept := make([][]Op, 0, n)
	for range n {
		change := tracker.BeginChange()
		state := change.State()
		_ = state.Set("text", state.Get("text").(string)+retentionAppend)
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		kept = append(kept, prepared.Ops())
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatal(err)
		}
	}
	tracker = nil
	return int64(heapAfterGC()) - int64(before), kept
}

// retainedDiffOps is the heap the ops of n DiffRevisions calls hold, each revision 8 bytes longer.
func retainedDiffOps(n int) (int64, [][]Op) {
	before := heapAfterGC()
	kept := make([][]Op, 0, n)
	text := ""
	previous := JsonValue(JsonObjectOf("text", text))
	for range n {
		text += retentionAppend
		next := JsonValue(JsonObjectOf("text", text))
		kept = append(kept, DiffRevisions(previous, next))
		previous = next
	}
	return int64(heapAfterGC()) - int64(before), kept
}

func TestAppendOpsDoNotRetainTheirSourceText(t *testing.T) {
	const commits, bound = 10_000, 2 << 20
	t.Run("tracker", func(t *testing.T) {
		retained, kept := retainedTrackerOps(t, commits)
		if retained > bound {
			t.Fatalf("the ops of %d appends of %d bytes retain %d bytes, more than %d", commits, len(retentionAppend), retained, bound)
		}
		for at, ops := range kept {
			if len(ops) != 1 || ops[0][0] != "a" || ops[0][2] != retentionAppend {
				t.Fatalf("commit %d ops = %v, want one append of %q", at, ops, retentionAppend)
			}
		}
	})
	t.Run("diff", func(t *testing.T) {
		retained, kept := retainedDiffOps(commits)
		if retained > bound {
			t.Fatalf("the ops of %d revisions 8 bytes longer retain %d bytes, more than %d", commits, retained, bound)
		}
		for at, ops := range kept {
			if len(ops) != 1 || ops[0][0] != "a" || ops[0][2] != retentionAppend {
				t.Fatalf("revision %d ops = %v, want one append of %q", at, ops, retentionAppend)
			}
		}
	})
}

// BenchmarkAppendOpRetention reports the heap held by the ops of n appends (retained-B) beside the cost of making them.
func BenchmarkAppendOpRetention(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("tracker/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			var retained int64
			for b.Loop() {
				var kept [][]Op
				retained, kept = retainedTrackerOps(b, n)
				runtime.KeepAlive(kept)
			}
			b.ReportMetric(float64(retained), "retained-B")
		})
		b.Run(fmt.Sprintf("diff/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			var retained int64
			for b.Loop() {
				var kept [][]Op
				retained, kept = retainedDiffOps(n)
				runtime.KeepAlive(kept)
			}
			b.ReportMetric(float64(retained), "retained-B")
		})
	}
}
