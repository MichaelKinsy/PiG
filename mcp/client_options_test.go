package mcp_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// packages/mcp/src/protocol/types.ts:15 `Implementation.title` and packages/mcp/src/client.ts:47 `McpClientOptions extends Implementation` carry the title into clientInfo.
// PiG-only: packages/mcp/test/client.test.ts never sends a title, capabilities, a pinned protocolVersion or roots in
// McpClientOptions, so the initialize request that carries each option has no upstream test.
// Pi declares packages/mcp/src/protocol/types.ts:12-16 (Implementation): the client implementation carries its title.
func TestClientInitializeCarriesTheConfiguredClientOptions(t *testing.T) {
	clientTransport, server := createServer(t)
	client := mcp.NewClient(mcp.ClientOptions{
		Implementation:  mcp.Implementation{Name: "opts-client", Version: "3.1.4", Title: "Options Client"},
		Capabilities:    &mcp.ClientCapabilities{Sampling: map[string]any{}},
		ProtocolVersion: "2025-06-18",
		Roots:           []mcp.Root{},
	})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	recorded := server.recorded()
	if len(recorded) == 0 || recorded[0].Method != "initialize" {
		t.Fatalf("recorded = %+v", recorded)
	}
	jsonEqual(t, paramsOf(t, recorded[0]), `{
		"protocolVersion":"2025-06-18",
		"capabilities":{"sampling":{},"roots":{}},
		"clientInfo":{"name":"opts-client","version":"3.1.4","title":"Options Client"}
	}`)
}
