package extension

import (
	"slices"
	"strconv"
	"testing"
)

// Pi retains the tracked unsubscribes in a Set: insertion order, deleting one leaves the others in place, and every subscription is its own member even when the same handler subscribes twice (loader.ts:201-210).
func TestExtensionRuntimeInvalidateOrderAfterUnsubscribes(t *testing.T) {
	r := CreateExtensionRuntime()
	var calls []string
	track := func(name string) func() { return r.TrackEventBusSubscription(func() { calls = append(calls, name) }) }
	track("a")
	b := track("b")
	track("c")
	d := track("d")
	b()
	d()
	if want := []string{"b", "d"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	b()
	d()
	track("e") // subscribed after the removals: last
	r.Invalidate()
	if want := []string{"b", "d", "a", "c", "e"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v (retained ones in insertion order, removed ones not again)", calls, want)
	}
}

// The same handler subscribed twice is two members: each unsubscribe removes its own, and a resubscription goes to the end.
func TestExtensionRuntimeSameHandlerTrackedTwice(t *testing.T) {
	r := CreateExtensionRuntime()
	var calls []string
	shared := func() { calls = append(calls, "shared") }
	first := r.TrackEventBusSubscription(shared)
	r.TrackEventBusSubscription(shared)
	r.TrackEventBusSubscription(func() { calls = append(calls, "other") })
	first()
	first()
	if want := []string{"shared"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	r.TrackEventBusSubscription(shared) // resubscribe: new member at the end
	r.Invalidate()
	if want := []string{"shared", "shared", "other", "shared"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// An unsubscribe that runs while Invalidate walks the retained ones removes a later member before Invalidate reaches it, and runs once.
func TestExtensionRuntimeUnsubscribeDuringInvalidate(t *testing.T) {
	r := CreateExtensionRuntime()
	var calls []string
	var offC func()
	r.TrackEventBusSubscription(func() { calls = append(calls, "a"); offC() })
	r.TrackEventBusSubscription(func() { calls = append(calls, "b") })
	offC = r.TrackEventBusSubscription(func() { calls = append(calls, "c") })
	r.Invalidate()
	if want := []string{"a", "c", "b"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	offC()
	r.Invalidate()
	if len(calls) != 3 {
		t.Fatalf("calls = %v, want no further call", calls)
	}
}

func BenchmarkTrackedUnsubscribe(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				r := CreateExtensionRuntime()
				offs := make([]func(), n)
				for i := range offs {
					offs[i] = r.TrackEventBusSubscription(func() {})
				}
				b.StartTimer()
				for _, off := range offs {
					off()
				}
			}
		})
	}
}
