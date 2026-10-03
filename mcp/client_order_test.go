package mcp_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// gatedTransport holds the write of the first `tools/call` request until the test opens the gate.
type gatedTransport struct {
	*mcptest.InMemoryTransport
	once    sync.Once
	reached chan struct{}
	gate    chan struct{}
}

func (g *gatedTransport) SendOrdered(message mcp.JSONRPCMessage, placed func()) error {
	if message.Method == "tools/call" {
		first := false
		g.once.Do(func() { first = true })
		if first {
			close(g.reached)
			<-g.gate
		}
	}
	return g.InMemoryTransport.SendOrdered(message, placed)
}

// Pi's Protocol.request calls transport.send synchronously when it assigns the request id, so a stdio or in-process
// server receives requests in id order (mcp/src/client.ts request). A request issued while an earlier one is still being
// written reaches the server after it.
func TestClientWritesRequestsInIDOrder(t *testing.T) {
	clientTransport, server := createServer(t)
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"content": []any{}}, nil
	})
	gated := &gatedTransport{InMemoryTransport: clientTransport, reached: make(chan struct{}), gate: make(chan struct{})}
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "test-client", Version: "2.0.0"}})
	if _, err := client.Connect(t.Context(), gated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var openGate sync.Once
	open := func() { openGate.Do(func() { close(gated.gate) }) }
	// A failure leaves the first write held; open the gate before Close waits for it.
	t.Cleanup(open)

	call := func(name string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := client.CallTool(t.Context(), name, map[string]any{}, mcp.RequestOptions{})
			done <- err
		}()
		return done
	}
	calledTools := func() []string {
		var names []string
		for _, m := range server.recorded() {
			if m.Method == "tools/call" {
				names = append(names, paramsOf(t, m)["name"].(string))
			}
		}
		return names
	}

	firstDone := call("first")
	<-gated.reached
	secondDone := call("second")
	// The second request has its id but must not overtake the first on the wire.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := calledTools(); len(got) != 0 {
			t.Fatalf("server received %v before the first request was written", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	open()
	for _, done := range []<-chan error{firstDone, secondDone} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := calledTools(); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("tools/call order = %v, want [first second]", got)
	}
}

// A direct MCP tool forwards the arguments the agent validated as json.RawMessage, and the server receives them in the model's
// member order, as Pi's `callTool({name, arguments})` sends the object the model produced (mcp/src/client.ts callTool).
func TestClientCallToolSendsRawArgumentsInTheirMemberOrder(t *testing.T) {
	client, server := connect(t)
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"content": []any{}}, nil
	})
	const arguments = `{"zeta":"1","alpha":{"yy":2,"bb":[{"qq":1,"aa":2}]}}`
	if _, err := client.CallTool(t.Context(), "probe", json.RawMessage(arguments), mcp.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, message := range server.recorded() {
		if message.Method != "tools/call" {
			continue
		}
		var params struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(message.Params, &params); err != nil {
			t.Fatal(err)
		}
		if string(params.Arguments) != arguments {
			t.Fatalf("server received arguments %s, want %s", params.Arguments, arguments)
		}
		return
	}
	t.Fatal("the server received no tools/call")
}
