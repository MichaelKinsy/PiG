package routing_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// Pi: packages/server/src/types.ts RoutedSessionHandle (attachClient acquires one attachment per call; terminated "resolves with an error for unexpected termination, or undefined after an expected close"; close ends the handle) as packages/server/src/testing/host.ts TestHarness implements it. The handle is used only through the routing.RoutedSessionHandle interface.
// Pi source: packages/server/src/session-router.ts:276-300 (open: the handle, terminated).
// mutation-checked: negating the condition `h.nextHarnessCloseError != nil` at host.go:448 fails it.
func TestRoutedSessionHandleContract(t *testing.T) {
	t.Parallel()
	ctx := waitContext(t)
	open := func(t *testing.T) (routing.RoutedSessionHandle, *routingtest.TestHarness) {
		t.Helper()
		host := routingtest.NewTestServerHost()
		metadata := host.Seed(nil)
		handle, err := host.OpenSession(ctx, metadata)
		if err != nil {
			t.Fatal(err)
		}
		return handle, latestHarness(t, host, metadata.SessionID())
	}

	t.Run("attachClient acquires one releasable attachment per call", func(t *testing.T) {
		t.Parallel()
		handle, harness := open(t)
		first, err := handle.AttachClient(ctx)
		if err != nil {
			t.Fatal(err)
		}
		second, err := handle.AttachClient(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if harness.AttachedClients() != 2 {
			t.Fatalf("attached clients = %d, want 2", harness.AttachedClients())
		}
		if err := first.Release(ctx); err != nil {
			t.Fatal(err)
		}
		if err := first.Release(ctx); err != nil { // a successful release is idempotent
			t.Fatal(err)
		}
		if harness.AttachedClients() != 1 {
			t.Fatalf("attached clients after releasing the first twice = %d, want 1", harness.AttachedClients())
		}
		if err := second.Release(ctx); err != nil {
			t.Fatal(err)
		}
		if harness.AttachedClients() != 0 {
			t.Fatalf("attached clients after both releases = %d, want 0", harness.AttachedClients())
		}
	})

	t.Run("an expected close terminates the handle without an error", func(t *testing.T) {
		t.Parallel()
		handle, harness := open(t)
		select {
		case <-handle.Terminated():
			t.Fatal("a live handle reported termination")
		default:
		}
		if err := handle.Close(ctx); err != nil {
			t.Fatal(err)
		}
		<-handle.Terminated()
		if err := handle.TerminalError(); err != nil {
			t.Fatalf("terminal error after an expected close = %v, want nil", err)
		}
		if harness.CloseCount() != 1 {
			t.Fatalf("close count = %d, want 1", harness.CloseCount())
		}
	})

	t.Run("an unexpected termination reports its error and survives a later close", func(t *testing.T) {
		t.Parallel()
		handle, _ := open(t)
		crash := errors.New("worker crashed")
		handle.(*routingtest.TestHarness).Terminate(crash)
		<-handle.Terminated()
		if err := handle.TerminalError(); !errors.Is(err, crash) {
			t.Fatalf("terminal error = %v, want %v", err, crash)
		}
		if err := handle.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if err := handle.TerminalError(); !errors.Is(err, crash) {
			t.Fatalf("terminal error after close = %v, want the first termination %v", err, crash)
		}
	})

	t.Run("a failing close reports its error", func(t *testing.T) {
		t.Parallel()
		host := routingtest.NewTestServerHost()
		metadata := host.Seed(nil)
		failure := errors.New("close failed")
		host.SetNextHarnessCloseError(failure)
		handle, err := host.OpenSession(ctx, metadata)
		if err != nil {
			t.Fatal(err)
		}
		if err := handle.Close(ctx); !errors.Is(err, failure) {
			t.Fatalf("close = %v, want %v", err, failure)
		}
	})
}
