package sdk

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
)

// serveToolRequests runs a one-tool extension on a real in-process socket and returns a function that sends one tool_call and waits for its response. It excludes process spawn and the host's own work, so it measures the SDK's decode, dispatch, handler call and encode.
func serveToolRequests(tb testing.TB, size int) (call func() string, stop func()) {
	tb.Helper()
	client, server := net.Pipe()
	connection, host := newConn(client), newConn(server)
	ext := New("bench")
	ext.conn = connection
	text := strings.Repeat("x", size)
	ext.Tool("echo", "Echo", Schema{"type": "object"}, func(Context, map[string]any) (any, error) {
		return map[string]any{"content": text}, nil
	})
	connection.start()
	host.start()
	loopDone := make(chan error, 1)
	go func() { loopDone <- ext.loop() }()
	args := json.RawMessage(`{"text":"hello"}`)
	request := envelope{Type: msgRequest, ID: "req", Request: &requestMsg{Method: "tool_call", Tool: "echo", ToolCallID: "call", Args: args}}
	call = func() string {
		if err := host.send(request); err != nil {
			tb.Fatal(err)
		}
		for env := range host.incoming {
			if env.Type == msgResponse {
				if env.Response.Error != nil {
					tb.Fatalf("tool failed: %+v", env.Response.Error)
				}
				return string(env.Response.Result)
			}
		}
		tb.Fatal("connection ended before the response")
		return ""
	}
	return call, func() {
		_ = host.send(envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
		<-loopDone
		_ = client.Close()
		_ = server.Close()
	}
}

func BenchmarkToolRequest(b *testing.B) {
	for _, size := range []int{0, 1024, 64 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			call, stop := serveToolRequests(b, size)
			defer stop()
			b.ReportAllocs()
			for b.Loop() {
				if got := call(); len(got) < size {
					b.Fatalf("short result %d", len(got))
				}
			}
		})
	}
}
