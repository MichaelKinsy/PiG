package ai

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Ports packages/ai/test/google-shared-retry.test.ts.

// googleAPIErrorOf is shaped like @google/genai's ApiError: it has a status and no headers.
type googleAPIErrorOf struct{ status int }

func (e *googleAPIErrorOf) Error() string        { return fmt.Sprintf("got status: %d", e.status) }
func (e *googleAPIErrorOf) ProviderStatus() *int { return &e.status }

// "retries a headers-less SDK error with a retryable status": the first backoff is the 500ms of retryProviderRequest, here
// without jitter.
func TestRetryGoogleRequestRetriesAHeaderlessSDKErrorWithARetryableStatus(t *testing.T) {
	previous := providerRetryJitter
	providerRetryJitter = func() float64 { return 0 }
	t.Cleanup(func() { providerRetryJitter = previous })
	request := &scriptedRequest{failures: []error{&googleAPIErrorOf{429}}}
	started := time.Now()
	result, err := RetryGoogleRequest(t.Context(), request.call, StreamOptions{MaxRetries: new(1)})
	if err != nil || result != "ok" || request.calls != 2 {
		t.Fatalf("result = %q, err = %v, calls = %d", result, err, request.calls)
	}
	if elapsed := time.Since(started); elapsed < 500*time.Millisecond {
		t.Fatalf("the retry waited %v, want the 500ms backoff", elapsed)
	}
}

// "does not retry when maxRetries is unset": the caller gets the error it threw.
func TestRetryGoogleRequestDoesNotRetryWhenMaxRetriesIsUnset(t *testing.T) {
	failure := &googleAPIErrorOf{429}
	request := &scriptedRequest{failures: []error{failure}, repeat: true}
	_, err := RetryGoogleRequest(t.Context(), request.call)
	if !errors.Is(err, failure) || request.calls != 1 {
		t.Fatalf("err = %v (%T), calls = %d", err, err, request.calls)
	}
}

// "does not retry a non-retryable status".
func TestRetryGoogleRequestDoesNotRetryANonRetryableStatus(t *testing.T) {
	failure := &googleAPIErrorOf{400}
	request := &scriptedRequest{failures: []error{failure}, repeat: true}
	_, err := RetryGoogleRequest(t.Context(), request.call, StreamOptions{MaxRetries: new(2)})
	if !errors.Is(err, failure) || request.calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, request.calls)
	}
}

// google-shared.ts passes the stream options' maxRetryDelayMs on: a Retry-After above it fails at once for an error that carries headers.
func TestRetryGoogleRequestPassesTheDelayCapOn(t *testing.T) {
	request := &scriptedRequest{failures: []error{providerErrorFor(429, "retry-after", "30")}, repeat: true}
	_, err := RetryGoogleRequest(t.Context(), request.call, StreamOptions{MaxRetries: new(1), MaxRetryDelayMs: new(1000)})
	if err == nil || request.calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, request.calls)
	}
}
