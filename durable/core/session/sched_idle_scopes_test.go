package session_test

// Pi main 35dfa757 (packages/durable/src/harness/scheduler.ts): idle checks without per-commit scans. After a commit that changed a
// task, the scheduler re-checked every conversation with a kept context range by walking its subtree, and scanned them again for
// the next expiry, so the cost of a commit grew with the number of conversations read in the last contextRetentionMs. Now a commit
// re-checks only the conversations above the tasks that became live or ended, and idle ranges expire from an index ordered by
// idleSince. wake() already takes the earliest sleep from the sleeping tasks (TestWakeNamesTheEarliestSleepWhileATaskRuns).

import "testing"

func (f *idleFixture) expectState(contexts, idle int) {
	f.t.Helper()
	gotContexts, gotIdle, mismatches, _ := f.sched.IdleState()
	if gotContexts != contexts || gotIdle != idle || mismatches != 0 {
		f.t.Fatalf("kept %d, idle %d, mismatches %d; want kept %d, idle %d, no mismatches", gotContexts, gotIdle, mismatches, contexts, idle)
	}
}

func TestKeptContextsExpireInIdleOrderWhenTheClockGoesBack(t *testing.T) {
	f := newIdleFixture(t)
	a, b := f.conversation(), f.conversation()
	f.sched.KeepContext(a, 0, 5, false)
	f.now = 500
	f.sched.KeepContext(b, 0, 5, false)
	f.expectState(2, 2)
	// B went idle at 500 and expires first, though it went idle last: the timer is for it.
	if got := f.armed[len(f.armed)-1]; got != 1500 {
		t.Fatalf("the expiry timer is for %v, want 1500 (B)", got)
	}
	f.now = 1600
	f.fire()
	if _, _, ok := f.sched.KeptContext(b); ok {
		t.Fatal("B is kept past its retention")
	}
	if _, _, ok := f.sched.KeptContext(a); !ok {
		t.Fatal("A, idle for 600 ms of 1000, expired with B")
	}
	f.expectState(1, 1)
	if len(f.released) != 1 || f.released[0] != b {
		t.Fatalf("released %v, want only B", f.released)
	}
}

func TestKeptContextsAreAllDroppedWhenRetentionBecomesZero(t *testing.T) {
	f := newIdleFixture(t)
	a, z := f.conversation(), f.conversation()
	_ = z
	f.now = 2000
	f.sched.KeepContext(a, 0, 5, false)
	f.expectState(1, 1)
	// The clock goes back below A's idle start; then retention 0 and a task change.
	f.now = 1000
	f.retention = 0
	f.end(f.task())
	f.expectState(0, 0)
}

func TestAKeptContextKeepsItsExpiryWhenReadAgain(t *testing.T) {
	f := newIdleFixture(t)
	a, b := f.conversation(), f.conversation()
	f.sched.KeepContext(a, 0, 5, false)
	f.now = 1500
	f.sched.KeepContext(b, 0, 5, false)
	f.now = 1800
	f.sched.KeepContext(a, 0, 6, false)
	f.now = 2100
	f.end(f.task())
	// A's period began at 1000 and was not extended by its second read; B's began at 1500.
	if _, _, ok := f.sched.KeptContext(a); ok {
		t.Fatal("A kept past the retention of its first idle read")
	}
	if _, _, ok := f.sched.KeptContext(b); !ok {
		t.Fatal("B dropped before its retention")
	}
	f.expectState(1, 1)
}

func TestATaskChangeSettlesTheConversationsAboveItWithoutACheckOfEveryScope(t *testing.T) {
	f := newIdleFixture(t)
	for range 20 {
		f.sched.KeepContext(f.conversation(), 0, 5, false)
	}
	f.expectState(20, 20)
	_, _, _, before := f.sched.IdleState()
	// The root has no kept range; a task in it is busy for the root only.
	id := f.task()
	f.expectState(20, 20)
	f.end(id)
	f.expectState(20, 20)
	if _, _, _, after := f.sched.IdleState(); after != before {
		t.Fatalf("%d idle checks covered every scope, want %d", after, before)
	}
}

// A kept range whose conversation gets work goes busy, and idle again with its end.
func TestAKeptContextGoesBusyWithWorkBelowItAndIdleAgainWhenItEnds(t *testing.T) {
	f := newIdleFixture(t)
	f.sched.KeepContext(f.root, 0, 5, false)
	f.expectState(1, 1)
	id := f.task()
	f.expectState(1, 0)
	f.end(id)
	f.expectState(1, 1)
}

// A commit that ends a task costs the same at any number of kept ranges: it settles the conversations above the task, where it once
// re-checked every kept range, so its time grew with the ranges (20 times from 200 to 4,000 kept ranges, where it is now flat).
func TestEndingATaskCostsTheSameAtAnyNumberOfKeptContexts(t *testing.T) {
	const few, many = 200, 4_000
	perCycle := func(contexts int) float64 {
		result := testing.Benchmark(func(b *testing.B) {
			f := newIdleFixture(b)
			for range contexts {
				f.sched.KeepContext(f.conversation(), 0, 5, false)
			}
			b.ResetTimer()
			for b.Loop() {
				f.end(f.task())
			}
		})
		return float64(result.NsPerOp())
	}
	small, large := perCycle(few), perCycle(many)
	if large > 4*small {
		t.Fatalf("a task's create and end takes %.0f ns with %d kept ranges and %.0f ns with %d: the cost of a commit grows with them", small, few, large, many)
	}
}
