package subprocess

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// pig additive (D214): the register frame carries the extension's ownership of thinking runs and a later ownThinkingRuns call
// changes it; the flag is per extension and leaves with the extension.
func TestRegisterPayloadCarriesOwnsThinkingRuns(t *testing.T) {
	var reg RegisterPayload
	if err := json.Unmarshal([]byte(`{"name":"x","owns_thinking_runs":true}`), &reg); err != nil || !reg.OwnsThinkingRuns {
		t.Fatalf("decoded %+v: %v", reg, err)
	}
	encoded, err := json.Marshal(RegisterPayload{Name: "x"})
	if err != nil || strings.Contains(string(encoded), "owns_thinking_runs") {
		t.Fatalf("an extension that does not own thinking runs sends %s: %v", encoded, err)
	}
}

func TestHostAppliesOwnsThinkingRuns(t *testing.T) {
	host := newWireHost(t)
	owner, plain := newWireExt(t), newWireExt(t)
	ownerExt, err := loadWireExt(t, host, owner, &RegisterPayload{Name: "owner", OwnsThinkingRuns: true})
	if err != nil {
		t.Fatal(err)
	}
	plainExt, err := loadWireExt(t, host, plain, &RegisterPayload{Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if !ownerExt.OwnsThinkingRuns() || plainExt.OwnsThinkingRuns() {
		t.Fatalf("after register: owner=%v plain=%v", ownerExt.OwnsThinkingRuns(), plainExt.OwnsThinkingRuns())
	}
	var changes atomic.Int32
	stop := ownerExt.ObserveThinkingRuns(func() { changes.Add(1) })
	defer stop()
	for _, enabled := range []bool{false, true, true, false} {
		if result := owner.call(CallOwnThinkingRuns, map[string]any{"enabled": enabled}, ""); result.Error != nil {
			t.Fatalf("ownThinkingRuns(%v): %+v", enabled, result.Error)
		}
		if got := ownerExt.OwnsThinkingRuns(); got != enabled {
			t.Fatalf("after ownThinkingRuns(%v) the extension owns %v", enabled, got)
		}
		if plainExt.OwnsThinkingRuns() {
			t.Fatal("another extension's call reached an extension that never declared ownership")
		}
	}
	if changes.Load() != 3 {
		t.Fatalf("observed %d changes, want 3 (the repeated call changes nothing)", changes.Load())
	}
	if result := plain.call(CallOwnThinkingRuns, map[string]any{}, ""); result.Error == nil {
		t.Fatal("a call without enabled was accepted")
	}
	if plainExt.OwnsThinkingRuns() {
		t.Fatal("a call without enabled changed the flag")
	}
}

// The flag drops with the extension: a stopped extension owns nothing.
func TestOwnsThinkingRunsDropsWhenTheExtensionStops(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "owner", OwnsThinkingRuns: true})
	if err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	stop := ext.ObserveThinkingRuns(func() { changes.Add(1) })
	defer stop()
	host.Shutdown("test done")
	if ext.OwnsThinkingRuns() || changes.Load() != 1 {
		t.Fatalf("after shutdown owns=%v changes=%d", ext.OwnsThinkingRuns(), changes.Load())
	}
}

// pig additive (D214): Shutdown returns only after the extension it stopped has withdrawn its ownership of thinking runs and told the
// observers. The extension's read loop reaches the drop on its own goroutine once it has applied the calls the extension made, so
// here it is held in a dialog call that has not returned: Shutdown must wait for the loop, and the drop must not run after it returned.
func TestShutdownWithdrawsOwnedThinkingRunsBeforeItReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newWireHost(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock() // a failed check must not leave the dialog call held
		host.SetCallHandler(func(_ string, call *CallPayload) (*CallResultPayload, error) {
			if call.Method == "ui.select" {
				close(entered)
				<-release
			}
			return &CallResultPayload{}, nil
		})
		w := newWireExt(t)
		ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "owner", OwnsThinkingRuns: true})
		if err != nil {
			t.Fatal(err)
		}
		var changes atomic.Int32
		stop := ext.ObserveThinkingRuns(func() { changes.Add(1) })
		defer stop()
		if err := w.send(&Envelope{Type: MsgCall, ID: "select", Call: &CallPayload{Method: "ui.select", Args: json.RawMessage(`{"title":"t","options":["keep"]}`)}}); err != nil {
			t.Fatal(err)
		}
		<-entered
		// The drain report waits for the dialog call, so the read loop is held inside its frames.
		if err := w.send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyRuntimeDrained}}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		done := make(chan struct{})
		go func() {
			host.Shutdown("test done")
			close(done)
		}()
		synctest.Wait()
		select {
		case <-done:
		default:
			unblock()
			synctest.Wait()
			<-done
		}
		if ext.OwnsThinkingRuns() || changes.Load() != 1 {
			t.Fatalf("after shutdown owns=%v changes=%d", ext.OwnsThinkingRuns(), changes.Load())
		}
	})
}
