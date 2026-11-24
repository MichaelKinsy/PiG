//go:build !race

package ai

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// failedRequestAllocs is what one failed HTTP request allocates with this test's transport and no decider: every allocation of the process while the request is made and
// its failed message read (testing.AllocsPerRun counts them all, the stream's goroutine included), as the floor of the mean over 200 requests. The failure response and its
// header map must not add to it: on the line before the seam (car 23 at 9a859103d, which holds rows 180 + 181) it was 252, 224, 257 and 219. The request encode then kept its splice
// and the marshalers of it between requests (spliceEncoding) and the counts fell by what a request paid for making them and for the list of encodings it made: about 40 and 38 on the two providers whose body is encoded
// with them (openai-completions, anthropic), 1 on google (the splice itself), none on openai-responses, which does not encode through a splice. Before that change the two
// counts held a number that was not constant: each request made a type cache whose hash trie has a seed of its own, so a request allocated 0 to 3 nodes more or fewer, and the mean
// of 200 requests sat near 257.9, which this test floored to 257 in 84 of 2000 processes to 258.
// The google count is the one of a timed failure: its request fails before a stream exists, so the module's call stamps the failed stream and its message (apiModuleFailure) and the message always carries durationMs, which costs the value and the copy an observation makes. When the stream was stamped
// after the request, a millisecond tick inside the request left the message untimed and 217 allocations, and the floor of the mean of 200 requests was 218 in most processes and 219 where the sync.Pool refills after a garbage collection outweighed the untimed requests.
// The race detector's instrumentation changes the count, so the test runs without it.
var failedRequestAllocs = map[string]float64{"openai-completions": 212, "openai-responses": 224, "anthropic": 219, "google": 219}

// failedRequestEnvLookups is the number of process environment lookups each provider's failed-request path makes
// (os.Getenv allocates 0 on Unix and 2 on Windows for UTF-16 conversion).
var failedRequestEnvLookups = map[string]float64{"openai-completions": 2, "openai-responses": 1, "anthropic": 2, "google": 0}

// failedRequestGoroutineIDLookups is the number of currentGoroutineID calls each provider's failed-request path makes
// (0 allocs on Linux/macOS amd64/arm64 via fast_gc.go, 1 alloc per call via runtime.Stack on Windows / slow_other.go).
// openai-completions and openai-responses enter responseTurn before inspecting resp.StatusCode, calling currentGoroutineID
// twice (native_body_iterator.go:138 and provider_streams.go:149 via ownedByCaller); anthropic and google reject 429 before responseTurn.
var failedRequestGoroutineIDLookups = map[string]float64{"openai-completions": 2, "openai-responses": 2, "anthropic": 0, "google": 0}

func TestFailedRequestAllocationsWithoutDeciderMatchTheLineBeforeTheSeam(t *testing.T) {
	// Finish goroutineid offset proof before measuring goidLookupAllocs so platforms with fast_gc.go measure 0
	// even when this test runs first in a fresh process, while slow_other.go (Windows) measures 1 per call.
	for goroutineid.Proving() {
		done := make(chan struct{})
		go func() {
			_ = currentGoroutineID()
			close(done)
		}()
		<-done
	}
	envLookupAllocs := testing.AllocsPerRun(100, func() { _ = os.Getenv("PI_CACHE_RETENTION") })
	goidLookupAllocs := testing.AllocsPerRun(100, func() { _ = currentGoroutineID() })
	for _, tc := range failedRequestProviders {
		t.Run(tc.name, func(t *testing.T) {
			provider := tc.build()
			transport := rateLimitedTransport()
			got := testing.AllocsPerRun(200, func() { runFailedRequest(t, provider, transport, false) })
			want := failedRequestAllocs[tc.name] + failedRequestEnvLookups[tc.name]*envLookupAllocs + failedRequestGoroutineIDLookups[tc.name]*goidLookupAllocs
			t.Logf("allocs/op %s = %v (want %v; envLookupAllocs=%v goidLookupAllocs=%v)", tc.name, got, want, envLookupAllocs, goidLookupAllocs)
			if got != want {
				t.Fatalf("allocs/op = %v, want %v (the line before the seam)", got, want)
			}
		})
	}
}
