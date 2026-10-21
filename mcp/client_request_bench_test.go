package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// BenchmarkClientCallTool is one tools/call round trip over an in-process transport: the request takes its place on the wire, is written in id order, and its response settles it. Parallel callers share one client, as a parallel tool batch does.
func BenchmarkClientCallTool(b *testing.B) {
	clientTransport, serverTransport := mcptest.NewInMemoryTransportPair()
	serverTransport.OnMessage(func(message mcp.JSONRPCMessage) {
		if !message.IsRequest() {
			return
		}
		var result any = map[string]any{"content": []any{}}
		if message.Method == "initialize" {
			result = map[string]any{
				"protocolVersion": mcp.LatestProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "bench-server", "version": "1.0.0"},
			}
		}
		raw, _ := json.Marshal(result)
		go func() { _ = serverTransport.Send(context.Background(), mcp.NewResult(*message.ID, raw)) }()
	})
	if err := serverTransport.Start(); err != nil {
		b.Fatal(err)
	}
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "bench-client", Version: "1.0.0"}})
	if _, err := client.Connect(b.Context(), clientTransport); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = client.Close(); _ = serverTransport.Close() })
	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "parallel"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			call := func() {
				if _, err := client.CallTool(b.Context(), "tool", map[string]any{}, mcp.RequestOptions{}); err != nil {
					b.Error(err)
				}
			}
			if !parallel {
				for b.Loop() {
					call()
				}
				return
			}
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					call()
				}
			})
		})
	}
}
