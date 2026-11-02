//go:build !race

package delta

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Pi da412b03 and d8d99950 rely on V8 ropes: a writer's append and the queue's merge of two appends to one path cost the appended text, not the text so far. A Go string is flat, so a stream of n 8-byte appends that copies the whole text each time allocates n*n*4 bytes.

// appendGrowth is the most a cost may grow over a tenfold longer stream: a linear path grows tenfold.
const appendGrowth = 12

func checkAppendGrowth(t *testing.T, name string, prepare func(n int) func()) {
	t.Helper()
	for _, size := range []int{100, 1_000} {
		last := testscaling.Check(t, testscaling.Case{Name: fmt.Sprintf("%s, %d to %d", name, size, 10*size), Size: size, Bound: testscaling.Linear, Prepare: prepare})
		if last.Large.Bytes > appendGrowth*last.Small.Bytes || last.Large.Allocs > appendGrowth*last.Small.Allocs {
			t.Errorf("%s: %d to %d appends grew %.1fx in bytes and %.1fx in allocations, want at most %dx", name, size, 10*size, last.Large.Bytes/last.Small.Bytes, last.Large.Allocs/last.Small.Allocs, appendGrowth)
		}
	}
}

// A stream of AppendText commits costs what a stream of commits that set a number costs, however long the text grows: the new revision shares the bytes the text already has.
func TestScalingTrackerAppendTextStream(t *testing.T) {
	const n = 10_000
	stream := func(n int) func() { return func() { streamAppends(t, n, "AppendText") } }
	checkAppendGrowth(t, "tracker AppendText stream", stream)
	appended := testscaling.Measure(stream, n, 3)
	control := testscaling.Measure(func(n int) func() { return func() { setCounts(t, n) } }, n, 3)
	if appended.Bytes > 1.5*control.Bytes {
		t.Errorf("%d AppendText commits allocated %.0f bytes, want at most 1.5x the %.0f bytes of %d commits that set a number", n, appended.Bytes, control.Bytes, n)
	}
}

// Folding n appends to one path allocates in proportion to n, not to the text so far.
func TestScalingPendingOpsFoldAppends(t *testing.T) {
	const n = 10_000
	fold := func(n int) func() {
		batch := []Op{{"a", []any{"text"}, appendDelta}}
		return func() {
			var pending PendingOps
			for range n {
				pending.Push(batch)
			}
			if take := pending.Take(); len(take.Ops) != 1 || len(take.Ops[0][2].(string)) != n*len(appendDelta) {
				t.Errorf("folded %d appends into %d ops", n, len(take.Ops))
			}
		}
	}
	checkAppendGrowth(t, "PendingOps fold of appends to one path", fold)
	if folded := testscaling.Measure(fold, n, 3); folded.Bytes > 10<<20 {
		t.Errorf("folding %d appends allocated %.0f bytes, want under 10 MiB", n, folded.Bytes)
	}
}
