package host

// Asserts the closed unions of packages/chord/src/types.ts (Pi 1.2.0). Ctx, Readable and RefBase are interfaces;
// their implementation is asserted by the tests of their implementations (CtxImpl, Cell/Derived, the object and remote
// references).

import "testing"

// types.ts:26-29: StateEvent is a snap, ops or reset event; only ops carries ops.
func TestStateEventTypes(t *testing.T) {
	if StateEventSnap != "snap" || StateEventOps != "ops" || StateEventReset != "reset" {
		t.Fatalf("types = %q %q %q", StateEventSnap, StateEventOps, StateEventReset)
	}
	snap := StateEvent[int]{Type: StateEventSnap, Value: 1}
	if snap.Ops != nil {
		t.Fatalf("snap carries ops: %+v", snap)
	}
}

// types.ts:53: RefStatus is "loading" | "live" | "released" | "gone".
func TestRefStatusValues(t *testing.T) {
	got := []RefStatus{RefLoading, RefLive, RefReleased, RefGone}
	want := []string{"loading", "live", "released", "gone"}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("status %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// types.ts:55-61: the optional members of GetOptions and Shape are undefined in the zero value (GetOptions has no host
// since aa9cf15b: the contract's placement says where an object lives).
func TestOptionalMembersAreUndefinedWhenZero(t *testing.T) {
	var options GetOptions
	if options.HasPrincipal || options.Principal != nil {
		t.Fatalf("zero GetOptions = %+v", options)
	}
	if (Shape{}).Variant != nil {
		t.Fatal("zero Shape has a kind")
	}
}
