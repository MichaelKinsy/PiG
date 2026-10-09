package mcp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// StreamableHttpTransportOptions.headers and maxMessageBytes
// (packages/mcp/src/transports/streamable-http.ts). Pi's streamable-http test does not
// exercise them, so these tests are PiG's own.

func TestStreamableHTTPTransportSendsTheConfiguredHeadersOnEveryRequest(t *testing.T) {
	url, requests := startServer(t, plainProtocol)
	transport := newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, Headers: map[string]string{"X-Pig-Test": "configured"}})
	client := newHTTPClient()
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(t.Context(), mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
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
	seen := map[string]bool{}
	for _, r := range requests.all() {
		seen[r.method] = true
		if got := r.headers.Get("X-Pig-Test"); got != "configured" {
			t.Fatalf("%s request carried X-Pig-Test = %q", r.method, got)
		}
	}
	if !seen["POST"] || !seen["GET"] || !seen["DELETE"] {
		t.Fatalf("requests seen = %v, want POST, GET and DELETE all carrying the header", seen)
	}
}

func TestStreamableHTTPTransportRejectsAnSSEEventLargerThanMaxMessageBytes(t *testing.T) {
	big := func(w http.ResponseWriter, r *http.Request, requests *requestLog) {
		if r.Method == http.MethodPost {
			message := readJSONBody(r)
			if message["method"] == "tools/call" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 4000)}}}})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				return
			}
			protocolHandler(w, r, requests, message)
			return
		}
		plainProtocol(w, r, requests)
	}
	call := func(maxMessageBytes int) error {
		url, _ := startServer(t, big)
		client := newHTTPClient()
		if _, err := client.Connect(t.Context(), newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, MaxMessageBytes: maxMessageBytes, OpenGetStream: new(false)})); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		_, err := client.CallTool(t.Context(), "echo", map[string]any{}, mcp.RequestOptions{})
		return err
	}
	if err := call(0); err != nil {
		t.Fatalf("the default limit rejected a 4000-byte event: %v", err)
	}
	err := call(1024)
	if err == nil || !strings.Contains(err.Error(), "MCP SSE event exceeds 1024 bytes") {
		t.Fatalf("error = %v, want the 1024-byte limit to reject the 4000-byte event", err)
	}
}
