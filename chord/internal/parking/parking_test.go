package parking

import (
	"context"
	"sync/atomic"
	"testing"
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
