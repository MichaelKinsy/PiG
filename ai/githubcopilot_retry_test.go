package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type copilotCancelErrorBody struct{ err error }

func (b copilotCancelErrorBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b copilotCancelErrorBody) Close() error             { return b.err }

func TestCopilotRetryPropagatesBodyCancellationFailure(t *testing.T) {
	cancelErr := errors.New("cancel body failed")
	calls := 0
	withMockCopilotClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		response := copilotJSONResp(http.StatusTooManyRequests, "")
		response.Header.Set("Retry-After", "0")
		response.Body = copilotCancelErrorBody{err: cancelErr}
		return response, nil
	})
	_, err := copilotFetchWithRetry(t.Context(), copilotRetryPolicy{MaxRetries: 1, MaxElapsedMs: 500}, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://copilot.invalid/", nil)
	})
	if !errors.Is(err, cancelErr) || calls != 1 {
		t.Fatalf("cancel failure=%v calls=%d; want original error and one attempt", err, calls)
	}
}

func TestCopilotPolicyBatchStopsOnHugeFiniteRetryAfter(t *testing.T) {
	calls := 0
	withMockCopilotClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		response := copilotJSONResp(http.StatusTooManyRequests, "rate limited")
		response.Header.Set("Retry-After", "1e300")
		return response, nil
	})
	enabled, err := enableGitHubCopilotModels(t.Context(), "test-token", []string{"first", "second"}, "")
	if err != nil || len(enabled) != 0 || calls != 1 {
		t.Fatalf("enabled=%v err=%v calls=%d; want empty result and one attempt", enabled, err, calls)
	}
}

func TestCopilotRetryParseFloatPrefix(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  float64
	}{
		{" \t+.5seconds", .5},
		{"\ufeff1_000", 1},
		{"\u00851", math.NaN()},
		{"1e+", 1},
		{".5.6", .5},
		{"0x1p2", 0},
		{"Infinitysuffix", math.Inf(1)},
		{"-Infinity", math.Inf(-1)},
		{"1e999", math.Inf(1)},
		{"NaN", math.NaN()},
		{"+.", math.NaN()},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got := copilotParseFloat(tc.input)
			if got != tc.want && !(math.IsNaN(got) && math.IsNaN(tc.want)) {
				t.Fatalf("parseFloat(%q)=%v want=%v", tc.input, got, tc.want)
			}
		})
	}
	delay, ok := copilotRetryDelay("1e300", 0)
	if !ok || !(delay > float64(math.MaxInt64)) || math.IsInf(delay, 0) {
		t.Fatalf("huge finite delay lost before budget comparison: %v, %v", delay, ok)
	}
}

// github-copilot.ts combines the caller signal with one retry-budget signal;
// a new attempt does not get a fresh whole-operation budget.
func TestCopilotRetryBudgetCancelsInFlightRequest(t *testing.T) {
	for _, initial429 := range []bool{false, true} {
		t.Run(fmt.Sprint(initial429), func(t *testing.T) {
			var calls atomic.Int32
			cancelled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := calls.Add(1)
				if initial429 && attempt == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				<-r.Context().Done()
				close(cancelled)
			}))
			defer server.Close()
			parent, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			_, err := copilotFetchWithRetry(parent, copilotRetryPolicy{MaxRetries: 1, MaxElapsedMs: 500}, func(ctx context.Context) (*http.Request, error) {
				return http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			})
			if !errors.Is(err, context.DeadlineExceeded) || parent.Err() != nil {
				t.Fatalf("request error=%v parent=%v; retry budget must cancel before parent", err, parent.Err())
			}
			wantCalls := int32(1)
			if initial429 {
				wantCalls++
			}
			if calls.Load() != wantCalls {
				t.Fatalf("calls=%d want=%d", calls.Load(), wantCalls)
			}
			select {
			case <-cancelled:
			case <-parent.Done():
				t.Fatal("server request was not cancelled")
			}
		})
	}
}

// Upstream cancels retryable response bodies without consuming them. Waiting
// for an unending rate-limit body must not prevent the next request.
func TestCopilotRetryCancelsRateLimitBodyBeforeRetry(t *testing.T) {
	var calls atomic.Int32
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(cancelled)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result, err := copilotFetchWithRetry(ctx, copilotRetryPolicy{MaxRetries: 1, MaxElapsedMs: 1000}, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	})
	if err != nil || result.StatusCode != http.StatusOK || string(result.Body) != "ok" || calls.Load() != 2 {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, calls.Load())
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("discarded body remains owned by the server")
	}
}

func TestCopilotRetryAfterMatchesJavaScriptNumericPrefixesAndBounds(t *testing.T) {
	for _, tc := range []struct {
		header string
		retry  bool
	}{
		{"0seconds", true},
		{"  +.001seconds", true},
		{"0x10", true},
		{"1_0", false},
		{"1e", false},
		{"1e300", false},
		{"-1e300", true},
		{"1e309", false},
		{"Infinity", false},
		{"not-a-date", false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", tc.header)
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte("rate limited"))
					return
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer server.Close()
			result, err := copilotFetchWithRetry(t.Context(), copilotRetryPolicy{MaxRetries: 1, MaxElapsedMs: 500}, func(ctx context.Context) (*http.Request, error) {
				return http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			})
			wantStatus, wantCalls, wantBody := http.StatusTooManyRequests, int32(1), "rate limited"
			if tc.retry {
				wantStatus, wantCalls, wantBody = http.StatusOK, 2, "ok"
			}
			if err != nil || result.StatusCode != wantStatus || calls.Load() != wantCalls || string(result.Body) != wantBody {
				t.Fatalf("status=%d calls=%d body=%q error=%v; want status=%d calls=%d body=%q", result.StatusCode, calls.Load(), result.Body, err, wantStatus, wantCalls, wantBody)
			}
		})
	}
}
