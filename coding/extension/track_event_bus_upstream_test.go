package extension

import (
	"slices"
	"testing"
)

// upstream: loader.ts:201-210 trackEventBusSubscription returns an unsubscribe that runs the original once; loader.ts:198-199 invalidate runs every still-retained unsubscribe in subscription order, once. The Pi probe (imported_registry_session_test.go) gets calls=2 from off();off() plus one retained subscription.
func TestExtensionRuntimeTrackEventBusSubscriptionUpstream(t *testing.T) {
	r := CreateExtensionRuntime()
	var calls []string
	off := r.TrackEventBusSubscription(func() { calls = append(calls, "first") })
	off()
	off()
	if !slices.Equal(calls, []string{"first"}) {
		t.Fatalf("after off() twice calls = %v, want one", calls)
	}
	r.TrackEventBusSubscription(func() { calls = append(calls, "second") })
	r.TrackEventBusSubscription(func() { calls = append(calls, "third") })
	r.Invalidate("stale")
	r.Invalidate("second")
	if want := []string{"first", "second", "third"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v (invalidate runs retained ones once, in order)", calls, want)
	}
	if err := r.AssertActive(); err == nil || err.Error() != "stale" {
		t.Fatalf("AssertActive = %v, want stale", err)
	}
}

// A subscription that was already removed is not retained: invalidate does not run it again.
func TestExtensionRuntimeInvalidateSkipsUnsubscribedSubscription(t *testing.T) {
	r := CreateExtensionRuntime()
	n := 0
	r.TrackEventBusSubscription(func() { n++ })()
	r.Invalidate()
	if n != 1 {
		t.Fatalf("unsubscribe ran %d times, want 1", n)
	}
}
