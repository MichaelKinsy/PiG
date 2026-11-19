//go:build !race

package ai

import "testing"

// failedRequestAllocs is what one failed HTTP request allocates with this test's transport and no decider: every allocation of the process while the request is made and
// its failed message read (testing.AllocsPerRun counts them all, the stream's goroutine included), as the floor of the mean over 200 requests. The failure response and its
// header map must not add to it: on the line before the seam (car 23 at 9a859103d, which holds rows 180 + 181) it was 252, 224, 257 and 219. The request encode then kept its splice
// and the marshalers of it between requests (spliceEncoding) and the counts fell by what a request paid for making them and for the list of encodings it made: about 40 and 38 on the two providers whose body is encoded
// with them (openai-completions, anthropic), 1 on google (the splice itself), none on openai-responses, which does not encode through a splice. Before that change the two
// counts held a number that was not constant: each request made a type cache whose hash trie has a seed of its own, so a request allocated 0 to 3 nodes more or fewer, and the mean
// of 200 requests sat near 257.9, which this test floored to 257 in 84 of 2000 processes to 258.
// The race detector's instrumentation changes the count, so the test runs without it.
var failedRequestAllocs = map[string]float64{"openai-completions": 212, "openai-responses": 224, "anthropic": 219, "google": 218}

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
