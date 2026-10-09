package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// The reconnect policy of streamable-http.ts (consumeResponseStream, runGetStream, isRetryable, reconnectDelay) is not
// pinned by streamable-http.test.ts; these tests pin each rule of it.

// listChangedEvent is an SSE event that carries a notification and the event ID id.
func listChangedEvent(id string) string {
	notification, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
	return fmt.Sprintf("id: %s\ndata: %s\n\n", id, notification)
}

// toolAnswerEvent is an SSE event that answers the tools/call request with the given id.
func toolAnswerEvent(eventID string, requestID any) string {
	answer, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": requestID, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "resumed"}}}})
	return fmt.Sprintf("id: %s\ndata: %s\n\n", eventID, answer)
}

// resumeServer answers tools/call with first and every resume GET with resume(n, requestID), n counting from 1. It
// records the Last-Event-ID of each resume.
func resumeServer(t *testing.T, first string, resume func(n int, requestID any) string) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requestID any
	var resumes []string
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodGet && r.Header.Get("Last-Event-Id") != "" {
			mu.Lock()
			resumes = append(resumes, r.Header.Get("Last-Event-Id"))
			n, id := len(resumes), requestID
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, resume(n, id))
			return
		}
		if r.Method != http.MethodPost {
			plainProtocol(w, r, requests)
			return
		}
		message := readJSONBody(r)
		if message["method"] != "tools/call" {
			protocolHandler(w, r, requests, message)
			return
		}
		mu.Lock()
		requestID = message["id"]
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, first)
	})
	return url, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(resumes)
	}
}

func connectHTTP(t *testing.T, options mcp.StreamableHTTPTransportOptions) *mcp.Client {
	t.Helper()
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, options)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// reconnectDelay returns the server's retry value before any backoff: `if (serverDelayMs !== undefined) return
// serverDelayMs`. A 60 s initial delay would outlast the call's deadline, so only the server's 5 ms (and 0 ms, which is a
// value, not an absent one) lets the resume finish.
func TestStreamableHTTPTransportWaitsTheServersRetryDelayBeforeResumingAResponseStream(t *testing.T) {
	for _, retry := range []string{"5", "0"} {
		t.Run(retry, func(t *testing.T) {
			url, resumes := resumeServer(t, "id: 1\nretry: "+retry+"\ndata:\n\n", func(_ int, requestID any) string {
				return toolAnswerEvent("2", requestID)
			})
			client := connectHTTP(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false), Reconnect: mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 60_000}})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result, err := client.CallTool(ctx, "echo", nil, mcp.RequestOptions{})
			if err != nil {
				t.Fatalf("the resume waited for the 60 s backoff instead of the server's retry %s ms: %v", retry, err)
			}
			jsonEqual(t, result, `{"content":[{"type":"text","text":"resumed"}]}`)
			if got := resumes(); !slices.Equal(got, []string{"1"}) {
				t.Fatalf("resumes = %q", got)
			}
		})
	}
}

// consumeResponseStream: `if (cursor.received) attempt = 0;` a resumed stream that delivered an event restores the full
// retry budget, so a response stream the server keeps closing after progress is resumed past maxRetries. The budget is
// checked before the reset, so with maxRetries 2 the third resume happens only because of it.
func TestStreamableHTTPTransportResetsTheResumeBudgetWhileAResponseStreamMakesProgress(t *testing.T) {
	url, resumes := resumeServer(t, listChangedEvent("1"), func(n int, requestID any) string {
		if n < 3 {
			return listChangedEvent(fmt.Sprint(n + 1))
		}
		return toolAnswerEvent("4", requestID)
	})
	client := connectHTTP(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false), Reconnect: mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 1, MaxRetries: 2}})
	result, err := client.CallTool(t.Context(), "echo", nil, mcp.RequestOptions{TimeoutMs: 10_000})
	if err != nil {
		t.Fatalf("a stream that keeps delivering events was abandoned after maxRetries: %v", err)
	}
	jsonEqual(t, result, `{"content":[{"type":"text","text":"resumed"}]}`)
	if got := resumes(); !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Fatalf("resumes = %q", got)
	}
}

// consumeResponseStream: `if (cursor.lastEventId === undefined || attempt >= this.maxRetries()) break;` resumes that
// deliver nothing stop after maxRetries attempts, and the request fails with the reason of the last stream.
func TestStreamableHTTPTransportStopsResumingAResponseStreamAfterMaxRetriesWithoutProgress(t *testing.T) {
	for _, maxRetries := range []int{1, 2} {
		t.Run(fmt.Sprint(maxRetries), func(t *testing.T) {
			url, resumes := resumeServer(t, listChangedEvent("1"), func(int, any) string { return ": nothing here\n\n" })
			client := connectHTTP(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false), Reconnect: mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 1, MaxRetries: maxRetries}})
			_, err := client.CallTool(t.Context(), "echo", nil, mcp.RequestOptions{TimeoutMs: 10_000})
			if err == nil || !strings.Contains(err.Error(), "MCP response stream failed: stream ended without a response") {
				t.Fatalf("err = %v", err)
			}
			if got := len(resumes()); got != maxRetries {
				t.Fatalf("resumed %d times, want maxRetries = %d", got, maxRetries)
			}
		})
	}
}

// runGetStream retries a GET stream that fails with a transient status (isTransientStatus: 408, 429 and 5xx) until
// maxRetries consecutive attempts failed, then reports the stream lost. Any other status is reported at once.
func TestStreamableHTTPTransportRetriesTheGETStreamOnlyOnTransientStatusesAndUpToMaxRetries(t *testing.T) {
	const maxRetries = 2
	for _, test := range []struct {
		status int
		gets   int32
		err    string
	}{
		{408, maxRetries + 1, "MCP server-to-client stream dropped and could not be reopened"},
		{429, maxRetries + 1, "MCP server-to-client stream dropped and could not be reopened"},
		{500, maxRetries + 1, "MCP server-to-client stream dropped and could not be reopened"},
		{503, maxRetries + 1, "MCP server-to-client stream dropped and could not be reopened"},
		{400, 1, "MCP HTTP request failed with status 400: refused"},
		{403, 1, "MCP HTTP request failed with status 403: refused"},
		{409, 1, "MCP HTTP request failed with status 409: refused"},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			var gets atomic.Int32
			url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
				if r.Method != http.MethodGet {
					plainProtocol(w, r, requests)
					return
				}
				gets.Add(1)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, "refused")
			})
			client := newHTTPClient()
			reported := make(chan error, 8)
			client.OnError(func(err error) { reported <- err })
			if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, Reconnect: mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 1, MaxRetries: maxRetries}})); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			select {
			case err := <-reported:
				if err.Error() != test.err {
					t.Fatalf("reported %q, want %q", err, test.err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the GET stream failure was never reported")
			}
			if got := gets.Load(); got != test.gets {
				t.Fatalf("GET attempts = %d, want %d", got, test.gets)
			}
			select {
			case err := <-reported:
				t.Fatalf("a second error was reported: %v", err)
			default:
			}
		})
	}
}

// reconnectDelay is `serverDelayMs ?? Math.min(initial * 2 ** attempt, this.maxDelay())`, with initial 1000 and
// maxDelay 30000 by default.
func TestStreamableHTTPReconnectDelayDoublesFromTheInitialDelayUpToTheCapUnlessTheServerSetsIt(t *testing.T) {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	for _, test := range []struct {
		name    string
		options mcp.StreamableHTTPReconnectOptions
		attempt int
		retry   *int
		want    time.Duration
	}{
		{"default first", mcp.StreamableHTTPReconnectOptions{}, 0, nil, ms(1000)},
		{"default second", mcp.StreamableHTTPReconnectOptions{}, 1, nil, ms(2000)},
		{"default fifth", mcp.StreamableHTTPReconnectOptions{}, 4, nil, ms(16000)},
		{"default capped", mcp.StreamableHTTPReconnectOptions{}, 5, nil, ms(30000)},
		{"default far past the cap", mcp.StreamableHTTPReconnectOptions{}, 2000, nil, ms(30000)},
		{"custom", mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 10, MaxDelayMs: 50}, 2, nil, ms(40)},
		{"custom capped", mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 10, MaxDelayMs: 50}, 3, nil, ms(50)},
		{"server retry", mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 10, MaxDelayMs: 50}, 7, new(5), ms(5)},
		{"server retry above the cap", mcp.StreamableHTTPReconnectOptions{}, 0, new(45000), ms(45000)},
		{"server retry zero", mcp.StreamableHTTPReconnectOptions{}, 3, new(0), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := mcp.ReconnectDelayForTest(test.options, test.attempt, test.retry); got != test.want {
				t.Fatalf("reconnectDelay(%d) = %v, want %v", test.attempt, got, test.want)
			}
		})
	}
}
