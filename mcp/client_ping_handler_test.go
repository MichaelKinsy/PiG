package mcp_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts:173 `this.requestHandlers.set("ping", () => ({}))`: a connected client answers a server's ping request with an empty object result.
func TestClientAnswersAServerPingWithAnEmptyResult(t *testing.T) {
	client, server := connect(t)
	defer func() { _ = client.Close() }()
	if err := server.transport.Send(context.Background(), mcp.NewRequest(mcp.StringID("p"), "ping", nil)); err != nil {
		t.Fatal(err)
	}
	var response mcp.JSONRPCMessage
	waitFor(t, func() bool {
		for _, m := range server.recorded() {
			if m.IsResponse() && m.ID.String() == "p" {
				response = m
				return true
			}
		}
		return false
	})
	jsonEqual(t, response, `{"jsonrpc":"2.0","id":"p","result":{}}`)
}
