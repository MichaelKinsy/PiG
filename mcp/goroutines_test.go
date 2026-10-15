package mcp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/mcp"
)

// goroutineGrace bounds how long a goroutine may take to return after its client closed.
const goroutineGrace = 10 * time.Second

// The goroutine gates of the three ways a Client reaches a server. Each takes a baseline before the owner starts, runs the owner through requests, an
// aborted request and Close, and fails when a goroutine the client or its transport started is still alive.

func TestStdioClientLeavesNoGoroutineBehindAfterClose(t *testing.T) {
	baseline := testenv.BaselineGoroutines()
	command, args, env := fixtureStdioOptions("stdio-server")
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env, OnStderr: func(string) {}})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "goroutine-gate", Version: "1.0.0"}})
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(t.Context(), mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"}, mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	baseline.RequireEnded(t, goroutineGrace)
}

func TestStreamableHTTPClientLeavesNoGoroutineBehindAfterClose(t *testing.T) {
	requests := &requestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			plainProtocol(w, r, requests)
			return
		}
		// The server keeps the GET stream open until the client goes away, so the transport's stream goroutine lives
		// until Close ends it.
		requests.add(recordedRequest{method: "GET", headers: r.Header.Clone()})
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	// The server's accept loop is the peer's and outlives the client; the baseline starts after it.
	baseline := testenv.BaselineGoroutines()
	// The client's connections come from a pool this test owns, so after Close only its idle keep-alive connections
	// may remain, and closing those cannot end a goroutine that still reads a stream the transport failed to cancel.
	pool := &http.Transport{}
	transport := newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: server.URL + "/mcp", Fetch: &http.Client{Transport: pool}})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"}, mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	// The GET stream opens after initialization and is the transport's long-lived goroutine.
	waitFor(t, func() bool {
		for _, r := range requests.all() {
			if r.method == "GET" {
				return true
			}
		}
		return false
	})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	// The idle connections end, and with them the test server's goroutines that serve them. A connection still in use
	// stays open, so its client and server goroutines both fail the check.
	pool.CloseIdleConnections()
	baseline.RequireEnded(t, goroutineGrace)
}

func TestInMemoryClientLeavesNoGoroutineBehindAfterAbortedAndTimedOutRequests(t *testing.T) {
	clientTransport, server := createServer(t)
	release := make(chan struct{})
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		<-release
		return nil, errors.New("released")
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "goroutine-gate", Version: "1.0.0"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	baseline := testenv.BaselineGoroutines()

	ctx, cancel := context.WithCancelCause(t.Context())
	errs := make(chan error, 1)
	go func() {
		_, err := client.CallTool(ctx, "wait", map[string]any{}, mcp.RequestOptions{})
		errs <- err
	}()
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.Method == "tools/call" {
				return true
			}
		}
		return false
	})
	cancel(errors.New("stop"))
	if err := <-errs; err == nil {
		t.Fatal("an aborted call succeeded")
	}
	if _, err := client.CallTool(t.Context(), "wait", map[string]any{}, mcp.RequestOptions{TimeoutMs: 5}); err == nil {
		t.Fatal("a timed-out call succeeded")
	}
	close(release)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	baseline.RequireEnded(t, goroutineGrace)
}
