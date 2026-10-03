package sdk

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Upstream agent-loop.ts:707-716 (prepareToolCall): tool.prepareArguments runs before validateToolArguments, so the host (which validates) must be able to
// ask the extension for the prepared arguments. The declaration says the tool has the hook, and a tool_prepare_arguments request runs it.
func TestToolPrepareArgumentsRunsInTheExtensionBeforeTheHostValidates(t *testing.T) {
	ext := New("prepare")
	var executed []map[string]any
	ext.RegisterTool(ToolDefinition{
		Name: "legacy", Label: "legacy", Description: "Echo", Parameters: Schema{"type": "object", "required": []string{"text"}},
		PrepareArguments: func(params map[string]any) (map[string]any, error) {
			if params["fail"] == true {
				return nil, errors.New("prepare exploded")
			}
			return map[string]any{"text": params["legacy"]}, nil
		},
		Execute: func(_ Context, params map[string]any) (any, error) {
			executed = append(executed, params)
			return "ok", nil
		},
	})
	ext.RegisterTool(ToolDefinition{Name: "plain", Label: "plain", Description: "Plain", Parameters: Schema{"type": "object"}, Execute: func(Context, map[string]any) (any, error) { return "ok", nil }})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	var tools []map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, reg.Tools)), &tools); err != nil {
		t.Fatal(err)
	}
	if tools[0]["prepares_arguments"] != true {
		t.Fatalf("legacy declaration = %#v, want prepares_arguments", tools[0])
	}
	if _, present := tools[1]["prepares_arguments"]; present {
		t.Fatalf("plain declaration = %#v, want no prepares_arguments", tools[1])
	}

	_, resp := runSurfaceRequest(t, host, "prepare-1", &requestMsg{Method: "tool_prepare_arguments", Tool: "legacy", Args: json.RawMessage(`{"legacy":"hello"}`)}, nil)
	if resp.Error != nil || string(resp.Result) != `{"text":"hello"}` {
		t.Fatalf("prepared = %s, %+v; want {\"text\":\"hello\"}", resp.Result, resp.Error)
	}
	_, resp = runSurfaceRequest(t, host, "prepare-2", &requestMsg{Method: "tool_prepare_arguments", Tool: "legacy", Args: json.RawMessage(`{"fail":true}`)}, nil)
	if resp.Error == nil || resp.Error.Message != "prepare exploded" {
		t.Fatalf("error = %+v, want the hook's \"prepare exploded\"", resp.Error)
	}
	_, resp = runSurfaceRequest(t, host, "prepare-3", &requestMsg{Method: "tool_prepare_arguments", Tool: "missing", Args: json.RawMessage(`{}`)}, nil)
	if resp.Error == nil {
		t.Fatal("a tool_prepare_arguments request for an unknown tool succeeded")
	}

	// agent-loop.ts:707-716 prepares once, before validation. The host sends tool_call the prepared arguments, so the runtime must not prepare them again.
	_, resp = runSurfaceRequest(t, host, "call-1", &requestMsg{Method: "tool_call", Tool: "legacy", ToolCallID: "c1", Args: json.RawMessage(`{"text":"already prepared","legacy":"again"}`)}, nil)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	want := []map[string]any{{"text": "already prepared", "legacy": "again"}}
	if !reflect.DeepEqual(executed, want) {
		t.Fatalf("tool_call executed with %#v, want the host's arguments %#v", executed, want)
	}
}

// Upstream agent-loop.ts:619-647 (`Promise.all(finalizedCalls.map(entry => entry()))`) and 820-837: every call of a parallel batch reaches tool.execute in
// source order. The host writes the batch's tool_call requests in source order, so the SDK invokes the handlers in the order the requests arrive: a
// request's handler waits for the earlier requests' handlers to be invoked, whatever order the goroutines that carry the requests run in.
func TestToolStartOrderHoldsALaterRequestUntilTheEarlierOnesBegan(t *testing.T) {
	var order toolStartOrder
	first, second, third, fourth := order.reserve(), order.reserve(), order.reserve(), order.reserve()
	began := func(start *toolStart) <-chan struct{} {
		ch := make(chan struct{})
		go func() {
			start.begin()
			close(ch)
		}()
		return ch
	}
	held := func(name string, began <-chan struct{}) {
		t.Helper()
		select {
		case <-began:
			t.Fatalf("%s began before the requests ahead of it", name)
		case <-time.After(50 * time.Millisecond):
		}
	}
	// Goroutines run in the reverse of the arrival order: the last request is waiting first.
	fourthBegan := began(fourth)
	thirdBegan := began(third)
	held("fourth", fourthBegan)
	held("third", thirdBegan)
	first.begin()
	// The first request began, but the second did not: the third still waits for it.
	held("third", thirdBegan)
	second.begin()
	recvStart(t, thirdBegan, "third")
	recvStart(t, fourthBegan, "fourth")
	first.release()

	// A request that ended without reaching its handler hands its place on, once.
	var ended toolStartOrder
	lost, next, last := ended.reserve(), ended.reserve(), ended.reserve()
	nextBegan := began(next)
	lastBegan := began(last)
	held("next", nextBegan)
	lost.release()
	lost.release()
	recvStart(t, nextBegan, "next")
	recvStart(t, lastBegan, "last")

	// A request that ends without its handler still keeps the order of the requests ahead of it: the next handler waits for them.
	var behind toolStartOrder
	ahead, skipped, after := behind.reserve(), behind.reserve(), behind.reserve()
	skippedEnded := make(chan struct{})
	go func() {
		skipped.release()
		close(skippedEnded)
	}()
	afterBegan := began(after)
	held("after", afterBegan)
	held("skipped", skippedEnded)
	ahead.begin()
	recvStart(t, skippedEnded, "skipped")
	recvStart(t, afterBegan, "after")
}

// The dispatch path takes a place for each tool_call request in arrival order and begins it before the handler runs. One processor makes the
// scheduler's choice deterministic (a goroutine that a request starts last runs first, as the unordered loop showed), so the handlers' first
// statements show the order the gate gave. With more processors a thread the operating system preempts between the hand-off and the handler's
// first statement can be overtaken, which no test can pin.
func TestToolCallHandlersStartInArrivalOrder(t *testing.T) {
	const batch, batches = 16, 24
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	ext := New("start-order")
	var mu sync.Mutex
	var started []int
	ext.RegisterTool(ToolDefinition{
		Name: "probe", Label: "probe", Description: "Record the start order", Parameters: Schema{"type": "object"},
		Execute: func(_ Context, params map[string]any) (any, error) {
			mu.Lock()
			started = append(started, int(params["n"].(float64)))
			mu.Unlock()
			return "ok", nil
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	for round := range batches {
		for n := range batch {
			host.writeEnvelope(t, envelope{Type: msgRequest, ID: fmt.Sprintf("r%d-%d", round, n), Request: &requestMsg{Method: "tool_call", Tool: "probe", ToolCallID: "c", Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, n))}})
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

func recvStart(t *testing.T, began <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-began:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never began", name)
	}
}

// A tool_call that ends before its handler (an unknown tool, undecodable arguments) hands its place on, so the next call's handler is not held behind it.
func TestToolCallThatEndsBeforeItsHandlerHandsOnItsPlace(t *testing.T) {
	ext := New("start-release")
	ext.RegisterTool(ToolDefinition{Name: "probe", Label: "probe", Description: "ok", Parameters: Schema{"type": "object"}, Execute: func(Context, map[string]any) (any, error) { return "ok", nil }})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	for round := range 8 {
		_, resp := runSurfaceRequest(t, host, fmt.Sprintf("missing-%d", round), &requestMsg{Method: "tool_call", Tool: "missing", ToolCallID: "c", Args: json.RawMessage(`{}`)}, nil)
		if resp.Error == nil {
			t.Fatalf("round %d: an unknown tool succeeded", round)
		}
		_, resp = runSurfaceRequest(t, host, fmt.Sprintf("broken-%d", round), &requestMsg{Method: "tool_call", Tool: "probe", ToolCallID: "c", Args: json.RawMessage(`"not an object"`)}, nil)
		if resp.Error == nil {
			t.Fatalf("round %d: undecodable arguments succeeded", round)
		}
		_, resp = runSurfaceRequest(t, host, fmt.Sprintf("probe-%d", round), &requestMsg{Method: "tool_call", Tool: "probe", ToolCallID: "c", Args: json.RawMessage(`{}`)}, nil)
		if resp.Error != nil {
			t.Fatalf("round %d: %+v", round, resp.Error)
		}
	}
}
