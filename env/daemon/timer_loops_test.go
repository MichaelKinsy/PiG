// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"runtime"
	"testing"
	"time"
)

// parkedIn waits until a goroutine whose stack holds function is blocked in a select, so a wake the caller sends next
// starts a new pass of that goroutine's loop instead of merging with the pass it is still running.
func parkedIn(t *testing.T, buf []byte, function string) {
	t.Helper()
	want := []byte(function)
	deadline := time.Now().Add(10 * time.Second)
	for {
		stacks := buf[:runtime.Stack(buf, true)]
		for stack := range bytes.SplitSeq(stacks, []byte("\n\n")) {
			if bytes.Contains(stack, want) && bytes.Contains(stack, []byte(" [select")) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no goroutine is waiting in %s", function)
		}
		runtime.Gosched()
	}
}

// waitForRoom's poll is what notices room that appeared without a drain signal: the loop must keep waking on its timer.
func TestWaitForRoomPollSeesRoomWithoutADrainSignal(t *testing.T) {
	o := newOutput()
	o.bulkBytes = bulkLimit + 1
	stop := make(chan struct{})
	defer close(stop)
	returned := make(chan struct{})
	go func() {
		o.waitForRoom(stop)
		close(returned)
	}()
	parkedIn(t, make([]byte, 1<<20), "(*output).waitForRoom")
	o.mu.Lock()
	o.bulkBytes = 0
	o.mu.Unlock()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("waitForRoom never re-checked the unsent bytes on its poll")
	}
}

// Every pass of waitForRoom re-arms the one poll timer: a pass allocates nothing, where a time.After per pass
// allocated a timer, its channel and a closure. Each wake below is a drain signal sent while the loop is parked, so
// the test counts whole passes of the production loop.
func TestWaitForRoomPassAllocatesNothing(t *testing.T) {
	const passes = 256
	o := newOutput()
	o.bulkBytes = bulkLimit + 1
	next := make([]chan struct{}, passes)
	for i := range next {
		next[i] = make(chan struct{})
	}
	stop := make(chan struct{})
	returned := make(chan struct{})
	go func() {
		o.waitForRoom(stop)
		close(returned)
	}()
	buf := make([]byte, 1<<20)
	parkedIn(t, buf, "(*output).waitForRoom")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range passes {
		o.mu.Lock()
		drained := o.drained
		o.drained = next[i]
		o.mu.Unlock()
		close(drained)
		parkedIn(t, buf, "(*output).waitForRoom")
	}
	runtime.ReadMemStats(&after)
	close(stop)
	<-returned
	// One allocation per pass, or any part of the three time.After made, fails; the margin below half an allocation
	// per pass absorbs what the runtime and the race detector allocate on their own meanwhile (measured 0-13).
	if got := after.Mallocs - before.Mallocs; got >= passes/2 {
		t.Fatalf("%d passes of waitForRoom allocated %d objects, want fewer than one per two passes (none per pass)", passes, got)
	}
}
