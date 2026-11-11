// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"errors"
	"slices"
	"testing"
	"unsafe"
)

// The microtask queue drains and refills from the front of its array: a loop that runs the same few microtasks again and again
// allocates no queue after the first round. Popping with queue[1:] left the queue with less room on every item, so the next
// Defer grew a new array (one allocation per refill, the largest allocation after the jv trees in a commit job's crossings).
func TestMicrotaskQueueIsReusedAfterItDrains(t *testing.T) {
	l := &Loop{}
	ran := 0
	tick := func() { ran++ }
	round := func() {
		l.Defer(tick)
		l.Defer(tick)
		l.Defer(tick)
		if err := l.Run(); err != nil {
			t.Fatal(err)
		}
	}
	round()
	if allocs := testing.AllocsPerRun(100, round); allocs != 0 {
		t.Fatalf("a refilled queue allocates %v times per round", allocs)
	}
	if ran == 0 || !l.Idle() {
		t.Fatalf("ran %d, idle %v", ran, l.Idle())
	}
}

// A microtask that queues another while the queue is at its last item runs it next, in order, after the ones queued before.
func TestMicrotaskQueueKeepsItsOrderAcrossARestart(t *testing.T) {
	l := &Loop{}
	var order []int
	l.Defer(func() { order = append(order, 1); l.Defer(func() { order = append(order, 3) }) })
	l.Defer(func() { order = append(order, 2) })
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	l.Defer(func() { order = append(order, 4) })
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 4 || order[0] != 1 || order[1] != 2 || order[2] != 3 || order[3] != 4 {
		t.Fatalf("order = %v", order)
	}
}

// A queue that never drains within one Run keeps an array the size of what is queued, not of everything that ran: each item
// queues one more while another waits, so two items are always queued and thousands run. With the spent front of the array
// kept until the queue drains, the array grew with every item that ran.
func TestMicrotaskQueueStaysTheSizeOfWhatIsQueued(t *testing.T) {
	l := &Loop{}
	const runs = 10_000
	ran, most := 0, 0
	var item func()
	item = func() {
		most = max(most, cap(l.queue))
		if ran++; ran < runs {
			l.Defer(item)
		}
	}
	l.Defer(item)
	l.Defer(item)
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	if ran != runs+1 || !l.Idle() {
		t.Fatalf("ran %d, idle %v", ran, l.Idle())
	}
	if most > 16 {
		t.Fatalf("the queue's array reached %d slots for 2 queued items", most)
	}
}

// Moving the queued items to the front of the array keeps their order, and a microtask queued during the move runs last.
func TestMicrotaskQueueKeepsItsOrderAcrossACompaction(t *testing.T) {
	l := &Loop{}
	var order []int
	next := 0
	var item func()
	item = func() {
		order = append(order, next)
		if next++; next < 200 {
			l.Defer(item)
		}
	}
	for range 3 {
		l.Defer(item)
	}
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("order[%d] = %d", i, v)
		}
	}
	if len(order) != 202 {
		t.Fatalf("ran %d", len(order))
	}
}

// IsNeedRead and IsNeedPublication run for every job, almost always with a nil error: that answer allocates nothing.
func TestIsNeedOfNoErrorAllocatesNothing(t *testing.T) {
	if allocs := testing.AllocsPerRun(100, func() {
		if _, ok := IsNeedRead(nil); ok {
			t.Fatal("nil is a NeedRead")
		}
		if IsNeedPublication(nil) {
			t.Fatal("nil is a NeedPublication")
		}
	}); allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

// A job on the line takes the steps after its body (the line's release, the fence and the delivery of its outcome) from stages the job
// itself holds, so the only allocation per job is the job itself, whether the microtask queue counts the ticks or a host's hops do.
func TestAJobOnTheLineAllocatesOnlyItself(t *testing.T) {
	l := &Loop{}
	run := func() error { return nil }
	order := 0
	done := func(error) { order++ }
	round := func() {
		l.LineAfterTail(run, nil)
		l.LineAfterTail(run, nil)
		if err := l.Run(); err != nil {
			t.Fatal(err)
		}
	}
	round()
	if allocs := testing.AllocsPerRun(100, round); allocs > 2 {
		t.Fatalf("%v allocations for two jobs: more than the jobs themselves", allocs)
	}
	l.Line(run, done)
	if err := l.Run(); err != nil || order != 1 {
		t.Fatalf("done ran %d times, err %v", order, err)
	}
}

// A job queued while another's work has not returned does not start before the line's release, which is LineRelease ticks after the
// return of the first job's work; its caller resumes after that, a tick later than the second job starts, and both callers resume in line
// order.
func TestAJobStartsWhenTheLineIsFreeAndOutcomesFollowInLineOrder(t *testing.T) {
	l := &Loop{}
	var events []string
	l.Elapsed = func(Ended) (int, int) { return 0, 2 }
	errA, errB := errors.New("a"), errors.New("b")
	l.Line(func() error { events = append(events, "run a"); return errA }, func(err error) { events = append(events, "done a "+err.Error()) })
	l.Line(func() error { events = append(events, "run b"); return errB }, func(err error) { events = append(events, "done b "+err.Error()) })
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"run a", "run b", "done a a", "done b b"}; !slices.Equal(events, want) {
		t.Fatalf("events %v, want %v", events, want)
	}
}

// Starting a line job moves none of the jobs that wait behind it: the pop is O(1), so a line that drains n queued jobs costs
// O(n). Moving the queue down by one on each pop made draining n jobs O(n^2) (200,000 queued jobs: 5.1 s instead of 22 ms).
func TestStartingALineJobMovesNoWaitingJob(t *testing.T) {
	l := &Loop{}
	var held Resumer
	l.Hops = func(n int, r Resumer) bool { held = r; return true }
	run := func() error { return nil }
	for range 4 {
		l.Line(run, nil)
	}
	slot := len(l.jobs) - 1
	last := l.jobs[slot]
	l.Elapsed = func(Ended) (int, int) { return 0, 1 }
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	if held == nil || l.waiting() != 3 {
		t.Fatalf("the first job did not start and hold (%d waiting)", l.waiting())
	}
	if slot >= len(l.jobs) || l.jobs[slot] != last {
		t.Fatal("starting the first job moved the jobs waiting behind it")
	}
}

// A line that never drains keeps an array the size of the jobs that wait, and runs them in order: each job queues the next while
// two wait, so thousands run through an array that stays small.
func TestLineJobsStayTheSizeOfWhatWaitsAndKeepTheirOrder(t *testing.T) {
	l := &Loop{}
	const runs = 10_000
	var order []int
	most, next := 0, 0
	var queue func()
	queue = func() {
		id := next
		next++
		l.Line(func() error {
			order = append(order, id)
			if next < runs {
				queue()
			}
			most = max(most, cap(l.jobs))
			return nil
		}, nil)
	}
	queue()
	queue()
	queue()
	if err := l.Run(); err != nil {
		t.Fatal(err)
	}
	if len(order) != runs || !l.Idle() {
		t.Fatalf("ran %d of %d, idle %v", len(order), runs, l.Idle())
	}
	for i, id := range order {
		if id != i {
			t.Fatalf("job %d ran at position %d", id, i)
		}
	}
	if most > 16 {
		t.Fatalf("the line's array grew to %d for 3 waiting jobs", most)
	}
}

// A queued job holds its place in the line and nothing of a running one (jobRun): a task graph queues 20,000 of them at once.
func TestAQueuedJobIsNoLargerThanItsLineState(t *testing.T) {
	if size := unsafe.Sizeof(job{}); size > 112 {
		t.Fatalf("a job is %d bytes, more than 112", size)
	}
}
