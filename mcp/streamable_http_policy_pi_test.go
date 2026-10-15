package mcp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Pi packages/mcp/src/transports/streamable-http.ts: isTransientStatus (:167-169), describeHttpFailure (:175-178), isRetryable (:466-471), reconnectDelay (:473-477).
// mutation-checked: `>=` for `>` at the 500-character body limit, a transient-status or retry-classification flip, and a reconnect delay that stops doubling or ignores the cap fail it.
func TestStreamableHTTPFailureDescriptionAndRetryPolicyMatchPi(t *testing.T) {
	t.Run("transient statuses are 408, 429 and 5xx", func(t *testing.T) {
		for status, want := range map[int]bool{200: false, 400: false, 401: false, 404: false, 407: false, 408: true, 409: false, 428: false, 429: true, 499: false, 500: true, 503: true, 599: true} {
			if got := isTransientStatus(status); got != want {
				t.Errorf("isTransientStatus(%d) = %v, want %v", status, got, want)
			}
		}
	})
	t.Run("describeHttpFailure trims the body and cuts it above 500 characters", func(t *testing.T) {
		exact := strings.Repeat("a", 500)
		over := strings.Repeat("b", 501)
		for _, tc := range []struct{ name, body, want string }{
			{"empty body", "", "MCP HTTP request failed with status 502"},
			{"whitespace only", " \n\t ", "MCP HTTP request failed with status 502"},
			{"short body is trimmed", "  boom \n", "MCP HTTP request failed with status 502: boom"},
			{"exactly 500 characters is kept whole", exact, "MCP HTTP request failed with status 502: " + exact},
			{"501 characters keep 497 and an ellipsis", over, "MCP HTTP request failed with status 502: " + strings.Repeat("b", 497) + "..."},
		} {
			if got := describeHTTPFailure(502, tc.body); got != tc.want {
				t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
			}
		}
	})
	t.Run("checkResponse accepts exactly the 2xx statuses", func(t *testing.T) {
		transport, err := NewStreamableHTTPTransport(StreamableHTTPTransportOptions{URL: "https://mcp.example/rpc"})
		if err != nil {
			t.Fatal(err)
		}
		for status, ok := range map[int]bool{199: false, 200: true, 204: true, 299: true, 300: false, 404: false, 500: false} {
			response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("body")), Header: http.Header{}}
			if err := transport.checkResponse(response); (err == nil) != ok {
				t.Errorf("checkResponse(%d) error = %v, want ok=%v", status, err, ok)
			}
		}
	})
	t.Run("only network failures and transient statuses are retried", func(t *testing.T) {
		transport, err := NewStreamableHTTPTransport(StreamableHTTPTransportOptions{URL: "https://mcp.example/rpc"})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name string
			err  error
			want bool
		}{
			{"transient http status", NewMcpHttpError(503, "x", ""), true},
			{"permanent http status", NewMcpHttpError(401, "x", ""), false},
			{"network error", &net.OpError{Op: "read", Err: errors.New("reset")}, true},
			{"url error", &url.Error{Op: "Get", URL: "https://x", Err: errors.New("refused")}, true},
			{"unexpected EOF", io.ErrUnexpectedEOF, true},
			{"EOF", io.EOF, true},
			{"cancelled", context.Canceled, false},
			{"protocol error", &McpError{Code: -32600, Message: "bad"}, false},
			{"other error", errors.New("something else"), false},
		} {
			if got := transport.isRetryable(tc.err); got != tc.want {
				t.Errorf("%s: isRetryable = %v, want %v", tc.name, got, tc.want)
			}
		}
	})
	t.Run("the reconnect delay doubles from the initial delay up to the maximum, and a server retry wins", func(t *testing.T) {
		transport, err := NewStreamableHTTPTransport(StreamableHTTPTransportOptions{URL: "https://mcp.example/rpc", Reconnect: StreamableHTTPReconnectOptions{InitialDelayMs: 100, MaxDelayMs: 700}})
		if err != nil {
			t.Fatal(err)
		}
		for attempt, want := range []time.Duration{100, 200, 400, 700, 700, 700} {
			if got := transport.reconnectDelay(attempt, &streamCursor{}); got != want*time.Millisecond {
				t.Errorf("attempt %d: delay %v, want %v", attempt, got, want*time.Millisecond)
			}
		}
		if got := transport.reconnectDelay(3, &streamCursor{hasRetry: true, retryMs: 1234}); got != 1234*time.Millisecond {
			t.Errorf("a server retry hint gave %v, want 1.234s", got)
		}
		defaults, err := NewStreamableHTTPTransport(StreamableHTTPTransportOptions{URL: "https://mcp.example/rpc"})
		if err != nil {
			t.Fatal(err)
		}
		for attempt, want := range []time.Duration{1000, 2000, 4000, 8000, 16000, 30000, 30000} {
			if got := defaults.reconnectDelay(attempt, &streamCursor{}); got != want*time.Millisecond {
				t.Errorf("default attempt %d: delay %v, want %v", attempt, got, want*time.Millisecond)
			}
		}
	})
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }

// streamable-http.ts:171-173 discard cancels the response body (when there is one) and tolerates a missing response or body.
// mutation-checked: a discard that skips the body close fails it.
func TestDiscardResponseClosesTheBodyAndToleratesMissingOnes(t *testing.T) {
	body := &closeTracker{Reader: strings.NewReader("x")}
	discardResponse(&http.Response{Body: body})
	if !body.closed {
		t.Error("discardResponse left the body open")
	}
	discardResponse(nil)
	discardResponse(&http.Response{})
}
