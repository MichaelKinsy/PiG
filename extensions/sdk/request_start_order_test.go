package sdk

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The dispatch path takes a place for each request that runs extension code (a tool_call, an event, a command) in arrival order and invokes its
// handler only after the places before it were released (runner.ts:1096-1125, agent-loop.ts:619-647). Concurrent emits invoke their handlers in
// call order: the tool_result hooks of a parallel batch, or a tool_result and a tool_execution_end. The test reads the order where the SDK
// keeps it, in startObserver between the wait for the earlier places and the release of its own, and checks that every handler ran once. The
// handlers' first statements are not that order: a goroutine descheduled after its release can be overtaken (see
// TestOvertakenHandlerKeepsItsInvocationPlace), which no GOMAXPROCS setting prevents.
func TestEventAndCommandHandlersStartInArrivalOrder(t *testing.T) {
	const batch, batches = 18, 24
	ext := New("request-start-order")
	invoked := observeStarts(ext)
	var mu sync.Mutex
	var ran []int
	record := func(n int) {
		mu.Lock()
		ran = append(ran, n)
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
		got := slices.Clone(ran)
		ran = nil
		mu.Unlock()
		slices.Sort(got)
		if want := ascending(batch); !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: handlers ran as %v, want each of %v once", round, got, want)
		}
		if got, want := invoked(), ascending(batch); !reflect.DeepEqual(got, want) { // runner.ts:1110: each emit reaches `await handler(event, ctx)` in arrival order, with nothing awaited before the first handler
			t.Fatalf("round %d: handlers invoked as %v, want %v", round, got, want)
		}
	}
}

// The order the SDK keeps is the order of invocation. A handler that the scheduler holds back after its place was released is overtaken by the
// handlers behind it, so its first statement runs last while it was still invoked in its place. The handler is held on a channel until every
// later handler recorded, so the overtaking does not depend on timing.
func TestOvertakenHandlerKeepsItsInvocationPlace(t *testing.T) {
	const batch, held = 18, 6
	ext := New("request-start-overtaken")
	invoked := observeStarts(ext)
	var mu sync.Mutex
	var ran []int
	recorded := make(chan struct{}, batch)
	release := make(chan struct{})
	ext.RegisterTool(ToolDefinition{
		Name: "probe", Label: "probe", Description: "Record the first statement", Parameters: Schema{"type": "object"},
		Execute: func(_ Context, params map[string]any) (any, error) {
			n := int(params["n"].(float64))
			if n == held {
				<-release
			}
			mu.Lock()
			ran = append(ran, n)
			mu.Unlock()
			recorded <- struct{}{}
			return "ok", nil
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	for n := range batch {
		host.writeEnvelope(t, envelope{Type: msgRequest, ID: fmt.Sprintf("r0-%d", n), Request: &requestMsg{Method: "tool_call", Tool: "probe", ToolCallID: "c", Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, n))}})
	}
	for range batch - 1 {
		select {
		case <-recorded:
		case <-time.After(10 * time.Second):
			t.Fatal("the handlers behind the held one never recorded")
		}
	}
	close(release)
	for range batch {
		if resp := host.readEnvelope(t); resp.Type != msgResponse || resp.Response.Error != nil {
			t.Fatalf("%+v", resp)
		}
	}
	mu.Lock()
	got := slices.Clone(ran)
	mu.Unlock()
	if len(got) != batch || got[batch-1] != held {
		t.Fatalf("first statements ran as %v, want %d last: the held handler was not overtaken", got, held)
	}
	if got, want := invoked(), ascending(batch); !reflect.DeepEqual(got, want) {
		t.Fatalf("handlers invoked as %v, want %v", got, want)
	}
	slices.Sort(got)
	if want := ascending(batch); !reflect.DeepEqual(got, want) {
		t.Fatalf("handlers ran as %v, want each of %v once", got, want)
	}
}

// observeStarts records the request number of every handler invocation (the id's part after its last "-") and returns a function that takes the
// numbers recorded since the last call. It is installed before the extension serves.
func observeStarts(ext *Extension) func() []int {
	var mu sync.Mutex
	var invoked []int
	ext.startObserver = func(_, id string) {
		n, err := strconv.Atoi(id[strings.LastIndexByte(id, '-')+1:])
		if err != nil {
			panic(fmt.Sprintf("request id %q has no number", id))
		}
		mu.Lock()
		invoked = append(invoked, n)
		mu.Unlock()
	}
	return func() []int {
		mu.Lock()
		defer mu.Unlock()
		got := invoked
		invoked = nil
		return got
	}
}

func ascending(n int) []int {
	want := make([]int, n)
	for i := range want {
		want[i] = i
	}
	return want
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
