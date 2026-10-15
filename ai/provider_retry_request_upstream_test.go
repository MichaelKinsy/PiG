package ai

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testProviderError is provider-retry.test.ts providerError: an Error with a status and headers.
type testProviderError struct {
	status  *int
	headers http.Header
}

func (e *testProviderError) Error() string {
	if e.status == nil {
		return "Provider error: undefined"
	}
	return "Provider error: " + strconv.Itoa(*e.status)
}
func (e *testProviderError) ProviderStatus() *int         { return e.status }
func (e *testProviderError) ProviderHeaders() http.Header { return e.headers }

func providerErrorFor(status int, headers ...string) *testProviderError {
	return &testProviderError{status: &status, headers: hdr(headers...)}
}

// scriptedRequest is vi.fn().mockRejectedValueOnce(...).mockResolvedValue("ok"): it fails with each given error in turn, then
// returns "ok"; with repeat it keeps failing with the last error.
type scriptedRequest struct {
	failures []error
	repeat   bool
	calls    int
}

func (s *scriptedRequest) call() (string, error) {
	index := s.calls
	s.calls++
	if index < len(s.failures) {
		return "", s.failures[index]
	}
	if s.repeat && len(s.failures) > 0 {
		return "", s.failures[len(s.failures)-1]
	}
	return "ok", nil
}

//go:fix inline
func intPointer(v int) *int { return new(v) }

// provider-retry.test.ts "retries retryable provider errors": the retry-after-ms delay is waited before the second attempt.
func TestRetryProviderRequestRetriesRetryableProviderErrors(t *testing.T) {
	request := &scriptedRequest{failures: []error{providerErrorFor(429, "retry-after-ms", "60")}}
	started := time.Now()
	result, err := RetryProviderRequest(t.Context(), request.call, ProviderRetryOptions{MaxRetries: new(1)})
	if err != nil || result != "ok" {
		t.Fatalf("result = %q, %v, want ok", result, err)
	}
	if request.calls != 2 {
		t.Errorf("calls = %d, want 2", request.calls)
	}
	if elapsed := time.Since(started); elapsed < 60*time.Millisecond {
		t.Errorf("the retry ran after %v, before the 60ms retry-after-ms delay", elapsed)
	}
}

// "does not retry errors the provider marks as non-retryable": x-should-retry: false wins over a retryable status.
func TestRetryProviderRequestHonorsXShouldRetryFalse(t *testing.T) {
	failure := providerErrorFor(429, "x-should-retry", "false")
	request := &scriptedRequest{failures: []error{failure}, repeat: true}
	_, err := RetryProviderRequest(t.Context(), request.call, ProviderRetryOptions{MaxRetries: new(2)})
	if err != error(failure) {
		t.Fatalf("err = %v, want the provider error itself", err)
	}
	if request.calls != 1 {
		t.Errorf("calls = %d, want 1", request.calls)
	}
}

// "does not retry statuses listed in noRetryStatuses": 504 is retryable by default, listed it fails at once.
func TestRetryProviderRequestNoRetryStatuses(t *testing.T) {
	failure := providerErrorFor(504, "retry-after-ms", "0")
	request := &scriptedRequest{failures: []error{failure}, repeat: true}
	_, err := RetryProviderRequest(t.Context(), request.call, ProviderRetryOptions{MaxRetries: new(2), NoRetryStatuses: []int{504}})
	if err != error(failure) {
		t.Fatalf("err = %v, want the provider error itself", err)
	}
	if request.calls != 1 {
		t.Errorf("calls = %d, want 1", request.calls)
	}
	// Without the list the same failure is retried to the budget.
	request = &scriptedRequest{failures: []error{failure}, repeat: true}
	if _, err := RetryProviderRequest(t.Context(), request.call, ProviderRetryOptions{MaxRetries: new(2)}); err != error(failure) || request.calls != 3 {
		t.Errorf("without NoRetryStatuses: err = %v, calls = %d, want the provider error after 3 calls", err, request.calls)
	}
}

// "rejects a provider-requested retry delay above the limit".
func TestRetryProviderRequestRejectsDelayAboveTheLimit(t *testing.T) {
	request := &scriptedRequest{failures: []error{providerErrorFor(429, "retry-after", "277403")}, repeat: true}
	_, err := RetryProviderRequest(t.Context(), request.call, ProviderRetryOptions{MaxRetries: new(1), MaxRetryDelayMs: new(1000)})
	if err == nil || !strings.Contains(err.Error(), "Server requested 277403s retry delay (max: 1s)") {
		t.Fatalf("err = %v, want the over-limit delay error", err)
	}
	if request.calls != 1 {
		t.Errorf("calls = %d, want 1", request.calls)
	}
}

// "allows disabling the provider-requested retry delay cap": maxRetryDelayMs 0 waits the server's delay; a 1ms cap rejects it.
func TestRetryProviderRequestZeroDisablesTheDelayCap(t *testing.T) {
	failure := providerErrorFor(429, "retry-after", "0.05")
	capped := &scriptedRequest{failures: []error{failure}}
	if _, err := RetryProviderRequest(t.Context(), capped.call, ProviderRetryOptions{MaxRetries: new(1), MaxRetryDelayMs: new(1)}); err == nil || !strings.Contains(err.Error(), "Server requested 1s retry delay (max: 1s)") {
		t.Fatalf("a 1ms cap: err = %v, want the over-limit delay error", err)
	}
	request := &scriptedRequest{failures: []error{failure}}
	started := time.Now()
	result, err := RetryProviderRequest(t.Context(), request.call, ProviderRetryOptions{MaxRetries: new(1), MaxRetryDelayMs: new(0)})
	if err != nil || result != "ok" || request.calls != 2 {
		t.Fatalf("result = %q, %v after %d calls, want ok after 2", result, err, request.calls)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Errorf("the retry ran after %v, before the 50ms retry-after delay", elapsed)
	}
}

// "aborts a provider-requested retry delay": the cancellation ends the backoff sleep with an AbortError ("Request aborted")
// while the 277403s delay is pending, and the request is not called again.
func TestRetryProviderRequestAbortsTheRetryDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	called := make(chan struct{})
	request := &scriptedRequest{failures: []error{providerErrorFor(429, "retry-after", "277403")}, repeat: true}
	done := make(chan error, 1)
	go func() {
		_, err := RetryProviderRequest(ctx, func() (string, error) {
			defer func() { close(called) }()
			return request.call()
		}, ProviderRetryOptions{MaxRetries: new(2), MaxRetryDelayMs: new(0)})
		done <- err
	}()
	<-called
	cancel()
	if err := <-done; err == nil || err.Error() != "Request aborted" {
		t.Fatalf("err = %v, want Request aborted", err)
	}
	if request.calls != 1 {
		t.Errorf("calls = %d, want 1", request.calls)
	}
}

// isProviderError: a failure without a status and headers (not a provider error) is returned at once, a provider error with
// no status (a connection failure) is retried.
func TestRetryProviderRequestClassifiesFailures(t *testing.T) {
	plain := errors.New("boom")
	request := &scriptedRequest{failures: []error{plain}, repeat: true}
	if _, err := RetryProviderRequest(t.Context(), request.call, ProviderRetryOptions{MaxRetries: new(2)}); err != plain || request.calls != 1 {
		t.Errorf("plain error: err = %v, calls = %d, want it returned after 1 call", err, request.calls)
	}
	connection := &scriptedRequest{failures: []error{&testProviderError{}}}
	providerRetryJitter = func() float64 { return 1 }
	t.Cleanup(func() { providerRetryJitter = defaultProviderRetryJitter })
	if result, err := RetryProviderRequest(t.Context(), connection.call, ProviderRetryOptions{MaxRetries: new(1)}); err != nil || result != "ok" || connection.calls != 2 {
		t.Errorf("no status: result = %q, %v after %d calls, want ok after 2", result, err, connection.calls)
	}
	// maxRetries defaults to 0.
	none := &scriptedRequest{failures: []error{providerErrorFor(500)}, repeat: true}
	if _, err := RetryProviderRequest(t.Context(), none.call, ProviderRetryOptions{}); err == nil || none.calls != 1 {
		t.Errorf("default budget: err = %v, calls = %d, want one call", err, none.calls)
	}
}
