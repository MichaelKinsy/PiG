//go:build !race

package ai

import "testing"

// failedRequestAllocs is what one failed HTTP request allocates on the line before the seam (car 23 at 9a859103d, which holds rows 180 + 181: one allocation fewer than car 21 on the two OpenAI paths), measured with this test's transport and no decider. The
// failure response and its header map must not add to it. The race detector's instrumentation changes the count, so the test runs without it.
var failedRequestAllocs = map[string]float64{"openai-completions": 252, "openai-responses": 224, "anthropic": 257, "google": 219}

func TestFailedRequestAllocationsWithoutDeciderMatchTheLineBeforeTheSeam(t *testing.T) {
	for _, tc := range failedRequestProviders {
		t.Run(tc.name, func(t *testing.T) {
			provider := tc.build()
			transport := rateLimitedTransport()
			got := testing.AllocsPerRun(200, func() { runFailedRequest(t, provider, transport, false) })
			t.Logf("allocs/op %s = %v", tc.name, got)
			if want := failedRequestAllocs[tc.name]; got != want {
				t.Fatalf("allocs/op = %v, want %v (the line before the seam)", got, want)
			}
		})
	}
}
