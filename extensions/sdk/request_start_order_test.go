package sdk

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The dispatch path takes a place for each request that runs extension code (a tool_call, an event, a command) in arrival order and begins it
// before the handler runs (runner.ts:1096-1125, agent-loop.ts:619-647). Concurrent emits start their handlers in call order: the tool_result
// hooks of a parallel batch, or a tool_result and a tool_execution_end. One processor makes the scheduler's choice deterministic (a goroutine
// that a request starts last runs first, as the unordered loop showed), so the handlers' first statements show the order the gate gave. With
// more processors a thread the operating system preempts between the hand-off and the handler's first statement can be overtaken, which no
// test can pin.
func TestEventAndCommandHandlersStartInArrivalOrder(t *testing.T) {
	const batch, batches = 18, 24
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	ext := New("request-start-order")
	var mu sync.Mutex
	var started []int
	record := func(n int) {
		mu.Lock()
		started = append(started, n)
		mu.Unlock()
	}
	ext.RegisterTool(ToolDefinition{
		Name: "probe", Label: "probe", Description: "Record the start order", Parameters: Schema{"type": "object"},
		Execute: func(_ Context, params map[string]any) (any, error) {
			record(int(params["n"].(float64)))
			return "ok", nil
		},
	})
	ext.OnEvent("tool_result", func(_ Context, data map[string]any) (any, error) {
		record(int(data["n"].(float64)))
		return nil, nil
	})
	ext.Command("probe", "Record the start order", func(_ Context, args string) error {
		var n int
		_, _ = fmt.Sscan(args, &n)
		record(n)
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	for round := range batches {
		for n := range batch {
			id := fmt.Sprintf("r%d-%d", round, n)
			switch n % 3 {
			case 0:
				host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "tool_call", Tool: "probe", ToolCallID: "c", Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, n))}})
			case 1:
				host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "event", Event: "tool_result", HandlerID: 1, Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, n))}})
			default:
				host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "command", Tool: "probe", Args: json.RawMessage(fmt.Sprintf(`"%d"`, n))}})
			}
		}
		for range batch {
			if resp := host.readEnvelope(t); resp.Type != msgResponse || resp.Response.Error != nil {
				t.Fatalf("round %d: %+v", round, resp)
			}
		}
		mu.Lock()
		got := started
		started = nil
		mu.Unlock()
		want := make([]int, batch)
		for n := range want {
			want[n] = n
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: handlers started as %v, want %v", round, got, want)
		}
	}
}

// An event request that ends before its handler (an unknown handler id) hands its place on, so the next request's handler is not held behind it.
func TestEventThatEndsBeforeItsHandlerHandsOnItsPlace(t *testing.T) {
	ext := New("event-start-release")
	ext.OnEvent("tool_result", func(Context, map[string]any) (any, error) { return nil, nil })
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	for round := range 8 {
		_, resp := runSurfaceRequest(t, host, fmt.Sprintf("missing-%d", round), &requestMsg{Method: "event", Event: "tool_result", HandlerID: 99, Args: json.RawMessage(`{}`)}, nil)
		if resp.Error == nil {
			t.Fatalf("round %d: an unknown handler succeeded", round)
		}
		_, resp = runSurfaceRequest(t, host, fmt.Sprintf("event-%d", round), &requestMsg{Method: "event", Event: "tool_result", HandlerID: 1, Args: json.RawMessage(`{}`)}, nil)
		if resp.Error != nil {
			t.Fatalf("round %d: %+v", round, resp.Error)
		}
	}
}
