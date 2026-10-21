package jsloop

import (
	"reflect"
	"sync"
	"testing"
)

// An await that already settled still resumes after the input callback that is running: a microtask queued while the loop is held runs at End, behind the callback's own remaining statements.
func TestLoopMicrotaskRunsAfterTheHeldCallback(t *testing.T) {
	var loop Loop
	var order []string
	loop.Begin()
	loop.Microtask(func() { order = append(order, "microtask") })
	if len(order) != 0 {
		t.Fatalf("a microtask escaped the held loop: %v", order)
	}
	order = append(order, "callback")
	loop.End()
	loop.Microtask(func() { order = append(order, "later") })
	if want := []string{"callback", "microtask", "later"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// Node drains the microtask queue between tasks, so the microtasks a task queues run before the next task. Two tasks already queued must not run in one batch.
// mutation-checked: draining every queued macrotask at once runs [a1 b a2].
func TestLoopRunsEachMacrotaskAfterThePreviousMacrotasksMicrotasks(t *testing.T) {
	var loop Loop
	var order []string
	loop.Begin()
	loop.Macrotask(func() { order = append(order, "a1"); loop.Microtask(func() { order = append(order, "a2") }) })
	loop.Macrotask(func() { order = append(order, "b") })
	loop.End()
	if want := []string{"a1", "a2", "b"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// Every queued microtask runs before a macrotask, whatever order they were queued in.
func TestLoopRunsQueuedMicrotasksBeforeMacrotasks(t *testing.T) {
	var loop Loop
	var order []string
	loop.Begin()
	loop.Macrotask(func() { order = append(order, "macrotask") })
	loop.Microtask(func() { order = append(order, "m1") })
	loop.Microtasks(func() { order = append(order, "m2") }, func() { order = append(order, "m3") })
	loop.End()
	if want := []string{"m1", "m2", "m3", "macrotask"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// Hops(n, fn) runs fn after n microtasks, so a microtask queued earlier runs first; Hops(0, fn) runs it at once.
func TestLoopHopsCountsMicrotasks(t *testing.T) {
	var loop Loop
	var order []string
	loop.Begin()
	loop.Hops(2, func() { order = append(order, "hops 2") })
	loop.Microtask(func() { order = append(order, "m1") })
	loop.Microtask(func() { order = append(order, "m2") })
	loop.Hops(0, func() { order = append(order, "hops 0") })
	loop.End()
	if want := []string{"hops 0", "m1", "m2", "hops 2"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// A producer that finds the loop busy never waits for it: completions queued while the loop is held run at End, none is lost, and one that races the idle hand-off is not stranded.
func TestLoopProtectsLaterInputAndDoesNotStrandCallbacks(t *testing.T) {
	var loop Loop
	var mu sync.Mutex
	var ran int
	count := func() { mu.Lock(); ran++; mu.Unlock() }
	got := func() int { mu.Lock(); defer mu.Unlock(); return ran }
	loop.Begin()
	var work sync.WaitGroup
	for range 100 {
		work.Go(func() { loop.Microtask(count) })
	}
	work.Wait()
	if n := got(); n != 0 {
		t.Fatalf("%d callbacks ran inside the held loop", n)
	}
	loop.End()
	if n := got(); n != 100 {
		t.Fatalf("%d callbacks ran, want 100", n)
	}
	for range 100 {
		work.Go(func() { loop.Macrotask(count) })
	}
	for range 100 {
		loop.Begin()
		loop.End()
	}
	work.Wait()
	loop.Begin()
	loop.End()
	if n := got(); n != 200 {
		t.Fatalf("%d callbacks ran, want 200: a callback was stranded at the idle hand-off", n)
	}
}

// Idle reports when the held input and the callbacks queued behind it drain.
func TestLoopIdleClosesWhenTheQueueDrains(t *testing.T) {
	var loop Loop
	select {
	case <-loop.Idle():
	default:
		t.Fatal("a new loop is not idle")
	}
	loop.Begin()
	idle := loop.Idle()
	select {
	case <-idle:
		t.Fatal("a held loop is idle")
	default:
	}
	loop.Microtask(func() {})
	loop.End()
	select {
	case <-idle:
	default:
		t.Fatal("the loop is not idle after End")
	}
}

// A callback runs on the goroutine of the producer that found the loop idle, so there is no wake-up hop.
func TestLoopRunsInlineOnAnIdleLoop(t *testing.T) {
	var loop Loop
	ran := false
	loop.Microtask(func() { ran = true })
	if !ran {
		t.Fatal("a microtask queued on an idle loop did not run before Microtask returned")
	}
}

func BenchmarkLoopMicrotaskIdle(b *testing.B) {
	var loop Loop
	fn := func() {}
	b.ReportAllocs()
	for b.Loop() {
		loop.Microtask(fn)
	}
}

func BenchmarkLoopHeldBatch(b *testing.B) {
	var loop Loop
	fn := func() {}
	b.ReportAllocs()
	for b.Loop() {
		loop.Begin()
		loop.Microtask(fn)
		loop.Macrotask(fn)
		loop.End()
	}
}
