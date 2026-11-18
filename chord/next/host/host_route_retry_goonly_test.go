package host

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Go-only: Pi's `retry` (route.ts:158-164) clears the address's failure and calls #resolveIfHeld, which does nothing while the address's
// resolve runs (`routed.resolving`, :237), and the retry is gone: the rejection of that resolve (:265-273) fails the holders and leaves
// the address failed until the next retry or hold, whatever the rejection says. That is right for the application's own failure (a start
// that throws while the retry waits).
//
// It is not right for a rejection that only reports a failure the retry has just ended (the workers' `#start` and `resolve` throw
// `uncrossed(routed.failure.error)`, workers.ts:370, :486-487, and `retry` cleared that failure first, :391-396): Pi's resolve that
// rejects and the route's handling of the rejection are one chain of microtasks, so no retry can enter between them from a timer or I/O;
// a Go resolve returns to a section of its own, and the retry can enter before it. Such a rejection says so (`Superseded`), and the
// route then handles it as usual (its holders fail with it, it is reported) and resolves the address once more at once, as a retry
// after the rejection does. A rejection that does not say so, or says it is not superseded, takes the retry with it, as Pi's.

// supersedingRejection is the rejection of a resolver that reports a failure of its own record, which a call it answers can end: the
// workers' recorded failure. The route reports and fails its holders with `error`, and asks `superseded` in the section that handles
// the rejection.
type supersedingRejection struct {
	error
	superseded func() bool
}

func (r supersedingRejection) Unwrap() error { return r.error }

func (r supersedingRejection) Superseded() bool { return r.superseded() }

// retriedWhileResolving holds the first resolve of S1, calls Retry for the address meanwhile (it finds the resolve running and starts
// none), and lets the resolve reject with what reject returns; a later resolve makes the worker. It returns once the resolve was let go.
func retriedWhileResolving(t *testing.T, reject func() error) (*relayed, *Reference, *atomic.Int32) {
	t.Helper()
	pending := make(chan struct{})
	calls := new(atomic.Int32)
	r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, worker func() resolved, ctx context.Context) (resolved, error) {
		if calls.Add(1) == 1 {
			_, _ = chordctx.Receive(ctx, pending)
			return resolved{}, reject()
		}
		return worker(), nil
	}})
	notes, err := r.Tab.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
	must(t, err)
	r.Transported()
	wantRouteState(t, notes, RefStateLoading)
	if r.Route.Retry(Session.Address(jsonObject("id", "S1"))) {
		t.Fatal("retry resolved the address that is resolving")
	}
	close(pending)
	return r, notes, calls
}

// wantOneRejectionReported fails the test unless the server reported one resolve_failed, whose error is want itself: the route reports
// what the resolve rejected with, not what carries it.
func wantOneRejectionReported(t *testing.T, r *relayed, want error) {
	t.Helper()
	errs := r.Errors.Server.All()
	if len(errs) != 1 || errs[0].Kind != ErrResolveFailed || errs[0].Address != "session|S1" || errs[0].Error != want { //nolint:errorlint // identity: the rejection's own error, not one that wraps it
		t.Fatalf("host errors = %v, want one resolve_failed of session|S1 with the rejection's own error %v", errs, want)
	}
}

func TestRouteHandleRetryWhileTheResolveRunsThatThenRejectsASupersededFailureResolvesOnceMoreAfterTheRejection(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		inner := errors.New("no worker")
		var asked atomic.Int32
		r, notes, calls := retriedWhileResolving(t, func() error {
			return supersedingRejection{error: inner, superseded: func() bool { asked.Add(1); return true }}
		})
		r.Transported()
		wantRouteState(t, notes, RefStateLive)
		if got := calls.Load(); got != 2 {
			t.Fatalf("resolves = %d, want 2 (the rejected one, then the retry's)", got)
		}
		if got := asked.Load(); got != 1 {
			t.Fatalf("superseded was asked %d times, want once (by the section that handled the rejection)", got)
		}
		wantOneRejectionReported(t, r, inner)
	})
}

// A rejection that says its failure is still the one the retry meant to end is not superseded: the retry is gone with it, as Pi's.
func TestRouteHandleRetryWhileTheResolveRunsThatThenRejectsAFailureThatIsNotSupersededTakesTheRetryWithIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		inner := errors.New("no worker")
		var asked atomic.Int32
		r, notes, calls := retriedWhileResolving(t, func() error {
			return supersedingRejection{error: inner, superseded: func() bool { asked.Add(1); return false }}
		})
		wantRouteWaiting(t, notes, "session|S1: no worker")
		if got := calls.Load(); got != 1 {
			t.Fatalf("resolves = %d, want 1 (the failure the rejection reports was not ended: nothing resolves for the retry)", got)
		}
		if got := asked.Load(); got != 1 {
			t.Fatalf("superseded was asked %d times, want once", got)
		}
		wantOneRejectionReported(t, r, inner)
	})
}

// The application's own failure (a start that throws) is a plain error: it cannot be superseded, the retry that entered while it ran is
// gone with it (route.ts:237), and the address stays failed.
func TestRouteHandleRetryWhileTheResolveRunsThatThenRejectsWithAPlainErrorTakesTheRetryWithIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		inner := errors.New("no worker")
		r, notes, calls := retriedWhileResolving(t, func() error { return inner })
		wantRouteWaiting(t, notes, "session|S1: no worker")
		if got := calls.Load(); got != 1 {
			t.Fatalf("resolves = %d, want 1 (a start that failed on its own is not resolved again for a retry that entered while it ran)", got)
		}
		wantOneRejectionReported(t, r, inner)
	})
}

// A rejection that can be superseded is handled as the error it carries, whether or not a retry entered: its holders fail with that
// error itself (a ChordError is not wrapped into another), and the workers' loop is not entered to ask about a retry nobody made.
func TestARejectionThatCanBeSupersededIsHandledAsTheErrorItCarriesWhenNoRetryEntered(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		inner := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "session|S1: the worker crashed"}, nil)
		var asked atomic.Int32
		r := relay(t, relayedOptions{Resolve: func(_ contracts.Address, _ func() resolved, _ context.Context) (resolved, error) {
			return resolved{}, supersedingRejection{error: inner, superseded: func() bool { asked.Add(1); return true }}
		}})
		notes, err := r.Tab.Hold(background, PlacedNotes, routeS("S1"), GetOptions{})
		must(t, err)
		r.Transported()
		wantRouteWaiting(t, notes, "session|S1: the worker crashed")
		if got := routeState(notes).Cause.Error; got.Code != inner.Code || got.Message != inner.Message {
			t.Fatalf("the holders' failure = %v, want the rejection's own ChordError %v (not one wrapping it)", got, inner)
		}
		wantOneRejectionReported(t, r, inner)
		if got := asked.Load(); got != 0 {
			t.Fatalf("superseded was asked %d times, want none (no retry entered)", got)
		}
	})
}
