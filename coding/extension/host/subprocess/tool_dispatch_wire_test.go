package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream agent-loop.ts:707-716 (prepareToolCall) calls tool.prepareArguments and only then validateToolArguments, so a
// tool that defines prepareArguments must be reachable from the host before it validates. The extension runs the hook
// (types.ts:584 `prepareArguments?: (args: unknown) => Static<TParams>`) and the host gets the rewritten arguments back.
func TestPrepareArgumentsRunsInTheExtension(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	prepared := `{"text":"hello"}`
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestPrepareArguments {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(prepared)}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "prepare", Tools: []ToolDecl{
		{Name: "legacy", Description: "Echo", Parameters: json.RawMessage(`{"type":"object","required":["text"]}`), PreparesArguments: true},
		{Name: "plain", Description: "Plain", Parameters: json.RawMessage(`{"type":"object"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if ext.Tools["plain"].Definition.PrepareArguments != nil {
		t.Fatal("a tool that does not declare prepares_arguments has a PrepareArguments")
	}
	legacy := ext.Tools["legacy"].Definition
	if legacy.PrepareArguments == nil {
		t.Fatal("a tool that declares prepares_arguments has no PrepareArguments")
	}
	got, err := legacy.PrepareArguments(json.RawMessage(`{"legacy":"hello"}`))
	if err != nil || string(got) != prepared {
		t.Fatalf("prepared = %s, %v; want %s", got, err, prepared)
	}
	env := w.request(RequestPrepareArguments)
	if env.Request.Tool != "legacy" || string(env.Request.Args) != `{"legacy":"hello"}` {
		t.Fatalf("request = tool %q args %s", env.Request.Tool, env.Request.Args)
	}

	// agent-loop.ts:707-716: a prepareArguments that throws is caught by prepareToolCall and becomes the tool's error result with the thrown message.
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestPrepareArguments {
			return nil
		}
		return &ResponsePayload{Error: &ErrorInfo{Message: "prepare exploded"}}
	}
	if _, err := legacy.PrepareArguments(json.RawMessage(`{}`)); err == nil || err.Error() != "prepare exploded" {
		t.Fatalf("error = %v, want the extension's \"prepare exploded\"", err)
	}
}

// Upstream agent-loop.ts:619-647 and 820-837 (executeToolCallsParallel, executePreparedToolCall): `Promise.all(finalizedCalls.map(entry => entry()))`
// invokes every call in source order and each call reaches tool.execute synchronously, so the calls of a batch start in source order. A subprocess
// tool starts when its request reaches the runtime, so the host must write the requests of a batch in source order even when the goroutines that
// carry them run in another order.
func TestSubprocessToolCallsLeaveTheHostInCallOrder(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != "tool_call" {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(`{"content":"ok"}`)}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "order", Tools: []ToolDecl{
		{Name: "probe", Description: "Probe", Parameters: json.RawMessage(`{"type":"object"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probe := ext.Tools["probe"].Definition
	if probe.ReserveCallOrder == nil {
		t.Fatal("a subprocess tool keeps no call order: its requests leave in goroutine order")
	}
	const n = 8
	orders := make([]*extension.CallOrder, n)
	for i := range n {
		orders[i] = probe.ReserveCallOrder(json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)))
		if orders[i] == nil {
			t.Fatalf("call %d reserved no place", i)
		}
	}
	var wg sync.WaitGroup
	for i := n - 1; i >= 0; i-- {
		wg.Go(func() {
			ctx := extension.WithCallOrder(t.Context(), orders[i])
			if _, err := probe.Execute(ctx, fmt.Sprintf("call-%d", i), json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)), nil); err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	for i := range n {
		env := w.request("tool_call")
		if want := fmt.Sprintf(`{"n":%d}`, i); string(env.Request.Args) != want {
			t.Fatalf("request %d carries %s, want %s: the batch left the host out of source order", i, env.Request.Args, want)
		}
	}
}

// A reservation that never sends its request must not strand the calls behind it: the call's own context may end first
// (agent-loop.ts:622-629 answers "Operation aborted" without calling execute), and a request that fails to write ends the call.
func TestSubprocessToolCallOrderIsReleasedWhenACallEndsBeforeItsRequest(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != "tool_call" {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(`{"content":"ok"}`)}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "release", Tools: []ToolDecl{
		{Name: "probe", Description: "Probe", Parameters: json.RawMessage(`{"type":"object"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probe := ext.Tools["probe"].Definition
	if probe.ReserveCallOrder == nil {
		t.Fatal("a subprocess tool keeps no call order")
	}
	first := probe.ReserveCallOrder(json.RawMessage(`{}`))
	second := probe.ReserveCallOrder(json.RawMessage(`{}`))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := probe.Execute(extension.WithCallOrder(cancelled, first), "call-1", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("a call with a cancelled context succeeded")
	}
	done := make(chan error, 1)
	go func() {
		_, err := probe.Execute(extension.WithCallOrder(t.Context(), second), "call-2", json.RawMessage(`{}`), nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second call never sent its request: the first call kept its place after it ended")
	}
}

// agent-loop.ts:619-647: the calls of a batch run concurrently once started (Promise.all). A call hands its place in the order on when its request is
// written, not when its tool finishes, so a tool that waits for the rest of the batch to start (a barrier) does not hold the batch up.
func TestSubprocessToolCallsRunConcurrentlyOnceTheirRequestsLeave(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	const n = 4
	var arrived sync.WaitGroup
	arrived.Add(n)
	allArrived := make(chan struct{})
	go func() {
		arrived.Wait()
		close(allArrived)
	}()
	w.handle = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != "tool_call" {
			return nil
		}
		arrived.Done()
		select {
		case <-allArrived:
			return &ResponsePayload{Result: json.RawMessage(`{"content":"ok"}`)}
		case <-time.After(10 * time.Second):
			return &ResponsePayload{Error: &ErrorInfo{Message: "the batch never started together"}}
		}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "barrier", Tools: []ToolDecl{
		{Name: "probe", Description: "Probe", Parameters: json.RawMessage(`{"type":"object"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probe := ext.Tools["probe"].Definition
	if probe.ReserveCallOrder == nil {
		t.Fatal("a subprocess tool keeps no call order")
	}
	orders := make([]*extension.CallOrder, n)
	for i := range n {
		orders[i] = probe.ReserveCallOrder(json.RawMessage(`{}`))
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			if _, err := probe.Execute(extension.WithCallOrder(t.Context(), orders[i]), fmt.Sprintf("call-%d", i), json.RawMessage(`{}`), nil); err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		})
	}
	wg.Wait()
}
