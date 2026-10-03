package mcp_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/stdio.test.ts.

func TestStdioTransportConnectsToANewlineDelimitedMCPServerAndCapturesStderr(t *testing.T) {
	command, args, env := fixtureStdioOptions("stdio-server")
	var mu sync.Mutex
	var stderr []string
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{
		Command: command, Args: args, Env: env,
		OnStderr: func(chunk string) {
			mu.Lock()
			stderr = append(stderr, chunk)
			mu.Unlock()
		},
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "stdio-test", Version: "1.0.0"}})
	if _, err := client.Connect(t.Context(), transport); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, tools, `[{"name":"echo","inputSchema":{"type":"object"}}]`)
	result, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result, `{"content":[{"type":"text","text":"hello"}]}`)
	if transport.PID() == 0 {
		t.Fatal("no pid")
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(stderr, ""), "stdio fixture ready")
	})
	if !strings.Contains(transport.Stderr(), "stdio fixture ready") {
		t.Fatalf("stderr = %q", transport.Stderr())
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if client.ConnectionState() != mcp.ClientStateClosed {
		t.Fatalf("state = %s", client.ConnectionState())
	}
}
