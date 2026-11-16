package chordctx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// A listener added to a context derived from a source of this package is run by the cancel of the source that owns the abort: a
// repeated call of the source's cancel function, made while the first is held after the source is cancelled, runs nothing and
// waits for the first. Before, the second call took the listeners from the first and ran them on its own goroutine. The gate
// is a context of another package under the source, so the derived context is also hooked to the source's Done channel
// (context.AfterFunc) and is cancelled there, not by a cancel function: what the owner holds is its listeners.
func TestRepeatedCancelLeavesTheListenersOfADerivedContextToTheCancelThatOwnsThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := newGatedParent(t)
		source, cancelSource := WithCancel(gate)
		other, cancelOther := WithCancel(Background())
		defer cancelOther(nil)
		derived := WithAbortSignal(other, source)
		var released atomic.Bool
		var mu sync.Mutex
		var ranBeforeRelease []bool
		OnAbort(derived, func(error) {
			mu.Lock()
			defer mu.Unlock()
			ranBeforeRelease = append(ranBeforeRelease, !released.Load())
		})
		first, second := errors.New("first cancel"), errors.New("second cancel")

		firstReturned := gate.hold(t, cancelSource, first)
		secondReturned := make(chan struct{})
		go func() {
			cancelSource(second)
			close(secondReturned)
		}()
		synctest.Wait()
		mu.Lock()
		ranBySecond := len(ranBeforeRelease)
		mu.Unlock()
		if ranBySecond != 0 {
			t.Errorf("a second cancel ran %d listeners of the context derived from the source", ranBySecond)
		}

		released.Store(true)
		close(gate.release)
		<-firstReturned
		<-secondReturned
		if derived.Err() == nil || context.Cause(derived) != first { //nolint:errorlint // the abort reason is passed by identity: a wrapping error would be a different reason
			t.Errorf("the derived context after both cancels returned: err %v, cause %v; want aborted with %v", derived.Err(), context.Cause(derived), first)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(ranBeforeRelease) != 1 || ranBeforeRelease[0] {
			t.Fatalf("the derived context's listener ran %d times, before the first cancel went on: %v; want once, in the first cancel", len(ranBeforeRelease), ranBeforeRelease)
		}
	})
}
