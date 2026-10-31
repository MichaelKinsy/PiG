//go:build !race

package extension

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Unsubscribing one tracked event-bus subscription is O(1), as deleting a closure from Pi's Set is (upstream: loader.ts:201-210 trackEventBusSubscription). The cost is a count of list nodes moved, not an allocation, so the gate measures wall time: the best of several runs at 10000 subscriptions stays within 5 ms, and a tenfold size step stays far from the 100x of a quadratic path. The race detector inflates the times, so the measurement runs without it.
func TestScalingTrackedUnsubscribeIsConstantTime(t *testing.T) {
	best := func(n int) time.Duration {
		fastest := time.Duration(1 << 62)
		for range 7 {
			r := CreateExtensionRuntime()
			offs := make([]func(), n)
			for i := range offs {
				offs[i] = r.TrackEventBusSubscription(func() {})
			}
			start := time.Now()
			for _, off := range offs {
				off()
			}
			fastest = min(fastest, time.Since(start))
		}
		return fastest
	}
	small, large := best(1000), best(10000)
	t.Logf("n=1000 %v; n=10000 %v", small, large)
	testenv.TimingGate(t, large > 5*time.Millisecond, "10000 single unsubscribes took %v, want <= 5ms", large)
	ratio := float64(large) / float64(max(small, 10*time.Microsecond))
	testenv.TimingGate(t, ratio > 40, "a tenfold size step cost %.0fx, want a linear total (about 10x)", ratio)
}
