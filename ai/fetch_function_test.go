package ai

import (
	"errors"
	"net/http"
	"testing"
)

// types.ts ProviderRequestOptions.fetch: a FetchFunction is the transport itself. Used as an http.RoundTripper it receives the request it was given and returns
// exactly the response or the error the function produced.
func TestFetchFunctionRoundTripDelegatesToTheFunction(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.test/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{StatusCode: 207}
	var seen *http.Request
	var transport http.RoundTripper = FetchFunction(func(r *http.Request) (*http.Response, error) {
		seen = r
		return response, nil
	})
	got, err := transport.RoundTrip(request) //nolint:bodyclose // the response is the test's own and has no body
	if err != nil || got != response || seen != request {
		t.Fatalf("RoundTrip = %v, %v (request passed on: %v)", got, err, seen == request)
	}
	failure := errors.New("fetch failed")
	transport = FetchFunction(func(*http.Request) (*http.Response, error) { return nil, failure })
	if got, err := transport.RoundTrip(request); got != nil || !errors.Is(err, failure) { //nolint:bodyclose // the failing round trip returns no response
		t.Fatalf("RoundTrip failure = %v, %v", got, err)
	}
}
