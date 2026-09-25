package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
)

func TestDriveStripsCallerCancellationKeepsValues(t *testing.T) {
	key := harness.CreateContextKey[string]("drive.value")
	caller, cancel := harness.WithCancel(harness.WithContextValue(context.Background(), key, "kept"))
	drive := NewDrive(caller, agentharness.DriveOptions{OperationID: "op", WaitForRetry: true, PollDeferred: true})
	cancel(errors.New("caller gone"))
	if drive.Context.Err() != nil {
		t.Fatal("drive context inherited caller cancellation")
	}
	if value, _ := harness.ContextValue(drive.Context, key); value != "kept" {
		t.Fatalf("drive value = %q", value)
	}
	if drive.OperationID != "op" || !drive.WaitForRetry || drive.DeferredPermits != 1 {
		t.Fatalf("drive = %+v", drive)
	}
	if NewDrive(context.Background(), agentharness.DriveOptions{OperationID: "x"}).DeferredPermits != 0 {
		t.Fatal("deferred permit granted without PollDeferred")
	}
}

func TestDriveCompletionSettlesOnce(t *testing.T) {
	drive := NewDrive(context.Background(), agentharness.DriveOptions{OperationID: "op"})
	outcome := agentharness.DriveOutcome{Kind: agentharness.DriveWaiting, OperationID: "op", Reason: agentharness.DriveWaitRetry, NotBefore: 9}
	drive.Settle(outcome)
	drive.Fail(errors.New("late failure"))
	drive.CloseGate(errors.New("late close"))
	got, err := drive.Completion(context.Background())
	if err != nil || got != outcome {
		t.Fatalf("completion = %+v, %v", got, err)
	}
	// Close still closes the gate and the close signal after settlement.
	if drive.CloseSignal.Err() == nil {
		t.Fatal("close signal not cancelled")
	}
	if err := drive.Gate.Admit(func() error { return nil }); err == nil || err.Error() != "late close" {
		t.Fatalf("admission after close = %v", err)
	}
}

func TestDriveCloseRejectsCompletionAndSignals(t *testing.T) {
	drive := NewDrive(context.Background(), agentharness.DriveOptions{OperationID: "op"})
	closed := errors.New("harness closed")
	waiter := make(chan error, 1)
	go func() {
		_, err := drive.Completion(context.Background())
		waiter <- err
	}()
	drive.CloseGate(closed)
	drive.CloseGate(errors.New("second close"))
	if err := <-waiter; err != closed { //nolint:errorlint // upstream rejects with the close error itself.
		t.Fatalf("completion = %v, want %v", err, closed)
	}
	if cause := context.Cause(drive.CloseSignal); cause != closed { //nolint:errorlint // first close cause identity.
		t.Fatalf("close cause = %v", cause)
	}
	select {
	case <-drive.Gate.Signal().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("gate signal not aborted by close")
	}
}

func TestDriveAbortClosesAdmissionBeforeSignal(t *testing.T) {
	drive := NewDrive(context.Background(), agentharness.DriveOptions{OperationID: "op"})
	drive.BeginAbort(func() error { return nil })
	var refused *execution.AbortRequested
	if err := drive.Gate.Admit(func() error { return nil }); !errors.As(err, &refused) {
		t.Fatalf("admission after BeginAbort = %v", err)
	}
	if drive.Gate.Signal().Err() != nil {
		t.Fatal("gate signalled before SignalAbort")
	}
	drive.SignalAbort()
	if !errors.As(context.Cause(drive.Gate.Signal()), &refused) {
		t.Fatalf("gate cause = %v", context.Cause(drive.Gate.Signal()))
	}
}

func TestDriveCompletionWaitIsCancellable(t *testing.T) {
	drive := NewDrive(context.Background(), agentharness.DriveOptions{OperationID: "op"})
	waitCtx, cancel := harness.WithCancel(context.Background())
	reason := errors.New("observer left")
	cancel(reason)
	if _, err := drive.Completion(waitCtx); err != reason { //nolint:errorlint // waiter abort reason identity.
		t.Fatalf("cancelled wait = %v", err)
	}
	select {
	case <-drive.Done():
		t.Fatal("cancelling an observer settled the drive")
	default:
	}
}

func TestSliceNotImplementedMessage(t *testing.T) {
	err := &SliceNotImplemented{Operation: "watchSession"}
	if err.Error() != "watchSession is not implemented until its later AgentHarness slice" {
		t.Fatalf("message = %q", err.Error())
	}
}
