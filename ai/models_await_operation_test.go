package ai

import (
	"context"
	"errors"
	"testing"
)

// raceWithAbortSignal (utils/abort.ts:18-21) rejects with the signal's reason at once when the signal is already aborted, and every operation models.ts hands
// it begins with signal.throwIfAborted() (:721, :730, :810), so nothing of the operation runs. Go does not start the operation at all: the collection admits no
// work for a caller that has already gone.
func TestAwaitModelsOperationWithAnAbortedSignalStartsNothingAndSettlesWithTheReason(t *testing.T) {
	reason := errors.New("operator interrupt")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(reason)
	spawned := 0
	_, err := awaitModelsOperation(ctx, func(run func()) bool { spawned++; run(); return true }, func() (string, error) { return "ran", nil })
	if !errors.Is(err, reason) || spawned != 0 {
		t.Fatalf("err = %v, operations started = %d; want the signal's reason and nothing started", err, spawned)
	}
}

// raceWithAbortSignal (utils/abort.ts:26-31): the abort listener settles the race when the signal fires, so an operation that aborts the signal and then
// resolves has already lost: the caller gets the reason, never the value. Here the result and the abort are both ready when the caller looks, and which one it
// takes first is the runtime's choice, so a value that got through is seen with certainty only over many rounds.
func TestAwaitModelsOperationResultThatArrivesWithTheAbortIsTheReason(t *testing.T) {
	reason := errors.New("operator interrupt")
	for round := range 200 {
		ctx, cancel := context.WithCancelCause(t.Context())
		value, err := awaitModelsOperation(ctx, func(run func()) bool { run(); return true }, func() (string, error) {
			cancel(reason)
			return "finished", nil
		})
		if !errors.Is(err, reason) || value != "" {
			t.Fatalf("round %d: (%q, %v), want the signal's reason and no value", round, value, err)
		}
	}
}
