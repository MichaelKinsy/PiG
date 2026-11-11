package parking

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

type carrier struct {
	context.Context
	signal *Signal
}

func (c carrier) Value(key any) any {
	if key == Key {
		return c.signal
	}
	return c.Context.Value(key)
}

func TestParkRaisesTheSignalAContextCarriesOnce(t *testing.T) {
	signal := New()
	ctx := carrier{Context: context.Background(), signal: signal}
	derived, cancel := context.WithCancel(ctx)
	defer cancel()
	Park(derived)
	select {
	case <-signal.done:
	default:
		t.Fatal("Park of a context derived from the carrier did not raise its signal")
	}
	Park(derived)
	signal.Wait()
}

func TestParkOfAContextWithoutASignalDoesNothing(t *testing.T) {
	Park(context.Background())
}

func TestRaiseGivesTheOwnerBackToTheSectionOnce(t *testing.T) {
	var owner atomic.Uint64
	signal := New()
	owner.Store(7)
	signal.Lend(&owner, 3)
	if owner.Load() != 7 {
		t.Fatal("Lend changed the owner: only Raise hands it back")
	}
	signal.Raise()
	if got := owner.Load(); got != 3 {
		t.Fatalf("owner after Raise = %d, want the section's 3", got)
	}
	// A later Raise (the handler returning after it parked) must not take the loop from whoever owns it now.
	owner.Store(9)
	signal.Raise()
	if got := owner.Load(); got != 9 {
		t.Fatalf("owner after a second Raise = %d, want 9: only the first counts", got)
	}
}

// A signal bound to a coroutine yields it at the first Raise on the coroutine's goroutine, and only there: a goroutine the
// handler started cannot give the section back, so its Raise is ignored and the handler's own Park raises.
func TestBoundSignalYieldsOnlyForARaiseOnItsGoroutine(t *testing.T) {
	var owner atomic.Uint64
	owner.Store(7)
	signal := New()
	signal.Lend(&owner, 3)
	yielded := 0
	signal.Bind(goroutineid.Current(), func() { yielded++ })
	done := make(chan struct{})
	go func() {
		defer close(done)
		signal.Raise()
	}()
	<-done
	if signal.Raised() || yielded != 0 || owner.Load() != 7 {
		t.Fatalf("a Raise on another goroutine counted: raised %v, yielded %d, owner %d", signal.Raised(), yielded, owner.Load())
	}
	signal.Raise()
	signal.Raise()
	if !signal.Raised() || yielded != 1 || owner.Load() != 3 {
		t.Fatalf("after the handler's own Raise: raised %v, yielded %d, owner %d; want true, 1, 3", signal.Raised(), yielded, owner.Load())
	}
	if signal.Returned() {
		t.Fatal("a Raise is not a Return")
	}
}

// Return is the Raise of a handler that finished; one after a park changes nothing.
func TestReturnTellsAHandlerThatFinishedFromOneThatParked(t *testing.T) {
	finished := New()
	finished.Return()
	if !finished.Raised() || !finished.Returned() {
		t.Fatalf("Return: raised %v, returned %v; want true, true", finished.Raised(), finished.Returned())
	}
	parked := New()
	parked.Raise()
	parked.Return()
	if parked.Returned() {
		t.Fatal("a Return after a park counted: the handler parked first")
	}
}
