package ai

import (
	"reflect"
	"sync"
	"testing"
)

// lazy.ts:48-50 starts forwarding in a .then reaction, not in the stack that resolves setup. Awaiting an already fulfilled value also queues a reaction.
func TestStreamContinuationFulfilledPromiseDoesNotRunInline(t *testing.T) {
	executor := &continuationExecutor{}
	var order []string
	executor.run(func(turn *continuationTurn) {
		promise := newContinuationPromise[string](executor)
		promise.resolve("setup")
		promise.onResolved(func(value string) { order = append(order, value) })
		order = append(order, "after registration")
		if want := []string{"after registration"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("reaction ran in the resolving stack: got %v, want %v", order, want)
		}
		if got := awaitContinuation(turn, promise); got != "setup" {
			t.Fatalf("await value = %q", got)
		}
		order = append(order, "await continuation")
	})
	want := []string{"after registration", "setup", "await continuation"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("reaction order = %v, want %v", order, want)
	}
}

// event-stream.ts:44-91 resolves the oldest waiter before the iterator's yield resumes its consumer. Reactions posted by reactions join the FIFO tail.
func TestStreamContinuationReactionsPreserveFIFO(t *testing.T) {
	executor := &continuationExecutor{}
	var order []string
	executor.run(func(*continuationTurn) {
		promise := newContinuationPromise[string](executor)
		promise.onResolved(func(value string) {
			order = append(order, "first "+value)
			executor.post(func() { order = append(order, "nested") })
		})
		promise.onResolved(func(value string) { order = append(order, "second "+value) })
		promise.resolve("event")
		promise.resolve("ignored")
		order = append(order, "producer segment")
	})
	want := []string{"producer segment", "first event", "second event", "nested"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("reaction order = %v, want %v", order, want)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if queueRetainsJobs(executor.ready) || executor.active != nil || executor.draining {
		t.Fatal("drained executor retained queue entries or execution ownership")
	}
}

func TestStreamContinuationAwaitResumesInItsReaction(t *testing.T) {
	executor := &continuationExecutor{}
	var order []string
	executor.run(func(turn *continuationTurn) {
		promise := newContinuationPromise[int](executor)
		promise.resolve(1)
		executor.post(func() {
			order = append(order, "earlier reaction")
			executor.post(func() { order = append(order, "later reaction") })
		})
		awaitContinuation(turn, promise)
		order = append(order, "await continuation")
	})
	want := []string{"earlier reaction", "await continuation", "later reaction"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("await added an extra reaction: got %v, want %v", order, want)
	}
}

func TestStreamContinuationPendingAwaitReleasesExecution(t *testing.T) {
	executor := &continuationExecutor{}
	pending := newContinuationPromise[int](executor)
	entered := make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() {
		<-entered
		executor.run(func(*continuationTurn) { pending.resolve(73) })
	})
	executor.run(func(turn *continuationTurn) {
		close(entered)
		if got := awaitContinuation(turn, pending); got != 73 {
			t.Errorf("await result = %d, want 73", got)
		}
	})
	workers.Wait()
}

// queueRetainsJobs reports a queued job or a reference a drained job left in the queue's storage.
func queueRetainsJobs(queue continuationQueue) bool {
	if queue.len() != 0 {
		return true
	}
	for _, job := range queue.items[:cap(queue.items)] {
		if job.fn != nil || job.grant != nil || job.promise != nil || job.reaction != nil {
			return true
		}
	}
	return false
}

// A queue that never empties keeps first-in, first-out order and reuses the storage its drained head frees, so its capacity stays bounded by the most jobs it held at once.
func TestContinuationQueueStaysFIFOAndBoundedWhileNeverEmpty(t *testing.T) {
	var queue continuationQueue
	var ran []int
	job := func(n int) continuationJob { return continuationJob{fn: func() { ran = append(ran, n) }} }
	next := 0
	for range 3 {
		queue.push(job(next))
		next++
	}
	for range 10000 {
		queue.push(job(next))
		next++
		queue.pop().run()
	}
	for queue.len() > 0 {
		queue.pop().run()
	}
	for i, n := range ran {
		if n != i {
			t.Fatalf("job %d ran in position %d", n, i)
		}
	}
	if len(ran) != next {
		t.Fatalf("ran %d of %d jobs", len(ran), next)
	}
	if cap(queue.items) > 8 {
		t.Fatalf("a queue that held at most 4 jobs grew to capacity %d", cap(queue.items))
	}
}
