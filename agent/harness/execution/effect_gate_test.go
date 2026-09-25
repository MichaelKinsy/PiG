package execution_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent/harness/execution"
)

// Mirrors execution-primitives.test.ts: admission closes before cancellation commits, but admitted work is signaled only by the owner after that commit.
func TestGateBeginAbortRefusesBeforeSignaling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate, control := execution.CreateGate()
		committed := make(chan struct{})
		waitCancellation := func() error { <-committed; return nil }
		control.BeginAbort(waitCancellation)

		invoked := false
		err := gate.Admit(func() error { invoked = true; return nil })
		var aborted *execution.AbortRequested
		if !errors.As(err, &aborted) || invoked {
			t.Fatalf("admission = %v, invoked = %v", err, invoked)
		}
		if err.Error() != "Abort requested" || gate.Signal().Err() != nil {
			t.Fatalf("premature signal or wrong refusal: %v, %v", gate.Signal().Err(), err)
		}

		var settled atomic.Bool
		go func() {
			if err := aborted.Cancellation(); err != nil {
				t.Errorf("cancellation: %v", err)
			}
			settled.Store(true)
		}()
		synctest.Wait()
		if settled.Load() {
			t.Fatal("refusal did not retain the owner's pending cancellation")
		}
		close(committed)
		synctest.Wait()
		if !settled.Load() || gate.Signal().Err() != nil {
			t.Fatal("settlement itself must not signal the gate")
		}
		control.SignalAbort()
		if gate.Signal().Err() != context.Canceled {
			t.Fatalf("signal = %v", gate.Signal().Err())
		}
		var reason *execution.AbortRequested
		if !errors.As(context.Cause(gate.Signal()), &reason) {
			t.Fatalf("signal cause = %v", context.Cause(gate.Signal()))
		}
		if err := reason.Cancellation(); err != nil {
			t.Fatalf("signal cancellation: %v", err)
		}
	})
}

func TestGateClosePermanentlyRefusesAndSignals(t *testing.T) {
	gate, control := execution.CreateGate()
	failure := errors.New("closed")
	control.Close(failure)
	control.Close(errors.New("later close"))
	control.BeginAbort(func() error { t.Fatal("closed gate replaced cancellation"); return nil })
	control.SignalAbort()
	requireSameError(t, gate.Admit(func() error { t.Fatal("closed gate invoked effect"); return nil }), failure)
	requireSameError(t, context.Cause(gate.Signal()), failure)
}

func TestGateAbortAndCloseKeepFirstSignalCause(t *testing.T) {
	for _, signalBeforeClose := range []bool{false, true} {
		t.Run(map[bool]string{false: "close-before-signal", true: "signal-before-close"}[signalBeforeClose], func(t *testing.T) {
			gate, control := execution.CreateGate()
			control.SignalAbort()
			if gate.Signal().Err() != nil {
				t.Fatal("signalAbort must do nothing while open")
			}
			first := errors.New("cancellation failed")
			control.BeginAbort(func() error { return first })
			control.BeginAbort(func() error { t.Fatal("second abort replaced first"); return nil })
			var refusal *execution.AbortRequested
			if !errors.As(gate.Admit(func() error { return nil }), &refusal) {
				t.Fatal("abort refusal lost original cancellation")
			}
			requireSameError(t, refusal.Cancellation(), first)
			if signalBeforeClose {
				control.SignalAbort()
				control.SignalAbort()
			}
			closed := errors.New("closed")
			control.Close(closed)
			control.SignalAbort()
			requireSameError(t, gate.Admit(func() error { return nil }), closed)
			cause := context.Cause(gate.Signal())
			if signalBeforeClose {
				var aborted *execution.AbortRequested
				if !errors.As(cause, &aborted) {
					t.Fatalf("close replaced first signal cause: %v", cause)
				}
				requireSameError(t, aborted.Cancellation(), first)
			} else {
				requireSameError(t, cause, closed)
			}
		})
	}
}

func TestGateAdmitReturnsCallbackResultAndAllowsReentrancy(t *testing.T) {
	gate, control := execution.CreateGate()
	failure := errors.New("effect failed")
	err := gate.Admit(func() error {
		requireSameError(t, gate.Admit(func() error { return failure }), failure)
		control.BeginAbort(func() error { return nil })
		control.SignalAbort()
		return failure
	})
	requireSameError(t, err, failure)
	if gate.Signal().Err() != context.Canceled {
		t.Fatal("admitted callback could not abort its own gate")
	}
}

func TestGateAdmittedWorkIsNotHeldUnderLifecycleLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate, control := execution.CreateGate()
		started, release, completed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			completed <- gate.Admit(func() error {
				close(started)
				<-release
				return context.Cause(gate.Signal())
			})
		}()
		<-started
		closed := errors.New("owner closed")
		control.Close(closed)
		close(release)
		requireSameError(t, <-completed, closed)
	})
}

func TestGateConcurrentAdmissionAndControl(t *testing.T) {
	gate, control := execution.CreateGate()
	closed := errors.New("closed")
	var work sync.WaitGroup
	for range 100 {
		work.Go(func() {
			err := gate.Admit(func() error { return nil })
			var aborted *execution.AbortRequested
			if err != nil && !errors.Is(err, closed) && !errors.As(err, &aborted) {
				t.Errorf("unexpected admission error: %v", err)
			}
		})
		work.Go(func() { control.BeginAbort(func() error { return nil }) })
		work.Go(control.SignalAbort)
		work.Go(func() { control.Close(closed) })
	}
	work.Wait()
	requireSameError(t, gate.Admit(func() error { t.Fatal("admitted after close"); return nil }), closed)
}

func requireSameError(t *testing.T, got, want error) {
	t.Helper()
	if got != want { //nolint:errorlint // Upstream preserves the exact error object; errors.Is would also accept a wrapper.
		t.Fatalf("error identity: got %v (%T), want %v (%T)", got, got, want, want)
	}
}
