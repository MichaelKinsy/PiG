package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

// packages/ai/src/utils/abort.ts: operationSignal(signal) is `signal ?? new AbortController().signal`.
func TestOperationSignalKeepsASignalAndSuppliesAnUncancelledOne(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	if got := OperationSignal(ctx); got != ctx {
		t.Errorf("a supplied signal is returned as it is")
	}
	cancel(errors.New("stop"))
	if got := OperationSignal(ctx); got.Err() == nil {
		t.Errorf("a cancelled signal stays cancelled")
	}
	var none context.Context
	fresh := OperationSignal(none)
	if fresh == nil || fresh.Err() != nil || fresh.Done() != nil {
		t.Errorf("a missing signal becomes one that is never cancelled, got %v", fresh)
	}
}

// packages/ai/src/utils/abort.ts: raceWithAbortSignal settles with the first of the operation and the abort, rejects with signal.reason, and observes the abandoned operation to settlement.
func TestRaceWithAbortSignalSettlesWithTheFirstOfOperationAndAbort(t *testing.T) {
	boom := errors.New("boom")
	var none context.Context
	reason := errors.New("because")

	t.Run("operation value wins", func(t *testing.T) {
		got, err := RaceWithAbortSignal(context.Background(), func() (int, error) { return 7, nil })
		if got != 7 || err != nil {
			t.Fatalf("got %d, %v", got, err)
		}
	})
	t.Run("operation error wins", func(t *testing.T) {
		_, err := RaceWithAbortSignal(context.Background(), func() (int, error) { return 0, boom })
		if !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("nil signal waits for the operation alone", func(t *testing.T) {
		got, err := RaceWithAbortSignal(none, func() (string, error) { return "ok", nil })
		if got != "ok" || err != nil {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("abort wins with the cause and the operation still settles", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		release := make(chan struct{})
		started := make(chan struct{})
		finished := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			_, err := RaceWithAbortSignal(ctx, func() (int, error) {
				defer close(finished)
				close(started)
				<-release
				return 1, boom
			})
			result <- err
		}()
		<-started
		cancel(reason)
		select {
		case err := <-result:
			if !errors.Is(err, reason) {
				t.Fatalf("want the cancel cause, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the race did not return on abort while the operation was still running")
		}
		select {
		case <-finished:
			t.Fatal("the operation must not be interrupted by the abort")
		default:
		}
		close(release)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("the abandoned operation never settled")
		}
	})
	t.Run("an already cancelled signal rejects with its cause even when the operation is instant, and the operation still runs", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(reason)
		for range 200 {
			ran := make(chan struct{})
			_, err := RaceWithAbortSignal(ctx, func() (int, error) { close(ran); return 1, nil })
			if !errors.Is(err, reason) {
				t.Fatalf("want the cancel cause, got %v", err)
			}
			select {
			case <-ran:
			case <-time.After(5 * time.Second):
				t.Fatal("the operation was not started")
			}
		}
	})
}

// abort.ts:raceWithAbortSignal checks signal.aborted before it waits and rejects with the abort reason at once, so an operation that
// settles is never chosen over an abort that came first. abortedSignal reports itself aborted but never closes Done, so only that
// up-front check can end the race with the abort.
func TestRaceWithAbortSignalPrefersAnEarlierAbortOverAReadyResult(t *testing.T) {
	signal := abortedSignal{Context: context.Background(), err: errors.New("aborted before the race")}
	got, err := RaceWithAbortSignal(signal, func() (int, error) { return 7, nil })
	if !errors.Is(err, signal.err) || got != 0 {
		t.Fatalf("pre-aborted race = %d, %v; want 0 and the abort reason", got, err)
	}
}

type abortedSignal struct {
	context.Context
	err error
}

func (s abortedSignal) Err() error { return s.err }
