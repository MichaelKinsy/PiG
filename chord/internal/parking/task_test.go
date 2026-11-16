package parking

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// A task is not one goroutine: it runs on the goroutine that started it and, while a handler's coroutine is adopted for it, on
// the coroutine as well (the goroutine that adopted it waits for the coroutine). The registry says which goroutines run the
// task now, and Current which task a goroutine runs; an adoption that ended leaves nothing of it.
func TestAdoptRegistersTheCoroutineForTheTaskAndItsEndTakesItAway(t *testing.T) {
	task, other := &Task{}, &Task{}
	if got := task.Goroutines(); len(got) != 0 {
		t.Fatalf("a task that did not start runs on goroutines %v, want none", got)
	}
	own := goroutineid.Current()
	leave := task.Enter()
	if got := task.Goroutines(); !slices.Equal(got, []uint64{own}) {
		t.Fatalf("a started task runs on goroutines %v, want [%d]", got, own)
	}

	coroutine := make(chan uint64)
	found := make(chan *Task)
	release := make(chan struct{})
	go func() {
		coroutine <- goroutineid.Current()
		for range 3 {
			<-release
			found <- Current()
		}
	}()
	id := <-coroutine
	asks := func() *Task { release <- struct{}{}; return <-found }

	if asks() != nil {
		t.Fatal("a goroutine that was not adopted runs a task")
	}
	end := Adopt(id)
	if got, want := task.Goroutines(), []uint64{min(own, id), max(own, id)}; !slices.Equal(got, want) {
		t.Fatalf("an adopted task runs on goroutines %v, want %v", got, want)
	}
	if asks() != task {
		t.Fatal("the adopted coroutine does not run the task it was adopted for")
	}
	if got := other.Goroutines(); len(got) != 0 {
		t.Fatalf("a task that did not start runs on goroutines %v, want none", got)
	}
	end()
	if got := task.Goroutines(); !slices.Equal(got, []uint64{own}) {
		t.Fatalf("a task whose adoption ended runs on goroutines %v, want [%d]", got, own)
	}
	if asks() != nil {
		t.Fatal("the coroutine still runs the task once the adoption ended")
	}
	leave()
	if got := task.Goroutines(); len(got) != 0 {
		t.Fatalf("a task that ended runs on goroutines %v, want none", got)
	}
}

// Adopt called by a goroutine that runs no task adopts nothing.
func TestAdoptOutsideATaskAdoptsNothing(t *testing.T) {
	end := Adopt(goroutineid.Current() + 1)
	end()
	if Current() != nil {
		t.Fatal("a goroutine that runs no task found one")
	}
}
