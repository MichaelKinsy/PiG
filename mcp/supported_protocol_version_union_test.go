package mcp_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// packages/mcp/src/protocol/types.ts SupportedProtocolVersion = (typeof SUPPORTED_PROTOCOL_VERSIONS)[number]: the client accepts a server that selects any member of
// the union, records the version it selected, and refuses every other.
// mutation-checked: removing one member from SupportedProtocolVersions makes the client refuse that version and fails it.
func TestClientAcceptsEverySupportedProtocolVersionUnionMember(t *testing.T) {
	if len(mcp.SupportedProtocolVersions) != 4 {
		t.Fatalf("SupportedProtocolVersions = %v, want the four Pi lists", mcp.SupportedProtocolVersions)
	}
	connectTo := func(t *testing.T, selected string) (*mcp.Client, error) {
		t.Helper()
		clientTransport, server := createServer(t)
		server.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) {
			return map[string]any{"protocolVersion": selected, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "server", "version": "0.1.0"}}, nil
		})
		client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "1.0.0"}})
		_, err := client.Connect(t.Context(), clientTransport)
		return client, err
	}
	for _, version := range mcp.SupportedProtocolVersions {
		client, err := connectTo(t, string(version))
		if err != nil {
			t.Fatalf("a server selecting %s was refused: %v", version, err)
		}
		if got := mcp.SupportedProtocolVersion(client.ProtocolVersion()); got != version {
			t.Errorf("recorded version = %s, want %s", got, version)
		}
		_ = client.Close()
	}
	for _, other := range []string{"1999-01-01", "2025-11-24", ""} {
		if _, err := connectTo(t, other); err == nil || !strings.Contains(err.Error(), "protocol version") {
			t.Errorf("a server selecting %q was accepted: %v", other, err)
		}
	}
}
