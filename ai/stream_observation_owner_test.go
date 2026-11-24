package ai

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// StreamObservation owns the synchronous part of one continuation turn on the goroutine that runs its
// callback (stream_observation.go:13). ModelRuntime.Stream and StreamSimple attach the shared executor
// through ObservationContext and spawn a separate goroutine for request preparation (before_provider_headers)
// while the agent loop's turn is still active. A foreign goroutine that asks StreamObservationFromContext
// while another goroutine owns the active turn must get nil, never that turn's observation.
func TestStreamObservationFromContextReturnsNilToForeignGoroutine(t *testing.T) {
	ctx := WithStreamContinuations(t.Context())
	stream := NewAssistantMessageEventStream()

	err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		if got := StreamObservationFromContext(ctx); got != observation {
			t.Fatalf("owning goroutine got %p, want %p", got, observation)
		}
		scopedCtx := observation.Context(ctx)
		if got := StreamObservationFromContext(scopedCtx); got != observation {
			t.Fatalf("owning goroutine with scoped context got %p, want %p", got, observation)
		}
		// Simulate ModelRuntime.StreamSimple passing outer.ObservationContext(ctx) to go runtime.start(...).
		foreignCtx := stream.ObservationContext(ctx)
		foreignFromExecutor := make(chan *StreamObservation, 1)
		foreignFromScoped := make(chan *StreamObservation, 1)
		go func() {
			foreignFromExecutor <- StreamObservationFromContext(foreignCtx)
			foreignFromScoped <- StreamObservationFromContext(scopedCtx)
		}()
		wait := testbudget.Wait(t)
		select {
		case got := <-foreignFromExecutor:
			if got != nil {
				t.Errorf("foreign goroutine with executor context got owner's observation %p, want nil", got)
			}
		case <-time.After(wait):
			t.Fatal("timed out waiting for foreign goroutine executor lookup")
		}
		select {
		case got := <-foreignFromScoped:
			if got != nil {
				t.Errorf("foreign goroutine with scoped context got owner's observation %p, want nil", got)
			}
		case <-time.After(wait):
			t.Fatal("timed out waiting for foreign goroutine scoped lookup")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestForeignGoroutineAwaitExternalDoesNotRaceOrDeadlockOwner reproduces the exact interleaving seen
// between the agent loop (RunStreamContinuation -> Yield -> ObservePublishedEvents) and a spawned
// ModelRuntime.start goroutine (StreamObservationFromContext -> AwaitExternal):
//  1. The foreign goroutine calls StreamObservationFromContext while the owner still holds its turn.
//  2. The owner runs Yield and parks in ObservePublishedEvents waiting for the first stream event
//     (synchronized deterministically via executor.trace.pause — zero sleeps).
//  3. The foreign goroutine runs AwaitExternal if StreamObservationFromContext returned an observation,
//     then pushes Start and Done onto the stream.
//
// On the unfixed base:
//   - under -race: races on continuationTurn.deferred (settleDeferred vs awaitResolvedThen) and deadlocks
//     sending stepDetach on racePull's unbuffered events channel;
//   - in plain builds: panics with "iter.Pull: yield called again before next" or deadlocks in serve().
func TestForeignGoroutineAwaitExternalDoesNotRaceOrDeadlockOwner(t *testing.T) {
	wait := testbudget.Wait(t)
	ctx, executor := withContinuationExecutor(t.Context())
	stream := NewAssistantMessageEventStream()
	stream.executor = executor

	ownerParked := make(chan struct{})
	var parkedOnce sync.Once
	var ownerID uint64
	executor.trace = &executorTrace{
		pause: func() {
			if ownerID != 0 && currentGoroutineID() == ownerID {
				parkedOnce.Do(func() { close(ownerParked) })
			}
		},
	}

	foreignLookedUp := make(chan struct{})
	var lookupOnce sync.Once
	ownerDone := make(chan any, 1)
	foreignDone := make(chan any, 1)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ownerDone <- r
			}
		}()
		err := RunStreamContinuation(runCtx, func(observation *StreamObservation) error {
			ownerID = observation.Owner().id
			foreignCtx := stream.ObservationContext(runCtx)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						foreignDone <- r
						return
					}
					foreignDone <- nil
				}()
				foreignObs := StreamObservationFromContext(foreignCtx)
				lookupOnce.Do(func() { close(foreignLookedUp) })
				select {
				case <-ownerParked:
				case <-runCtx.Done():
					return
				}
				if foreignObs != nil {
					_ = foreignObs.AwaitExternal(func() error { return nil })
				}
				_ = stream.Push(StartEvent{Partial: testAssistant(StopReasonPending)})
				_ = stream.Push(DoneEvent{Reason: StopReasonStop, Message: testAssistant(StopReasonStop)})
				stream.End()
			}()
			select {
			case <-foreignLookedUp:
			case <-runCtx.Done():
				return runCtx.Err()
			}
			observation.Yield()
			for range stream.ObservePublishedEvents(observation.Context(runCtx)) {
			}
			return nil
		})
		ownerDone <- err
	}()

	select {
	case r := <-ownerDone:
		if r != nil {
			t.Fatalf("owner turn failed or panicked: %v", r)
		}
	case <-time.After(wait):
		cancelRun()
		t.Fatal("owner deadlocked waiting for stream while foreign goroutine held or panicked on owner's coroutine")
	}
	select {
	case r := <-foreignDone:
		if r != nil {
			t.Fatalf("foreign goroutine failed or panicked: %v", r)
		}
	case <-time.After(wait):
		cancelRun()
		t.Fatal("foreign goroutine deadlocked inside AwaitExternal on owner's coroutine")
	}
}

// TestForeignGoroutineDataRaceOnDeferred reproduces the exact WARNING: DATA RACE from the gate log
// (settleDeferred at ai/continuation.go:1038 vs awaitResolvedThen at ai/continuation.go:1051) by
// driving StreamObservationFromContext + AwaitExternal concurrently with Yield from two goroutines.
func TestForeignGoroutineDataRaceOnDeferred(t *testing.T) {
	wait := testbudget.Wait(t)
	ctx, executor := withContinuationExecutor(t.Context())
	stream := NewAssistantMessageEventStream()
	stream.executor = executor

	const rounds = 64
	for range rounds {
		ready := make(chan struct{})
		foreignDone := make(chan any, 1)
		ownerDone := make(chan any, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ownerDone <- r
				}
			}()
			err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
				foreignCtx := stream.ObservationContext(ctx)
				go func() {
					defer func() {
						if r := recover(); r != nil {
							foreignDone <- r
							return
						}
						foreignDone <- nil
					}()
					close(ready)
					if foreignObs := StreamObservationFromContext(foreignCtx); foreignObs != nil {
						_ = foreignObs.AwaitExternal(func() error { return nil })
					}
				}()
				<-ready
				observation.Yield()
				return nil
			})
			ownerDone <- err
		}()
		select {
		case r := <-ownerDone:
			if r != nil {
				t.Fatalf("owner failed or panicked: %v", r)
			}
		case <-time.After(wait):
			t.Fatal("owner deadlocked during concurrent foreign AwaitExternal and owner Yield")
		}
		select {
		case r := <-foreignDone:
			if r != nil {
				t.Fatalf("foreign goroutine failed or panicked: %v", r)
			}
		case <-time.After(wait):
			t.Fatal("foreign goroutine deadlocked during concurrent foreign AwaitExternal and owner Yield")
		}
	}
}

// TestStreamObservationFromContextReturnsNilToForeignGoroutineDuringProducerTurn verifies that
// while a provider response turn holds execution (turn.producer != nil), the producer's own
// goroutine receives the producer observation while any foreign goroutine sharing the same
// executor or scoped context receives nil.
func TestStreamObservationFromContextReturnsNilToForeignGoroutineDuringProducerTurn(t *testing.T) {
	wait := testbudget.Wait(t)
	ctx, executor := withContinuationExecutor(t.Context())
	stream := NewAssistantMessageEventStream()
	stream.executor = executor

	err := stream.responseContinuation(func(turn *continuationTurn) error {
		own := StreamObservationFromContext(ctx)
		if own == nil || own.turn != turn {
			t.Fatalf("producer goroutine got %v, want producer observation for turn %p", own, turn)
		}
		scopedCtx := own.Context(ctx)
		if got := StreamObservationFromContext(scopedCtx); got != own {
			t.Fatalf("producer goroutine with scoped context got %p, want %p", got, own)
		}
		foreignFromExecutor := make(chan *StreamObservation, 1)
		foreignFromScoped := make(chan *StreamObservation, 1)
		go func() {
			foreignFromExecutor <- StreamObservationFromContext(ctx)
			foreignFromScoped <- StreamObservationFromContext(scopedCtx)
		}()
		select {
		case got := <-foreignFromExecutor:
			if got != nil {
				t.Errorf("foreign goroutine with executor context during producer turn got %p, want nil", got)
			}
		case <-time.After(wait):
			t.Fatal("timed out waiting for foreign executor lookup during producer turn")
		}
		select {
		case got := <-foreignFromScoped:
			if got != nil {
				t.Errorf("foreign goroutine with scoped context during producer turn got %p, want nil", got)
			}
		case <-time.After(wait):
			t.Fatal("timed out waiting for foreign scoped lookup during producer turn")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
