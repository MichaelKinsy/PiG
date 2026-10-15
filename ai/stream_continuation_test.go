package ai

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestStreamContinuationTransferReservesOrderBeforeWorkerAdmission(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	var order []string
	for observation := range stream.ObserveEvents(t.Context()) {
		order = append(order, "parent prefix")
		continuation := observation.PrepareContinuation()
		observation.turn.executor.post(func() { order = append(order, "queued after reservation") })
		admit := make(chan struct{})
		finished := make(chan error, 1)
		var workers sync.WaitGroup
		workers.Go(func() {
			<-admit
			finished <- continuation.Run(func(*StreamObservation) error {
				order = append(order, "transferred prefix")
				return nil
			})
		})
		if err := observation.Await(func() error {
			close(admit)
			return <-finished
		}); err != nil {
			t.Fatal(err)
		}
		workers.Wait()
		order = append(order, "parent resumed")
		break
	}
	want := []string{"parent prefix", "transferred prefix", "queued after reservation", "parent resumed"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("transfer order = %v, want %v", order, want)
	}
}

func TestStreamContinuationCanceledReservationReleasesQueue(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	for observation := range stream.ObserveEvents(t.Context()) {
		continuation := observation.PrepareContinuation()
		if !continuation.Cancel() {
			t.Fatal("unclaimed continuation could not be canceled")
		}
		err := observation.Await(func() error {
			return continuation.Run(func(*StreamObservation) error {
				t.Error("canceled continuation invoked its callback")
				return nil
			})
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled continuation = %v", err)
		}
		break
	}
}

func TestStreamContinuationCancelAfterGrantBeforeRunReleasesOwnership(t *testing.T) {
	executor := &continuationExecutor{}
	continuation := &StreamContinuation{turn: executor.newTurn()}
	if !continuation.Cancel() {
		t.Fatal("granted but unclaimed continuation could not be canceled")
	}
	if err := continuation.Run(func(*StreamObservation) error {
		t.Error("canceled continuation invoked its callback")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled continuation = %v", err)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.active != nil || executor.ready.len() != 0 {
		t.Fatal("canceled continuation retained execution ownership")
	}
}

func TestStreamContinuationCancelCannotAbandonRunningCallback(t *testing.T) {
	executor := &continuationExecutor{}
	continuation := &StreamContinuation{turn: executor.newTurn()}
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		finished <- continuation.Run(func(*StreamObservation) error {
			close(entered)
			<-release
			return nil
		})
	})
	<-entered
	if continuation.Cancel() {
		t.Error("cancellation abandoned a running callback")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	workers.Wait()
}

// upstream: packages/agent/src/agent-loop.ts:416-453 awaits the event sink; an awaiting continuation is a promise reaction, so it resumes before any I/O completion that became ready while the awaited listener ran (Node runs every microtask before the next macrotask). The transferred callback finishes and releases execution while the awaiting goroutine is still descheduled, and that goroutine must not lose its place to the I/O completion.
func TestStreamContinuationAwaitResumesBeforeIOCompletionQueuedByTransferredCallback(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	var order []string
	for observation := range stream.ObserveEvents(t.Context()) {
		continuation := observation.PrepareContinuation()
		executor := observation.turn.executor
		finished := make(chan error, 1)
		go func() {
			finished <- continuation.Run(func(*StreamObservation) error {
				order = append(order, "transferred prefix")
				// An I/O completion (a provider response) becomes ready while the listener runs.
				executor.postExternal(func() { order = append(order, "io completion") })
				return nil
			})
		}()
		// The waiting goroutine observes the callback only after it has returned and released execution, as it does when the scheduler runs it late.
		if err := observation.AwaitContinuation(continuation, func() error { return <-finished }); err != nil {
			t.Fatal(err)
		}
		order = append(order, "parent resumed")
		break
	}
	want := []string{"transferred prefix", "parent resumed"}
	if len(order) < 2 || !reflect.DeepEqual(order[:2], want) {
		t.Fatalf("order = %v, want the parent to resume before the I/O completion", order)
	}
}

// A host call reads the executor's observation to decide which turn to release. Another turn's callback replaces it while this turn waits, so a turn that resumes from a wait must find its own observation again; otherwise a result wait made after the resume neither releases its turn nor lets the stream advance.
func TestStreamContinuationResumeRestoresItsObservation(t *testing.T) {
	executor := &continuationExecutor{}
	first := &StreamContinuation{turn: executor.newTurn()}
	second := &StreamContinuation{turn: executor.newTurn()}
	wait := newContinuationPromise[struct{}](executor)
	var firstObservation *StreamObservation
	var resumedWith *StreamObservation
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.Run(func(observation *StreamObservation) error {
			firstObservation = observation
			awaitContinuation(observation.turn, wait)
			executor.mu.Lock()
			resumedWith = executor.observation
			executor.mu.Unlock()
			return nil
		})
	}()
	// The second callback starts only after the first has waited, replaces the executor's observation, and resolves the first's wait before it releases execution.
	secondErr := second.Run(func(observation *StreamObservation) error {
		executor.mu.Lock()
		current := executor.observation
		executor.mu.Unlock()
		if current != observation {
			t.Errorf("a running callback's observation is not the executor's")
		}
		wait.resolve(struct{}{})
		suspendContinuation(observation.turn)
		return nil
	})
	if err := <-firstDone; err != nil || secondErr != nil {
		t.Fatalf("first=%v second=%v", err, secondErr)
	}
	if resumedWith != firstObservation {
		t.Fatalf("resumed turn found observation %p, want its own %p", resumedWith, firstObservation)
	}
}

// A goroutine that runs one callback after another identifies itself once: goroutineID formats the goroutine's stack, which
// costs time proportional to its depth, and Run paid it for every callback. The owner decides which goroutine a Result call
// inside the callback may yield for, so RunOwned must record it exactly as Run does.
func TestRunOwnedIdentifiesTheGoroutineOnce(t *testing.T) {
	var calls atomic.Int64
	previous := currentGoroutineID
	currentGoroutineID = func() uint64 { calls.Add(1); return previous() }
	t.Cleanup(func() { currentGoroutineID = previous })
	_, executor := withContinuationExecutor(t.Context())
	const callbacks = 50
	done := make(chan struct{})
	go func() {
		defer close(done)
		owner := CurrentStreamOwner()
		for range callbacks {
			continuation := &StreamContinuation{turn: executor.newTurn()}
			err := continuation.RunOwned(owner, func(observation *StreamObservation) error {
				if !observation.ownedByCaller() {
					t.Error("the goroutine that runs the callback does not own its observation")
				}
				other := make(chan bool)
				go func() { other <- observation.ownedByCaller() }()
				if <-other {
					t.Error("another goroutine owns the observation")
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}
	}()
	<-done
	// One call identifies the owner; each ownedByCaller check asks for its own goroutine, two per callback.
	if got, want := calls.Load(), int64(1+2*callbacks); got != want {
		t.Fatalf("goroutineID ran %d times for %d callbacks, want %d", got, callbacks, want)
	}
}

// Run identifies its goroutine itself, as it always did.
func TestRunIdentifiesTheCallingGoroutine(t *testing.T) {
	_, executor := withContinuationExecutor(t.Context())
	continuation := &StreamContinuation{turn: executor.newTurn()}
	err := continuation.Run(func(observation *StreamObservation) error {
		if !observation.ownedByCaller() {
			t.Error("Run did not record the calling goroutine as the owner")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
