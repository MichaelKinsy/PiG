package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts handleCancelled aborts `this.incoming.get(params.requestId)`: a Map lookup, so a server request with id -0 is cancelled by requestId 0
// (SameValueZero), and the client's response carries JSON.stringify's id 0.
func TestClientCancelsAServerRequestWhoseIDIsNegativeZeroByRequestIDZero(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	cancelled := make(chan struct{})
	client.SetRequestHandler("custom/wait", func(ctx context.Context, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, context.Cause(ctx)
	})
	request, err := mcp.ParseJSONRPCMessage([]byte(`{"jsonrpc":"2.0","id":-0,"method":"custom/wait"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.transport.Send(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := server.transport.Send(context.Background(), mcp.NewNotification("notifications/cancelled", json.RawMessage(`{"requestId":0,"reason":"stop"}`))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("notifications/cancelled with requestId 0 did not abort the request with id -0")
	}
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.IsResponse() {
				wire, err := json.Marshal(m)
				if err != nil || !strings.Contains(string(wire), `"id":0,`) {
					t.Fatalf("response = %s (%v), want id 0", wire, err)
				}
				return true
			}
		}
		return false
	})
}
