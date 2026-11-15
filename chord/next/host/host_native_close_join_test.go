package host

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Own test: the close of a native runs as an owned task of the host's loop (Pi's `await close()`: its continuation is a
// microtask once the closer settled), so a test's `delivered()` (deliveredOn) waits for a closer that runs, and does not wait for
// one parked on a gate through chordctx.Receive (`await gate.promise` still pending).
func TestNativeCloseIsAnOwnedTaskTheJoinWaitsForWhileItRunsAndNotWhileItIsParked(t *testing.T) {
	served := func(t *testing.T, closer func(ctx context.Context)) (Host, *Reference) {
		t.Helper()
		host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
			return h.Serve(objSvc, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.Open(func(context.Context) (any, error) { return nil, nil },
					func(_ any, _ NativeCloseReason, ctx context.Context) error { closer(ctx); return nil })
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)}}}, nil
			}, ServeOptions{})
		}, loadedOptions{})
		return host, mustGet(t, host, objSvc, jsonObject("id", 1.0))
	}

	t.Run("a closer that runs is waited for", func(t *testing.T) {
		var closed atomic.Bool
		host, ref := served(t, func(context.Context) {
			// Work of its own that takes a while (a flush, a socket): not a wait for the test.
			time.Sleep(50 * time.Millisecond)
			closed.Store(true)
		})
		ref.Release()
		deliveredOn(host)
		if !closed.Load() {
			t.Fatal("delivered() returned before the native's close ran to its end")
		}
	})

	t.Run("a closer parked through chordctx.Receive is not", func(t *testing.T) {
		gate := make(chan struct{})
		var closed atomic.Bool
		host, ref := served(t, func(ctx context.Context) {
			_, _ = chordctx.Receive(context.WithoutCancel(ctx), gate)
			closed.Store(true)
		})
		ref.Release()
		deliveredOn(host)
		if closed.Load() {
			t.Fatal("the native's close ended with its gate shut")
		}
		close(gate)
		deliveredOn(host)
		if !closed.Load() {
			t.Fatal("delivered() returned before the closer, whose gate was released, ended")
		}
	})
}
