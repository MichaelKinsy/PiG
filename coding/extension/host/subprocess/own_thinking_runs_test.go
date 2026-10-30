package subprocess

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
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
