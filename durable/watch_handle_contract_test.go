package durable_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Pi types.ts:868-881 WatchHandle and session/observation.ts:155 CommittedWatch: value is the acquisition revision before start and the
// latest delivered revision afterward; start installs the sole asynchronous listener and never invokes it inline; stop idempotently stops
// future callbacks and returns the watch's terminal result; closed settles with that result; a listener that fails ends only its own watch
// with reason listener_error carrying the error. The watch is held as a durable.WatchHandle, the interface a caller sees.
func TestWatchHandleContract(t *testing.T) {
	ctx := context.Background()
	token := durable.DefineDoc(durable.DocDefinition[durable.JsonObject]{
		CommonDocDefinition: durable.CommonDocDefinition[durable.JsonObject]{
			Kind: "watch.contract", Version: 1,
			Initial: func() durable.JsonObject { return delta.JsonObjectOf("value", 0) },
		},
		DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
	})
	open := func() sessiontest.Harness {
		harness := sessiontest.OpenTestSession()
		if _, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (struct{}, error) {
			_, err := tx.Doc(token)
			return struct{}{}, err
		}); err != nil {
			t.Fatal(err)
		}
		return harness
	}
	set := func(harness sessiontest.Harness, value int) {
		t.Helper()
		if _, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (struct{}, error) {
			draft, err := tx.Doc(token)
			if err != nil {
				return struct{}{}, err
			}
			return struct{}{}, draft.Set("value", value)
		}); err != nil {
			t.Fatal(err)
		}
	}
	watch := func(harness sessiontest.Harness) durable.WatchHandle[durable.JsonObject] {
		t.Helper()
		handle, err := harness.Session.WatchDocErased(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		return handle
	}
	waitClosed := func(handle durable.WatchHandle[durable.JsonObject]) durable.WatchEnd {
		t.Helper()
		select {
		case <-handle.Closed():
		case <-time.After(10 * time.Second):
			t.Fatal("the watch never closed")
		}
		return handle.End()
	}
	panics := func(text string, use func()) {
		t.Helper()
		defer func() {
			recovered := recover()
			message, _ := recovered.(string)
			if err, ok := recovered.(error); ok {
				message = err.Error()
			}
			if !strings.Contains(message, text) {
				t.Errorf("panic = %v, want one containing %q", recovered, text)
			}
		}()
		use()
	}

	t.Run("delivers committed frames asynchronously and keeps the acquisition value until then", func(t *testing.T) {
		harness := open()
		handle := watch(harness)
		if got := fmt.Sprint(handle.Value().Value("value")); got != "0" {
			t.Fatalf("acquisition value = %v, want 0", got)
		}
		var mu sync.Mutex
		var delivered []any
		inline := true
		handle.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
			mu.Lock()
			defer mu.Unlock()
			if inline {
				t.Error("the listener ran inline in Start")
			}
			delivered = append(delivered, fmt.Sprint(value.Value("value")))
			return nil
		})
		mu.Lock()
		inline = false
		mu.Unlock()
		set(harness, 1)
		harness.Session.WaitDeliveries()
		mu.Lock()
		got := append([]any(nil), delivered...)
		mu.Unlock()
		if len(got) != 1 || got[0] != "1" || fmt.Sprint(handle.Value().Value("value")) != "1" {
			t.Fatalf("delivered %v, value %v, want one delivery of 1 and the latest value 1", got, handle.Value().Value("value"))
		}
		first, _ := handle.Stop()
		second, _ := handle.Stop()
		if first != second || first.Reason != durable.WatchStopped {
			t.Fatalf("stop = %+v then %+v, want the same stopped result", first, second)
		}
		if end := waitClosed(handle); end.Reason != durable.WatchStopped {
			t.Fatalf("closed with %+v, want stopped", end)
		}
		set(harness, 2)
		harness.Session.WaitDeliveries()
		mu.Lock()
		after := len(delivered)
		mu.Unlock()
		if after != 1 {
			t.Fatalf("a stopped watch delivered %d frames, want 1", after)
		}
	})

	// observation.ts:174-212: a frame committed before start stays pending (advance does not touch value), and start only schedules its drain
	// (`if (this.#pending.length > 0) this.#schedule()`, a microtask), so the listener never runs inside start even when a frame is waiting.
	t.Run("keeps a frame committed before start pending and delivers it after start returns", func(t *testing.T) {
		harness := open()
		handle := watch(harness)
		set(harness, 1)
		harness.Session.WaitDeliveries()
		if got := fmt.Sprint(handle.Value().Value("value")); got != "0" {
			t.Fatalf("value before start = %v, want the acquisition revision 0", got)
		}
		var mu sync.Mutex
		var delivered []string
		inStart := true
		handle.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
			mu.Lock()
			defer mu.Unlock()
			if inStart {
				t.Error("the listener ran inline in Start with a pending frame")
			}
			delivered = append(delivered, fmt.Sprint(value.Value("value")))
			return nil
		})
		mu.Lock()
		inStart = false
		mu.Unlock()
		harness.Session.WaitDeliveries()
		mu.Lock()
		got := append([]string(nil), delivered...)
		mu.Unlock()
		if len(got) != 1 || got[0] != "1" || fmt.Sprint(handle.Value().Value("value")) != "1" {
			t.Fatalf("delivered %v, value %v, want the pending frame 1 delivered once after start", got, handle.Value().Value("value"))
		}
		_, _ = handle.Stop()
	})

	t.Run("accepts one start and none after stop", func(t *testing.T) {
		harness := open()
		listener := func(context.Context, durable.JsonObject, []durable.Op) error { return nil }
		started := watch(harness)
		started.Start(listener)
		panics("already started", func() { started.Start(listener) })
		_, _ = started.Stop()
		stopped := watch(harness)
		_, _ = stopped.Stop()
		panics("stopped", func() { stopped.Start(listener) })
	})

	t.Run("ends only the watch whose listener failed", func(t *testing.T) {
		harness := open()
		failed, healthy := watch(harness), watch(harness)
		failed.Start(func(context.Context, durable.JsonObject, []durable.Op) error { return errors.New("listener failed") })
		var mu sync.Mutex
		calls := 0
		healthy.Start(func(context.Context, durable.JsonObject, []durable.Op) error {
			mu.Lock()
			calls++
			mu.Unlock()
			return nil
		})
		set(harness, 1)
		end := waitClosed(failed)
		if end.Reason != durable.WatchListenerError || end.Error == nil || end.Error.Error() != "listener failed" {
			t.Fatalf("end = %+v, want listener_error carrying the listener's error", end)
		}
		harness.Session.WaitDeliveries()
		mu.Lock()
		got := calls
		mu.Unlock()
		if got != 1 {
			t.Fatalf("the healthy watch was delivered %d frames, want 1", got)
		}
		_, _ = healthy.Stop()
	})
}
