package mcp_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi: packages/mcp/src/transports/streamable-http.ts:238, 406-409. After notifications/initialized the transport starts the GET stream without waiting for its
// response (`void this.runGetStream()`), so a client whose server holds the GET response open still finishes connecting. The Go transport waits only until the
// GET request is written, not until its response headers arrive.
// mutation-checked: the mutant "the write trace is attached to non-GET requests" (streamable_http.go:433) fails it.
func TestStreamableHTTPTransportConnectDoesNotWaitForTheGETStreamResponse(t *testing.T) {
	release := make(chan struct{})
	gotGET := make(chan struct{}, 1)
	url, _ := startServer(t, func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		var message map[string]any
		if r.Method == http.MethodPost {
			message = readJSONBody(r)
		}
		if r.Method == http.MethodGet {
			gotGET <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		protocolHandler(w, r, requests, message)
	})
	t.Cleanup(func() { close(release) })
	client := newHTTPClient()
	connected := make(chan error, 1)
	go func() {
		_, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url}))
		connected <- err
	}()
	select {
	case err := <-connected:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Connect did not return while the server held the GET stream response")
	}
	select {
	case <-gotGET:
	case <-time.After(5 * time.Second):
		t.Fatal("the GET stream request never reached the server")
	}
}
