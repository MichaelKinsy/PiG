package http

import "testing"

// Ports packages/chord/test/examples/66-websocket-limits.ts: WebSocket frame limits. The default frame limit of the links
// endpoint is 1 MiB; an oversized frame receives close code 1009 instead of an abrupt disconnect. The script's printed
// line ("Oversized frame close code:") is the assertion here. The links endpoint requires authentication; the example's
// client is admitted as the owner (the `x-principal` header the test server's authenticator reads).
func TestExample66WebSocketLimits(t *testing.T) {
	server := serving(t, nil, servedOptions{links: &LinksOptions{Path: "/chord"}})
	socket := mustSocketTo(t, server.port, "/chord", owner)
	// The close is observed before the frame is sent, so it cannot race the observer.
	closed := socket.closed
	socket.Send(make([]byte, 1024*1024+1), true, nil)
	if event := receive(t, closed, "the close"); event.Code != 1009 {
		t.Fatalf("Oversized frame close code: %d, want 1009", event.Code)
	}
}
