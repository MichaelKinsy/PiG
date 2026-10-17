package mcp_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Behaviour of StreamableHttpTransport (packages/mcp/src/transports/streamable-http.ts, Pi 1.1.0) that streamable-http.test.ts
// does not pin. Each test drives the transport through the Client against a real HTTP server with Pi's inputs.

// streamable-http.ts:255 and :457: a 200 whose content type is neither JSON nor an event stream is an McpHttpError that names the
// type, or `missing` when the response carries none. The POST case is the initialize request; the GET case is the stream opened after it.
//
// mutation-checked: the mutant "a missing POST content type is reported as an empty name" fails it.
// mutation-checked: the mutant "a missing GET content type is reported as an empty name" fails it.
func TestStreamableHTTPTransportNamesTheUnsupportedContentTypeOrMissing(t *testing.T) {
	for _, test := range []struct{ name, contentType, want string }{
		{"text/plain", "text/plain", "text/plain"},
		{"missing", "", "missing"},
	} {
		respond := func(w http.ResponseWriter) {
			if test.contentType == "" {
				w.Header()["Content-Type"] = nil
			} else {
				w.Header().Set("Content-Type", test.contentType)
			}
			w.WriteHeader(200)
		}
		t.Run("POST "+test.name, func(t *testing.T) {
			url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, _ *requestLog) {
				_, _ = io.ReadAll(r.Body)
				respond(w)
			})
			client := newHTTPClient()
			_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false)}))
			if want := "Unsupported MCP response content type: " + test.want; err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to contain %q", err, want)
			}
		})
		t.Run("GET "+test.name, func(t *testing.T) {
			url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
				if r.Method != http.MethodGet {
					plainProtocol(w, r, requests)
					return
				}
				respond(w)
			})
			client := newHTTPClient()
			reported := make(chan error, 4)
			client.OnError(func(err error) { reported <- err })
			if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url})); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			select {
			case err := <-reported:
				if want := "Unsupported MCP GET response content type: " + test.want; !strings.Contains(err.Error(), want) {
					t.Fatalf("reported %q, want it to contain %q", err, want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the GET stream failure was never reported")
			}
		})
	}
}

// streamable-http.ts:393 (consumeResponseStream): a resume GET that fails with a status isRetryable rejects ends the request at once
// with that error; a transient status is retried until maxRetries. 400 is not transient, 503 is.
//
// mutation-checked: the mutant "isTransientStatus counts 400 as transient, so the resume is retried" fails it.
// mutation-checked: the mutant "a transient resume failure is not retried" fails it.
func TestStreamableHTTPTransportStopsResumingAResponseStreamOnANonRetryableFailure(t *testing.T) {
	const maxRetries = 3
	for _, test := range []struct {
		status  int
		resumes int
		reason  string
	}{
		{400, 1, "MCP HTTP request failed with status 400: gone"},
		{503, maxRetries, "MCP HTTP request failed with status 503: gone"},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			var resumes atomic.Int32
			url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
				if r.Method == http.MethodGet && r.Header.Get("Last-Event-Id") != "" {
					resumes.Add(1)
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, "gone")
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
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				_, _ = io.WriteString(w, "id: 1\ndata:\n\n")
			})
			client := connectHTTP(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false), Reconnect: mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 1, MaxRetries: maxRetries}})
			_, err := client.CallTool(t.Context(), "echo", nil, mcp.RequestOptions{TimeoutMs: 10_000})
			if err == nil || !strings.Contains(err.Error(), "MCP response stream failed: "+test.reason) {
				t.Fatalf("err = %v, want the stream failure %q", err, test.reason)
			}
			if got := int(resumes.Load()); got != test.resumes {
				t.Fatalf("resume GETs = %d, want %d", got, test.resumes)
			}
		})
	}
}

// streamable-http.ts:423 (runGetStream): `if (cursor.received || Date.now() - openedAt > this.maxDelay()) attempt = 0`. A GET stream that
// delivers an event before it drops counts as healthy, so the retry budget starts over: with maxRetries 1 the stream is reopened again and
// again while it delivers, and no error is reported.
//
// mutation-checked: the mutant "a GET stream that delivered an event does not reset the retry budget" fails it.
func TestStreamableHTTPTransportResetsTheGETRetryBudgetWhenAStreamDeliveredAnEvent(t *testing.T) {
	var gets atomic.Int32
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method != http.MethodGet {
			plainProtocol(w, r, requests)
			return
		}
		gets.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, listChangedEvent("a"))
	})
	client := newHTTPClient()
	reported := make(chan error, 4)
	client.OnError(func(err error) { reported <- err })
	if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, Reconnect: mcp.StreamableHTTPReconnectOptions{InitialDelayMs: 1, MaxRetries: 1}})); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	waitFor(t, func() bool { return gets.Load() >= 5 })
	select {
	case err := <-reported:
		t.Fatalf("a GET stream that kept delivering reported %v after %d connections", err, gets.Load())
	default:
	}
}
