package mcp_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts:174-179 registers the roots/list handler only when the roots option is set (an array, a function, or the empty array), and answers
// with the array or the function's result; client.ts:496 answers every request without a handler with -32601 "Method not found: <method>".
// Pi: packages/mcp/src/client.ts:174-179 (roots handler), :496 (method not found), :216 (roots capability)
func TestClientAnswersRootsListOnlyWhenRootsAreConfigured(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options mcp.ClientOptions
		want    string
	}{
		{"no roots option", mcp.ClientOptions{}, `{"jsonrpc":"2.0","id":"r","error":{"code":-32601,"message":"Method not found: roots/list"}}`},
		{"empty roots array", mcp.ClientOptions{Roots: []mcp.Root{}}, `{"jsonrpc":"2.0","id":"r","result":{"roots":[]}}`},
		{"roots function", mcp.ClientOptions{RootsFunc: func(context.Context) ([]mcp.Root, error) {
			return []mcp.Root{{URI: "file:///fn"}}, nil
		}}, `{"jsonrpc":"2.0","id":"r","result":{"roots":[{"uri":"file:///fn"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientTransport, server := createServer(t)
			tc.options.Implementation = mcp.Implementation{Name: "c", Version: "1"}
			client := mcp.NewClient(tc.options)
			if _, err := client.Connect(t.Context(), clientTransport); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			if err := server.transport.Send(context.Background(), mcp.NewRequest(mcp.StringID("r"), "roots/list", nil)); err != nil {
				t.Fatal(err)
			}
			var answer mcp.JSONRPCMessage
			waitFor(t, func() bool {
				for _, m := range server.recorded() {
					if m.IsResponse() && m.ID.String() == "r" {
						answer = m
						return true
					}
				}
				return false
			})
			jsonEqual(t, answer, tc.want)
		})
	}
}
