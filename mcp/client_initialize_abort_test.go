package mcp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts:421-429 ("The spec forbids cancelling `initialize`"): aborting an in-flight initialize rejects it with an AbortError but sends no
// notifications/cancelled, while any other aborted request does (TestClientCancelsAbortedAndTimedOutRequests).
// Pi: packages/mcp/src/client.ts:421-429
func TestClientDoesNotSendACancelNotificationWhenInitializeIsAborted(t *testing.T) {
	clientTransport, server := createServer(t)
	initializing := make(chan struct{}, 1)
	server.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) {
		initializing <- struct{}{}
		return blockUntilTestEnds(t)
	})
	cancelled := func() int {
		count := 0
		for _, m := range server.recorded() {
			if m.Method == "notifications/cancelled" {
				count++
			}
		}
		return count
	}
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "2.0.0"}})
	ctx, cancel := context.WithCancelCause(t.Context())
	errs := make(chan error, 1)
	go func() {
		_, err := client.Connect(ctx, clientTransport)
		errs <- err
	}()
	<-initializing
	cancel(errors.New("stop"))
	var abortErr *mcp.McpAbortError
	if err := <-errs; !errors.As(err, &abortErr) {
		t.Fatalf("aborted initialize error = %v, want an McpAbortError", err)
	}
	// A cancel notification, when sent, goes out right after the rejection; no notification within this window is the absence the spec requires.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if n := cancelled(); n != 0 {
			t.Fatalf("initialize abort sent %d notifications/cancelled, want none", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
