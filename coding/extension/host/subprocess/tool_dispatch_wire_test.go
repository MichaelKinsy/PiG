package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
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

// toolCallOrderProbe loads an extension whose tool answers every call with "ok-<n>", n being the argument the call carries.
func toolCallOrderProbe(t *testing.T) (*wireExt, extension.ToolDefinition) {
	t.Helper()
	host := newWireHost(t)
	w := newWireExt(t)
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != "tool_call" {
			return nil
		}
		var args struct {
			N int `json:"n"`
		}
		_ = json.Unmarshal(env.Request.Args, &args)
		return &ResponsePayload{Result: json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":"ok-%d"}]}`, args.N))}
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
	return w, probe
}

func toolResultText(result extension.AgentToolResult) string {
	var parts []string
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// A reservation that never sends its request must not strand the calls behind it. A call whose request cannot be written ends before it
// (here its frame is larger than a frame may be: the encoder refuses it, Conn.encodeEnvelope), and hands its place on as it ends; the next call
// then writes its request and gets its answer. The other ways a call ends before its request (a connection that is closing or closed, a failed
// write, a state push that fails, the grace period ending while a stuck writer holds the call) end in the same deferred release.
func TestSubprocessToolCallOrderIsReleasedWhenACallEndsBeforeItsRequest(t *testing.T) {
	w, probe := toolCallOrderProbe(t)
	first := probe.ReserveCallOrder(json.RawMessage(`{}`))
	second := probe.ReserveCallOrder(json.RawMessage(`{}`))
	tooLarge := json.RawMessage(`"` + strings.Repeat("a", MaxFrameSize) + `"`)
	_, err := probe.Execute(extension.WithCallOrder(t.Context(), first), "call-1", tooLarge, nil)
	if _, ok := errors.AsType[*FrameTooLargeError](err); !ok {
		t.Fatalf("a call whose request cannot be written = %v, want its frame-too-large error", err)
	}
	type outcome struct {
		result extension.AgentToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := probe.Execute(extension.WithCallOrder(t.Context(), second), "call-2", json.RawMessage(`{"n":2}`), nil)
		done <- outcome{result, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || toolResultText(got.result) != "ok-2" {
			t.Fatalf("the second call = %q, %v; want the extension's answer", toolResultText(got.result), got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second call never sent its request: the first call kept its place after it ended")
	}
	if env := w.request("tool_call"); string(env.Request.Args) != `{"n":2}` {
		t.Fatalf("the first request on the wire carries %s: the call that could not be written was sent", env.Request.Args)
	}
}

// agent-loop.ts:831-853 (executePreparedToolCall) calls tool.execute with the run's signal as it is, and the extension tool wrapper adds no abort check
// (tool-definition-wrapper.ts:40-48): a call of a run that had already aborted, which the loop's own gates (agent-loop.ts:623-635, 751, 770) do not stop,
// is the tool's to answer. Such a call keeps the place it reserved: its request is written after the requests of the earlier reservations and before
// those of the later ones, it carries the aborted signal, and its result is what the extension returned.
func TestSubprocessAbortedToolCallTakesItsTurnInOrder(t *testing.T) {
	w, probe := toolCallOrderProbe(t)
	live := probe.ReserveCallOrder(json.RawMessage(`{}`))
	aborted := probe.ReserveCallOrder(json.RawMessage(`{}`))
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	type outcome struct {
		result extension.AgentToolResult
		err    error
	}
	liveDone, abortedDone := make(chan outcome, 1), make(chan outcome, 1)
	// The aborted call, which holds the later place, starts first: it must wait for the earlier place all the same.
	go func() {
		result, err := probe.Execute(extension.WithCallOrder(ended, aborted), "call-2", json.RawMessage(`{"n":2}`), nil)
		abortedDone <- outcome{result, err}
	}()
	select {
	case env := <-w.requests:
		t.Fatalf("a request was written before the earlier reservation's: %s %s", env.Request.Method, env.Request.Args)
	case <-time.After(200 * time.Millisecond):
	}
	go func() {
		result, err := probe.Execute(extension.WithCallOrder(t.Context(), live), "call-1", json.RawMessage(`{"n":1}`), nil)
		liveDone <- outcome{result, err}
	}()
	firstRequest := w.request("tool_call")
	secondRequest := w.request("tool_call")
	if string(firstRequest.Request.Args) != `{"n":1}` || string(secondRequest.Request.Args) != `{"n":2}` {
		t.Fatalf("requests written as %s then %s, want call-1's then call-2's", firstRequest.Request.Args, secondRequest.Request.Args)
	}
	if firstRequest.Request.SignalAborted {
		t.Fatal("the live call's request carries an aborted signal")
	}
	if !secondRequest.Request.SignalAborted {
		t.Fatal("the aborted run's request does not carry the aborted signal")
	}
	for name, done := range map[string]chan outcome{"live": liveDone, "aborted": abortedDone} {
		select {
		case got := <-done:
			want := map[string]string{"live": "ok-1", "aborted": "ok-2"}[name]
			if got.err != nil || toolResultText(got.result) != want {
				t.Fatalf("the %s call = %q, %v; want the extension's answer %q", name, toolResultText(got.result), got.err, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the %s call never ended", name)
		}
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
