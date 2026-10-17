package mcp_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// streamable-http.ts:221-223 setProtocolVersion stores the version and :313 sends it as the MCP-Protocol-Version header of every later request:
// absent before the first call, the stored value after it, and the newest value after a second call. The transport is driven directly, so the
// header can only come from SetProtocolVersion and not from a client's initialize.
// Pi: packages/mcp/src/transports/streamable-http.ts:221 (setProtocolVersion), :313 (header)
func TestStreamableHTTPTransportSetProtocolVersionSetsTheHeaderOfLaterRequests(t *testing.T) {
	url, requests := startServer(t, plainProtocol)
	transport := newHTTPTransport(t, mcp.StreamableHTTPTransportOptions{URL: url, OpenGetStream: new(false)})
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	send := func(method string) {
		t.Helper()
		if err := transport.Send(context.Background(), mcp.NewNotification(method, nil)); err != nil {
			t.Fatal(err)
		}
	}
	versionOf := func(method string) (string, bool) {
		t.Helper()
		for _, r := range requests.all() {
			if r.message["method"] == method {
				return r.headers.Get("MCP-Protocol-Version"), true
			}
		}
		return "", false
	}
	send("notifications/one")
	transport.SetProtocolVersion("2025-03-26")
	send("notifications/two")
	transport.SetProtocolVersion("2025-06-18")
	send("notifications/three")
	for method, want := range map[string]string{"notifications/one": "", "notifications/two": "2025-03-26", "notifications/three": "2025-06-18"} {
		got, seen := versionOf(method)
		if !seen || got != want {
			t.Errorf("%s: MCP-Protocol-Version = %q (request seen %v), want %q", method, got, seen, want)
		}
	}
}
