// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// noBackend serves tools that use only the executor's own operations; any other call fails the test by nil dereference.
type noBackend struct{ driver.ToolBackend }

func toolHost(t *testing.T, core *funcCore, tools map[string]durable.ToolRegistration) *sqlhost.Host {
	t.Helper()
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &driver.Tools{
		Lookup:  func(name string) (durable.ToolRegistration, bool) { r, ok := tools[name]; return r, ok },
		Backend: func(driver.ToolCall) driver.ToolBackend { return noBackend{} },
	}
	host := sqlhost.New(sqlhost.Options{
		Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectTool: runner.Handler()},
	})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return host
}

func runTool(t *testing.T, core *funcCore, host *sqlhost.Host) {
	t.Helper()
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

// openWith makes the core start one tool effect on open.
func openWith(name string, args string, window string) func(abi.Event) *abi.Step {
	return func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventOpen {
			return nil
		}
		p := payload.ToolEffect{TaskID: 7, ConversationID: 3, ToolName: name, CallID: "call-1", Arguments: json.RawMessage(args), Replay: "unsafe"}
		if window != "" {
			p.OutputWindow = json.RawMessage(window)
		}
		return &abi.Step{Effects: []abi.Effect{effect(1, abi.EffectTool, p)}}
	}
}

func tool(execute func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error)) map[string]durable.ToolRegistration {
	return map[string]durable.ToolRegistration{"t": {ToolSchema: ai.ToolSchema{Name: "t"}, Execute: execute}}
}

func done(core *funcCore) (abi.Event, bool) {
	evs := core.events(abi.EventToolDone)
	if len(evs) == 0 {
		return abi.Event{}, false
	}
	return evs[0], true
}

// Pi: packages/durable/src/harness/types.ts:169 (callId).
func TestToolReportsOutputDiagnosticAndResultInOrder(t *testing.T) {
	isError := false
	core := &funcCore{respond: openWith("t", `{"x":1}`, "")}
	host := toolHost(t, core, tool(func(_ context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if m, _ := args.(map[string]any); m["x"] != float64(1) {
			t.Errorf("args = %v", args)
		}
		if api.TaskId() != 7 || api.ConversationId() != 3 || api.CallId() != "call-1" {
			t.Errorf("identity = %v %v %q", api.TaskId(), api.ConversationId(), api.CallId())
		}
		if api.OutputWindow() != nil {
			t.Error("no window was offered")
		}
		api.Output("one ")
		api.Output([]byte("two"))
		api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityWarn, Message: "careful", Code: "w"})
		return durable.ToolExecutionResult{IsError: &isError, Details: map[string]any{"n": 2}, HasDetails: true}, nil
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	var seq []string
	for _, ev := range core.events(abi.EventToolProgress) {
		seq = append(seq, string(rune('0'+ev.Phase))+":"+string(ev.Payload))
	}
	want := []string{`0:one `, `0:two`, `2:{"severity":"warn","message":"careful","code":"w"}`}
	if strings.Join(seq, "|") != strings.Join(want, "|") {
		t.Fatalf("progress = %q, want %q", seq, want)
	}
	ev, _ := done(core)
	if ev.Phase != abi.OutcomeResult || ev.ID != 1 || string(ev.Payload) != `{"isError":false,"details":{"n":2}}` {
		t.Fatalf("tool_done = outcome %d id %d %s", ev.Phase, ev.ID, ev.Payload)
	}
}

// Pi source: packages/durable/src/env/index.ts
// mutation-checked: dropping the reads and writes of ShellOutputSkip.Bytes, ShellOutputSkip.EndsWithNewline, ShellOutputSkip.Newlines, ShellOutputWindow.MaxBytes, ShellOutputWindow.MaxLines fails it
func TestToolOutputSkippingFramesTheSkipBeforeTheChunk(t *testing.T) {
	core := &funcCore{respond: openWith("t", `{}`, `{"maxBytes":10,"maxLines":2}`)}
	host := toolHost(t, core, tool(func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		w := api.OutputWindow()
		if w == nil || w.MaxBytes != 10 || w.MaxLines != 2 {
			t.Errorf("window = %+v", w)
		}
		api.Output("tail", env.ShellOutputSkip{Bytes: 9, Newlines: 2, EndsWithNewline: true})
		api.Output("zero", env.ShellOutputSkip{}) // nothing skipped: a plain chunk
		return durable.ToolExecutionResult{}, nil
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	evs := core.events(abi.EventToolProgress)
	if len(evs) != 2 {
		t.Fatalf("%d progress events", len(evs))
	}
	if evs[0].Skipped != 9 {
		t.Fatalf("header skipped = %d", evs[0].Skipped)
	}
	n := binary.LittleEndian.Uint32(evs[0].Payload)
	if meta, chunk := evs[0].Payload[4:4+n], evs[0].Payload[4+n:]; string(meta) != `{"bytes":9,"newlines":2,"endsWithNewline":true}` || string(chunk) != "tail" {
		t.Fatalf("meta %s chunk %s", meta, chunk)
	}
	if evs[1].Skipped != 0 || string(evs[1].Payload) != "zero" {
		t.Fatalf("a zero skip is a plain chunk: %+v", evs[1])
	}
}

func TestToolFailureIsAThrownOutcomeAndSettledCallsRefuseOperations(t *testing.T) {
	var captured durable.ToolExecutionApi
	core := &funcCore{respond: openWith("t", `{}`, "")}
	host := toolHost(t, core, tool(func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		captured = api
		return durable.ToolExecutionResult{}, errors.New("disk full")
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	ev, _ := done(core)
	if ev.Phase != abi.OutcomeThrown || string(ev.Payload) != `{"name":"Error","message":"disk full"}` {
		t.Fatalf("tool_done = outcome %d %s", ev.Phase, ev.Payload)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(error).Error(), "Tool call call-1 has settled") {
			t.Fatalf("recover = %v", r)
		}
	}()
	captured.Output("late")
}

func TestDetailsWaitsForTheCoresAckAndReportsARejection(t *testing.T) {
	var mu sync.Mutex
	var acked []uint32
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventToolProgress && ev.Phase == 1 {
			mu.Lock()
			acked = append(acked, ev.WaitID)
			n := len(acked)
			mu.Unlock()
			payload := binary.LittleEndian.AppendUint32(nil, ev.WaitID)
			if n == 2 {
				payload = append(payload, 1)
				payload = append(payload, `"stale"`...)
			} else {
				payload = append(payload, 0)
			}
			return &abi.Step{Notices: []abi.Notice{{Kind: abi.NoticeProgressAck, Payload: payload}}}
		}
		return openWith("t", `{}`, "")(ev)
	}}
	var first, second error
	host := toolHost(t, core, tool(func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		first = api.Details(ctx, map[string]any{"step": 1})
		second = api.Details(ctx, map[string]any{"step": 2})
		return durable.ToolExecutionResult{}, nil
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	if first != nil {
		t.Fatalf("first details: %v", first)
	}
	if second == nil || !strings.Contains(second.Error(), `"stale"`) {
		t.Fatalf("a rejected details must fail the wait, got %v", second)
	}
	if len(acked) != 2 || acked[0] == 0 || acked[0] == acked[1] {
		t.Fatalf("wait IDs must be nonzero and distinct: %v", acked)
	}
}

func TestCancellingTheDetailsContextCancelsOnlyTheWait(t *testing.T) {
	core := &funcCore{respond: openWith("t", `{}`, "")} // never acks
	var waitErr error
	host := toolHost(t, core, tool(func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		waitErr = api.Details(waitCtx, 1)
		return durable.ToolExecutionResult{}, nil
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v", waitErr)
	}
	if ev, _ := done(core); ev.Phase != abi.OutcomeResult {
		t.Fatalf("the call itself survives a cancelled wait: outcome %d", ev.Phase)
	}
}

func TestCancelledToolPostsNoCompletion(t *testing.T) {
	started := make(chan struct{})
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventAbort {
			return &abi.Step{Effects: []abi.Effect{{ID: 2, Kind: abi.EffectCancel, Payload: abi.U32Payload(1)}}}
		}
		return openWith("t", `{}`, "")(ev)
	}}
	returned := make(chan error, 1)
	host := toolHost(t, core, tool(func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		close(started)
		<-ctx.Done()
		returned <- ctx.Err()
		return durable.ToolExecutionResult{}, ctx.Err()
	}))
	runTool(t, core, host)
	<-started
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventAbort, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancel never reached the tool")
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok := done(core); ok {
		t.Fatal("a cancelled call must not complete: the core owns its fate")
	}
	if host.Err() != nil {
		t.Fatal(host.Err())
	}
}

func TestToolTheRegistryDoesNotHoldDiscardsTheHost(t *testing.T) {
	core := &funcCore{respond: openWith("ghost", `{}`, "")}
	host := toolHost(t, core, tool(nil))
	runTool(t, core, host)
	waitFor(t, "discard", func() bool { return host.Err() != nil })
	if !strings.Contains(host.Err().Error(), `"ghost"`) {
		t.Fatalf("err = %v", host.Err())
	}
}
