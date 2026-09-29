package codingagent

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestReloadStepPumpsUIAndJoinsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewInteractiveMode(InteractiveOptions{})
		ctx, cancel := context.WithCancelCause(t.Context())
		entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- m.awaitReloadStep(ctx, func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				<-release
				return ctx.Err()
			})
		}()
		<-entered
		applied := false
		if err := m.postToMain(t.Context(), func() { applied = true }); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !applied {
			t.Error("awaited reload blocked the UI owner")
		}
		cause := errors.New("reload owner stopped")
		cancel(cause)
		<-cancelled
		synctest.Wait()
		var result error
		returned := false
		select {
		case result = <-done:
			returned = true
			t.Errorf("reload returned before worker joined: %v", result)
		default:
		}
		close(release)
		if !returned {
			result = <-done
		}
		if !errors.Is(result, cause) {
			t.Fatalf("reload cancellation = %v, want %v", result, cause)
		}
		if m.modalInputCh != nil {
			t.Error("reload retained the modal input route")
		}
	})
}

func TestReloadStepDoesNotCancelSuccessfulOperationContext(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{})
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	var observed context.Context
	if err := m.awaitReloadStep(parent, func(ctx context.Context) error { observed = ctx; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := observed.Err(); err != nil {
		t.Fatalf("successful reload cancelled the loaded Host context: %v", err)
	}
	cancel()
	if !errors.Is(observed.Err(), context.Canceled) {
		t.Fatalf("operation context lost owner cancellation: %v", observed.Err())
	}
}

func TestReloadStepPreservesErrorsAndRejectsCancelledAdmission(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{})
	want := errors.New("extension callback failed")
	if err := m.awaitReloadStep(t.Context(), func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatalf("reload error = %v", err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(want)
	called := false
	err := m.awaitReloadStep(ctx, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, want) || called {
		t.Fatalf("cancelled admission = %v, called=%v", err, called)
	}
}
