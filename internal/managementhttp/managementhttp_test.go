package managementhttp

// pi: packages/coding-agent/src/utils/management-http.ts

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripFunc stands in for upstream's vi.spyOn(globalThis, "fetch").
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type recorder struct {
	mu    sync.Mutex
	calls []*http.Request
}

func (r *recorder) client(respond func(call int, req *http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		r.mu.Lock()
		r.calls = append(r.calls, req)
		call := len(r.calls)
		r.mu.Unlock()
		return respond(call, req)
	})}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func newRequest(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestFetchWithRetryRetriesTransientTransportFailure(t *testing.T) {
	var rec recorder
	client := rec.client(func(call int, _ *http.Request) (*http.Response, error) {
		if call <= 2 {
			return nil, errors.New("fetch failed")
		}
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	resp, err := FetchWithRetry(client, newRequest(t, context.Background()), FetchRetryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || len(rec.calls) != 3 {
		t.Fatalf("status %d after %d calls, want 200 after 3", resp.StatusCode, len(rec.calls))
	}
}

func TestFetchWithRetrySharesTheTimeoutBudgetAcrossAttempts(t *testing.T) {
	var rec recorder
	client := rec.client(func(call int, _ *http.Request) (*http.Response, error) {
		if call == 1 {
			return nil, errors.New("fetch failed")
		}
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	resp, err := FetchWithRetry(client, newRequest(t, context.Background()), FetchRetryOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if len(rec.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(rec.calls))
	}
	first, ok1 := rec.calls[0].Context().Deadline()
	second, ok2 := rec.calls[1].Context().Deadline()
	if !ok1 || !ok2 || !first.Equal(second) {
		t.Fatalf("attempt deadlines = %v/%v, %v/%v; want one shared deadline", first, ok1, second, ok2)
	}
}

func TestFetchWithRetryRetriesAnAttemptTimeout(t *testing.T) {
	var rec recorder
	client := rec.client(func(call int, req *http.Request) (*http.Response, error) {
		if call == 1 {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	resp, err := FetchWithRetry(client, newRequest(t, context.Background()), FetchRetryOptions{AttemptTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if len(rec.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(rec.calls))
	}
	if rec.calls[0].Context() == rec.calls[1].Context() {
		t.Fatal("attempts shared one attempt context; each attempt needs its own timeout")
	}
}

func TestFetchWithRetryRetriesTransientHTTPResponses(t *testing.T) {
	var rec recorder
	client := rec.client(func(call int, _ *http.Request) (*http.Response, error) {
		if call == 1 {
			return response(http.StatusServiceUnavailable, "busy"), nil
		}
		return response(http.StatusOK, `{"ok":true}`), nil
	})
	resp, err := FetchWithRetry(client, newRequest(t, context.Background()), FetchRetryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || len(rec.calls) != 2 {
		t.Fatalf("status %d after %d calls, want 200 after 2", resp.StatusCode, len(rec.calls))
	}
}

func TestFetchWithRetryDoesNotRetryCallerCancellation(t *testing.T) {
	var rec recorder
	client := rec.client(func(int, *http.Request) (*http.Response, error) {
		return response(http.StatusOK, ""), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := FetchWithRetry(client, newRequest(t, ctx), FetchRetryOptions{})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(rec.calls))
	}
}

// Beyond upstream's cases: the retry budget and status gate are honored.
func TestFetchWithRetryStopsAfterMaxRetriesAndReturnsTheLastStatus(t *testing.T) {
	var rec recorder
	client := rec.client(func(int, *http.Request) (*http.Response, error) {
		return response(http.StatusBadGateway, "down"), nil
	})
	resp, err := FetchWithRetry(client, newRequest(t, context.Background()), FetchRetryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadGateway || len(rec.calls) != 3 {
		t.Fatalf("status %d after %d calls, want 502 after 3", resp.StatusCode, len(rec.calls))
	}

	rec = recorder{}
	noStatusRetry := false
	zero := 0
	client = rec.client(func(int, *http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, "busy"), nil
	})
	resp, err = FetchWithRetry(client, newRequest(t, context.Background()), FetchRetryOptions{RetryOnStatus: &noStatusRetry})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(rec.calls) != 1 {
		t.Fatalf("retryOnStatus=false made %d calls, want 1", len(rec.calls))
	}

	rec = recorder{}
	client = rec.client(func(int, *http.Request) (*http.Response, error) { return nil, errors.New("fetch failed") })
	failedResp, err := FetchWithRetry(client, newRequest(t, context.Background()), FetchRetryOptions{MaxRetries: &zero})
	if failedResp != nil {
		_ = failedResp.Body.Close()
	}
	if err == nil || len(rec.calls) != 1 {
		t.Fatalf("maxRetries=0: err=%v calls=%d, want an error after 1 call", err, len(rec.calls))
	}
}

// A connection that another request left in the client's pool must not carry an attempt either: net/http would resend the request once when that
// connection drops before the response. Pi's fetch (undici) sends one request and fails, so with MaxRetries 0 the server sees exactly one.
func TestFetchWithRetryDoesNotResendOnAPooledConnection(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warm" {
			w.WriteHeader(http.StatusOK)
			return
		}
		requests.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.(*net.TCPConn).SetLinger(0)
		_ = conn.Close()
	}))
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{}}
	warm, err := client.Get(server.URL + "/warm")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, warm.Body)
	_ = warm.Body.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/drop", nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	if response, err := FetchWithRetry(client, req, FetchRetryOptions{MaxRetries: &zero}); err == nil {
		_ = response.Body.Close()
		t.Fatalf("a dropped connection with MaxRetries 0 returned status %d, want the transport error", response.StatusCode)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("the server saw %d requests, want 1 (one attempt, no transparent resend)", got)
	}
}
