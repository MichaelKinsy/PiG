package runtime

import (
	"context"
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// A request that waits at the gate returns once the Load finished; later ones do not wait.
func TestLoadGateOpensForWaitersAndLaterRequests(t *testing.T) {
	gate := NewLoadGate()
	done := make(chan error, 1)
	go func() { done <- gate.Wait(chordctx.Background()) }()
	<-gate.Blocked()
	gate.Finish(nil)
	if err := <-done; err != nil {
		t.Fatalf("waiter: %v", err)
	}
	if err := gate.Wait(chordctx.Background()); err != nil {
		t.Fatalf("later request: %v", err)
	}
}

// A cancelled request returns its cancellation cause to its caller; the gate stays closed for the others.
func TestLoadGateCancelReturnsTheCauseToThatWaiterOnly(t *testing.T) {
	gate := NewLoadGate()
	cause := errors.New("the caller went away")
	ctx, cancel := context.WithCancelCause(chordctx.Background())
	done := make(chan error, 1)
	go func() { done <- gate.Wait(ctx) }()
	<-gate.Blocked()
	cancel(cause)
	if err := <-done; !errors.Is(err, cause) {
		t.Fatalf("cancelled waiter = %v, want %v", err, cause)
	}
	other := make(chan error, 1)
	go func() { other <- gate.Wait(chordctx.Background()) }()
	select {
	case err := <-other:
		t.Fatalf("another waiter returned while the gate is closed: %v", err)
	default:
	}
	gate.Finish(nil)
	if err := <-other; err != nil {
		t.Fatalf("other waiter: %v", err)
	}
}

// A request cancelled before it waits fails at once, even while the gate is closed.
func TestLoadGateAlreadyCancelledFailsWithoutWaiting(t *testing.T) {
	gate := NewLoadGate()
	cause := errors.New("cancelled first")
	ctx, cancel := context.WithCancelCause(chordctx.Background())
	cancel(cause)
	if err := gate.Wait(ctx); !errors.Is(err, cause) {
		t.Fatalf("Wait = %v, want %v", err, cause)
	}
}

// A failed Load answers every request, the ones waiting and the later ones, with its error, and does not hang them.
func TestLoadGateFailedLoadAnswersEveryRequestWithItsError(t *testing.T) {
	gate := NewLoadGate()
	failure := errors.New("plugin x failed to load")
	done := make(chan error, 1)
	go func() { done <- gate.Wait(chordctx.Background()) }()
	<-gate.Blocked()
	gate.Finish(failure)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("waiter = %v, want %v", err, failure)
	}
	if err := gate.Wait(chordctx.Background()); !errors.Is(err, failure) {
		t.Fatalf("later request = %v, want %v", err, failure)
	}
	gate.Finish(nil)
	if err := gate.Wait(chordctx.Background()); !errors.Is(err, failure) {
		t.Fatalf("the first outcome stands, got %v", err)
	}
}

// A nil gate (a host without a worker entry) is open.
func TestLoadGateNilIsOpen(t *testing.T) {
	var gate *LoadGate
	if err := gate.Wait(chordctx.Background()); err != nil {
		t.Fatal(err)
	}
	gate.Finish(errors.New("ignored"))
	if gate.Blocked() != nil {
		t.Fatal("a nil gate never blocks")
	}
}
