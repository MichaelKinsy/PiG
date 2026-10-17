package codemode_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Ports packages/codemode/test/portable.test.ts, describe("RemoteCodemodeSandbox") (Pi 1.2.0), with its scripts and
// expectations, against a real VM.

// jsonClone is structuredClone: the value that arrives shares nothing with the one that was sent. A value that does not
// survive JSON is a bug in the protocol types and panics, naming the type.
func jsonClone[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("cloning %T: %v", v, err))
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		panic(fmt.Sprintf("cloning %T from %s: %v", v, data, err))
	}
	return out
}

// clonedRemote is an in-process remote that records what crosses the boundary, cloned as Workers RPC would (portable.test.ts
// recordingRemote). Execute returns once the VM has stopped, which is when upstream's `served` promise settles.
type clonedRemote struct {
	serve codemode.ServeOptions
	// mu and exchanges are shared by every copy: the sizes of the batches the remote sent.
	rec *recording
}

type recording struct {
	mu        sync.Mutex
	exchanges []int
	served    sync.WaitGroup
}

func (r *recording) batches() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.exchanges...)
}

func (r clonedRemote) Execute(ctx context.Context, request codemode.RemoteRequest, exchange codemode.RemoteExchange) error {
	if r.rec != nil {
		r.rec.served.Add(1)
		defer r.rec.served.Done()
	}
	codemode.ServeRemote(ctx, jsonClone(request), func(ctx context.Context, messages []codemode.RemoteMessage, reply func(*codemode.RemoteReply, error)) {
		if r.rec != nil {
			r.rec.mu.Lock()
			r.rec.exchanges = append(r.rec.exchanges, len(messages))
			r.rec.mu.Unlock()
		}
		exchange(ctx, jsonClone(messages), func(answer *codemode.RemoteReply, err error) {
			if answer != nil {
				cloned := jsonClone(*answer)
				answer = &cloned
			}
			reply(answer, err)
		})
	}, r.serve)
	return nil
}

func newRemoteSandbox(t *testing.T, options codemode.RemoteSandboxOptions, remote codemode.Remote) *codemode.RemoteSandbox {
	t.Helper()
	options.Remote = func() (codemode.Remote, error) { return remote, nil }
	sandbox, err := codemode.NewRemoteSandbox(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	return sandbox
}

// Pi 1.2.0 packages/codemode/test/portable.test.ts:61 describe("RemoteCodemodeSandbox") "runs tools in the caller and the script on the remote"
func TestRemoteSandboxRunsToolsInTheCallerAndTheScriptOnTheRemote(t *testing.T) {
	rec := &recording{}
	var seen []json.RawMessage
	var mu sync.Mutex
	add := codemode.Tool{Name: "add", Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		seen = append(seen, args)
		mu.Unlock()
		var a struct{ A int }
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return json.Marshal(a.A + 1)
	}}
	sandbox := newRemoteSandbox(t, codemode.RemoteSandboxOptions{Tools: []codemode.Tool{add}}, clonedRemote{rec: rec})
	result := run(t, sandbox.Sandbox, "text('hi'); return await tools.add({ a: 1 })", codemode.ExecuteOptions{Store: map[string]json.RawMessage{"k": js("1")}})
	if !result.OK {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	sameJSON(t, "value", result.Value, "2")
	if len(result.Output) != 1 || result.Output[0] != (codemode.OutputItem{Type: codemode.OutputItemText, Text: "hi"}) {
		t.Errorf("output = %+v", result.Output)
	}
	if len(seen) != 1 || string(seen[0]) != `{"a":1}` {
		t.Errorf("tool saw %s, want [{a:1}]", seen)
	}
	if len(result.Calls) != 1 || result.Calls[0].Name != "add" || result.Calls[0].Status != codemode.CallOK {
		t.Errorf("calls = %+v", result.Calls)
	}
	rec.served.Wait()
}

// Pi 1.2.0 packages/codemode/test/portable.test.ts:83 describe("RemoteCodemodeSandbox") "batches output instead of making one exchange per line"
func TestRemoteSandboxBatchesOutputInsteadOfMakingOneExchangePerLine(t *testing.T) {
	rec := &recording{}
	sandbox := newRemoteSandbox(t, codemode.RemoteSandboxOptions{}, clonedRemote{rec: rec})
	result := run(t, sandbox.Sandbox, "for (let i = 0; i < 5000; i++) console.log(i); return 'ok'")
	if !result.OK {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	sameJSON(t, "value", result.Value, `"ok"`)
	if len(result.Output) != 5000 {
		t.Fatalf("%d output items, want 5000", len(result.Output))
	}
	if last := result.Output[4999]; last != (codemode.OutputItem{Type: codemode.OutputItemText, Text: "4999", Console: true}) {
		t.Errorf("last item = %+v", last)
	}
	if n := len(rec.batches()); n > 6 {
		t.Errorf("%d exchanges, want at most 6", n)
	}
	rec.served.Wait()
}

// Pi 1.2.0 packages/codemode/test/portable.test.ts:93 describe("RemoteCodemodeSandbox") "caps the interrupt budget and memory at the remote's limits"
func TestRemoteSandboxCapsTheInterruptBudgetAndMemoryAtTheRemotesLimits(t *testing.T) {
	remote := clonedRemote{serve: codemode.ServeOptions{MaxInterruptBudget: 1_000, MaxMemoryLimitBytes: 8 * 1024 * 1024}}
	sandbox := newRemoteSandbox(t, codemode.RemoteSandboxOptions{InterruptBudget: 1_000_000_000}, remote)
	result := run(t, sandbox.Sandbox, "while (true) {}")
	if e := wantFailure(t, result, codemode.ErrorTimeout); e.Message != "Execution exceeded its interrupt budget of 1000" {
		t.Errorf("message = %q", e.Message)
	}
	memory := run(t, sandbox.Sandbox, "const parts = []; for (;;) parts.push('x'.repeat(1 << 20) + parts.length);")
	if memory.OK {
		t.Fatalf("result = %+v, want a failure", memory)
	}
	if text, _ := json.Marshal(memory.Error); !strings.Contains(string(text), "out of memory") {
		t.Errorf("error = %s, want an out of memory failure", text)
	}
}

// The remote's pending exchange is answered with "stop", so its VM is disposed and Execute returns.
// Pi 1.2.0 packages/codemode/test/portable.test.ts:107 describe("RemoteCodemodeSandbox") "tells the remote to stop when the caller aborts"
func TestRemoteSandboxTellsTheRemoteToStopWhenTheCallerAborts(t *testing.T) {
	rec := &recording{}
	started := make(chan struct{})
	hang := codemode.Tool{Name: "hang", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	sandbox := newRemoteSandbox(t, codemode.RemoteSandboxOptions{Tools: []codemode.Tool{hang}}, clonedRemote{rec: rec})
	ctx, abort := context.WithCancel(t.Context())
	go func() {
		<-started
		abort()
	}()
	result, err := sandbox.Execute(ctx, "await tools.hang(); return 'never'", codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantFailure(t, result, codemode.ErrorAborted)
	rec.served.Wait()
}

// The client going away ends a remote whose script is computing, instead of letting it run to its budget (ServeRemote): the
// script computes after its first call is answered, with a budget it would not use up in the test's lifetime.
func TestServeRemoteStopsAComputingScriptWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := codemode.RemoteRequest{Data: codemode.VmStartData{
		Code:  "await tools.go(); while (true) {}",
		Tools: []codemode.VmStartTool{{Name: "go", JSName: "go"}},
	}, InterruptBudget: 1_000_000_000}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		codemode.ServeRemote(ctx, request, func(_ context.Context, messages []codemode.RemoteMessage, reply func(*codemode.RemoteReply, error)) {
			last := messages[len(messages)-1]
			if last.Type != codemode.MessageCall {
				reply(nil, nil)
				return
			}
			go func() {
				// Answer the call, then end the context while the script computes.
				reply(&codemode.RemoteReply{HostToWorkerMessage: codemode.HostToWorkerMessage{Type: codemode.MessageResult, ID: last.ID, Ok: true}}, nil)
				cancel()
			}()
		}, codemode.ServeOptions{MaxInterruptBudget: 1_000_000_000})
	}()
	select {
	case <-returned:
	case <-time.After(20 * time.Second):
		t.Fatal("ServeRemote kept computing after its context ended")
	}
}

// sandbox.test.ts:829-836 on the served VM (CodemodeServeOptions.wasm, packages/codemode/src/runtime/remote.ts:92): the loader supplies
// the module the served VM runs on, and a loader that fails ends the execution with the sandbox failure Failed to load QuickJS.
func TestServeRemoteUsesItsWasmLoader(t *testing.T) {
	loads := 0
	good := clonedRemote{serve: codemode.ServeOptions{Wasm: func() ([]byte, error) { loads++; return codemode.QuickJSWasm(), nil }}}
	sandbox := newRemoteSandbox(t, codemode.RemoteSandboxOptions{}, good)
	result := run(t, sandbox.Sandbox, "return 6 * 7", codemode.ExecuteOptions{})
	if !result.OK {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	sameJSON(t, "value", result.Value, "42")
	if loads != 1 {
		t.Fatalf("the loader ran %d times, want 1", loads)
	}
	bad := clonedRemote{serve: codemode.ServeOptions{Wasm: func() ([]byte, error) { return nil, errors.New("no wasm") }}}
	failing := newRemoteSandbox(t, codemode.RemoteSandboxOptions{}, bad)
	result = run(t, failing.Sandbox, "return 1", codemode.ExecuteOptions{})
	if result.OK || result.Error == nil || result.Error.Kind != codemode.ErrorSandbox || !strings.Contains(result.Error.Message, "Failed to load QuickJS: no wasm") {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
}

// A client written by hand against the Remote interface, as a TypeScript client of the Durable Object is (portable.test.ts "RemoteCodemodeSandbox"
// drives the same Remote.execute with its own exchange). Pi: packages/codemode/src/runtime/remote.ts:73-74 (CodemodeRemote.execute),
// :52-55 (CodemodeRemoteRequest.data and interruptBudget) and :62-67 (the exchange). The remote reports the script's output and its tool
// call through the exchange in order and returns once the terminal message has been delivered. The budget the request names is
// asserted by TestTheRequestIsCappedAtTheRemotesLimits.
func TestARemoteExecutedByHandReportsTheScriptThroughTheExchange(t *testing.T) {
	var remote codemode.Remote = clonedRemote{}
	request := codemode.RemoteRequest{
		Data:            codemode.VmStartData{Code: "text('hi'); return await tools.add({ a: 1 })", Tools: []codemode.VmStartTool{{Name: "add", JSName: "add"}}, Globals: []codemode.VmStartGlobal{}, Store: map[string]string{}},
		InterruptBudget: 100_000,
	}
	var mu sync.Mutex
	var types []string
	var done *codemode.RemoteMessage
	exchange := func(_ context.Context, messages []codemode.RemoteMessage, reply func(*codemode.RemoteReply, error)) {
		mu.Lock()
		defer mu.Unlock()
		var answer *codemode.RemoteReply
		for i, m := range messages {
			types = append(types, m.Type)
			switch m.Type {
			case codemode.MessageCall:
				result := "2"
				answer = &codemode.RemoteReply{HostToWorkerMessage: codemode.HostToWorkerMessage{Type: codemode.MessageResult, ID: m.ID, Ok: true, Payload: &result}}
			case codemode.MessageDone:
				done = &messages[i]
			}
		}
		reply(answer, nil)
	}
	if err := remote.Execute(t.Context(), request, exchange); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(types, ",") != "output,call,done" {
		t.Fatalf("messages = %v, want output, call, done in order", types)
	}
	if done == nil || !done.Ok || done.Value == nil || *done.Value != "2" {
		t.Fatalf("done = %+v", done)
	}
}

// The tool table and the lifecycle of CodemodeSandboxBase (packages/codemode/src/runtime/execution.ts:363-370) are what RemoteCodemodeSandbox
// (packages/codemode/src/runtime/remote.ts:268) inherits: the tool registered after construction runs in the caller, an unregistered one is
// gone, and close() rejects later executions.
func TestRemoteSandboxHasTheToolTableOfTheBase(t *testing.T) {
	sandbox := newRemoteSandbox(t, codemode.RemoteSandboxOptions{}, clonedRemote{})
	echo := codemode.Tool{Name: "echo", Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) { return args, nil }}
	// The promoted methods are those of the RemoteSandbox type itself, as the base class members are those of the subclass.
	if err := (*codemode.RemoteSandbox).RegisterTool(sandbox, echo); err != nil {
		t.Fatal(err)
	}
	result := run(t, sandbox.Sandbox, "return await tools.echo({ n: 1 })", codemode.ExecuteOptions{})
	if !result.OK {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	sameJSON(t, "value", result.Value, `{"n":1}`)
	if !sandbox.UnregisterTool("echo") || len(sandbox.Tools()) != 0 {
		t.Fatal("echo was not unregistered")
	}
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	// execution.ts:416: execute() on a closed sandbox rejects with Error("Sandbox is closed").
	if result, err := sandbox.Execute(t.Context(), "return 1", codemode.ExecuteOptions{}); err == nil || err.Error() != "Sandbox is closed" {
		t.Fatalf("a closed sandbox answered %+v, err = %v, want the error Sandbox is closed", result, err)
	}
}
