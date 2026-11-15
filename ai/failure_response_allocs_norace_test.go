//go:build !race

package ai

import "testing"

// failedRequestAllocs is what one failed HTTP request allocates on car 21 (d87c7d13e3), measured with this test's transport and no decider. The
// failure response and its header map must not add to it. The race detector's instrumentation changes the count, so the test runs without it.
var failedRequestAllocs = map[string]float64{"openai-completions": 253, "openai-responses": 225, "anthropic": 257, "google": 219}

func TestFailedRequestAllocationsWithoutDeciderMatchCar21(t *testing.T) {
	for _, tc := range failedRequestProviders {
		t.Run(tc.name, func(t *testing.T) {
			provider := tc.build()
			transport := rateLimitedTransport()
			got := testing.AllocsPerRun(200, func() { runFailedRequest(t, provider, transport, false) })
			t.Logf("allocs/op %s = %v", tc.name, got)
			if want := failedRequestAllocs[tc.name]; got != want {
				t.Fatalf("allocs/op = %v, want %v (car 21)", got, want)
			}
		})
	}
}
