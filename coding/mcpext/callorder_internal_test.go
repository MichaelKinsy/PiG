package mcpext

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/mcp"
)

// orderCaller records the order in which tool calls reach the client and gives each its id, as mcp.Client does.
type orderCaller struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
}

func (c *orderCaller) CallTool(_ context.Context, _ string, args any, options mcp.RequestOptions) (*mcp.CallToolResult, error) {
	raw, _ := args.(json.RawMessage)
	c.mu.Lock()
	err := c.fail[string(raw)]
	if err == nil {
		c.calls = append(c.calls, string(raw))
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if options.OnIssued != nil {
		options.OnIssued()
	}
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{}}, nil
}

func (c *orderCaller) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

// Pi's `Promise.all([tools.a({n: 1}), tools.a({n: 2})])` runs each call's synchronous prefix in call order, so the first
// call's request takes its id first (agent-loop.ts executeToolCallsParallel, mcp/src/client.ts request). A call whose place
// is later in the server's lane must not reach the client before every earlier call issued its request or ended.
func TestMcpToolCallsToOneServerReachTheClientInReservedOrder(t *testing.T) {
	caller := &orderCaller{fail: map[string]error{`{"n":0}`: errors.New("connection lost")}}
	definition := CreateMcpToolDefinition(McpToolOptions{
		Server: "docs", Tool: mcp.Tool{Name: "search"}, Name: "mcp__docs__search",
		GetClient: func(context.Context) (McpToolCaller, error) { return caller, nil },
		Lane:      &extension.CallLane{},
	})
	failed := definition.ReserveCallOrder(nil)
	first := definition.ReserveCallOrder(nil)
	second := definition.ReserveCallOrder(nil)
	if failed == nil || first == nil || second == nil {
		t.Fatal("ReserveCallOrder reserved nothing for a tool with a lane")
	}
	run := func(order *extension.CallOrder, args string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := definition.Execute(extension.WithCallOrder(t.Context(), order), "id", json.RawMessage(args), nil)
			done <- err
		}()
		return done
	}

	secondDone := run(second, `{"n":2}`)
	firstDone := make(chan (<-chan error), 1)
	time.AfterFunc(50*time.Millisecond, func() { firstDone <- run(first, `{"n":1}`) })
	select {
	case <-secondDone:
		t.Fatalf("the second call ended before the first was issued: %v", caller.recorded())
	case done := <-firstDone:
		if got := caller.recorded(); len(got) != 0 {
			t.Fatalf("calls before the first call ran = %v, want none", got)
		}
		// The first call waits behind a call that fails before it has an id: that call's end releases its place.
		failedDone := run(failed, `{"n":0}`)
		if err := <-failedDone; err == nil {
			t.Fatal("the failing call succeeded")
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if got, want := caller.recorded(), []string{`{"n":1}`, `{"n":2}`}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

// A definition without a lane reserves nothing, so the host does not order its calls.
func TestMcpToolWithoutALaneReservesNoCallOrder(t *testing.T) {
	definition := CreateMcpToolDefinition(McpToolOptions{Server: "docs", Tool: mcp.Tool{Name: "search"}, Name: "mcp__docs__search"})
	if order := definition.ReserveCallOrder(nil); order != nil {
		t.Fatalf("ReserveCallOrder = %+v, want nil", order)
	}
}
