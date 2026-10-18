// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/output"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// tool_progress kinds (ABI section 6).
const (
	progressOutput     = 0
	progressDetails    = 1
	progressDiagnostic = 2
)

// ToolCall identifies one tool effect.
type ToolCall struct {
	TaskID         durable.TaskId
	ConversationID durable.ConversationId
	CallID         string
	Name           string
}

// ToolBackend is the part of durable.ToolExecutionApi that reads or writes core state: the operations a tool call runs
// against the session. The session that owns the core supplies it per call; the executor supplies the rest (output,
// diagnostics, details, identity).
type ToolBackend interface {
	durable.DocumentObserver
	durable.DocumentReader
	Registry() durable.RegistrySnapshot
	Agent(ctx context.Context) (durable.Agent, error)
	// Models is HarnessOptions.models.
	Models() durable.Models
	// Env is the environment built for this call; nil without one.
	Env() env.ExecutionEnv
	// Now is the Harness clock.
	Now() float64
	Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error)
	Memo(ctx context.Context, name string) (value durable.JsonValue, ok bool, err error)
	MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error)
	CreateTaskErased(ctx context.Context, task durable.AnyTask, input durable.JsonValue, options durable.TaskOptions) (durable.TaskId, error)
	GetTask(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error)
	WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error)
	Conversation(ctx context.Context, id durable.ConversationId) (durable.ConversationHandle, error)
}

// Tools runs tool effects: it finds the registration, gives it an api, and reports progress and the result to the core.
type Tools struct {
	// Lookup returns the registration a tool call executes: the called tool among the conversation's resolved agent's tools (tool.ts call
	// phase), so a later same-name tool replaces an earlier one and wrappers apply. ok is false when the agent has no such tool.
	Lookup func(ctx context.Context, conversation durable.ConversationId, name string) (registration durable.ToolRegistration, ok bool, err error)
	// Backend returns the core-state operations for one call.
	Backend func(call ToolCall) ToolBackend
	waits   atomic.Uint32
}

// Handler returns the sqlhost handler of tool effects.
func (t *Tools) Handler() sqlhost.Handler { return t.run }

func (t *Tools) run(ctx context.Context, call *sqlhost.Call) error {
	var p payload.ToolEffect
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("tool payload: %w", err)
	}
	info := ToolCall{TaskID: durable.TaskId(p.TaskID), ConversationID: durable.ConversationId(p.ConversationID), CallID: p.CallID, Name: p.ToolName}
	registration, ok, err := t.Lookup(ctx, info.ConversationID, p.ToolName)
	if err != nil && ctx.Err() != nil {
		// The core cancelled the effect before the tool ran: it settles the call on the effect's completion, so the end of a
		// cancelled call is reported as a throw, as it is when Execute observes the cancel, not left to a host that says nothing.
		call.Post(abi.Event{Kind: abi.EventToolDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	if err != nil {
		return fmt.Errorf("tool effect %d resolves %q: %w", call.ID, p.ToolName, err)
	}
	if !ok {
		return fmt.Errorf("tool effect %d names %q, which the registry does not hold", call.ID, p.ToolName)
	}
	var args any
	if err := json.Unmarshal(p.Arguments, &args); err != nil {
		return fmt.Errorf("tool %s arguments: %w", p.ToolName, err)
	}
	limits := harness.ToolOutputLimits(&registration)
	api := &toolAPI{ToolBackend: t.Backend(info), call: call, info: info, waits: &t.waits, nested: harness.NewNestedCalls(info.CallID, registration.Replay == durable.ReplaySafe)}
	if len(p.OutputWindow) > 0 {
		var w struct{ MaxBytes, MaxLines int }
		if err := json.Unmarshal(p.OutputWindow, &w); err != nil {
			return fmt.Errorf("tool %s output window: %w", p.ToolName, err)
		}
		api.window = &env.ShellOutputWindow{MaxBytes: w.MaxBytes, MaxLines: w.MaxLines}
		// The core offers a window only for tail retention, with the call's own limits.
		limits.MaxBytes, limits.MaxLines, limits.Retain = w.MaxBytes, w.MaxLines, string(durable.RetainTail)
	}
	api.retained = output.NewOutputBuffer(limits)
	var result durable.ToolExecutionResult
	err = callDefinition(func() (err error) { result, err = registration.Execute(ctx, args, api); return err })
	api.ended.Store(true)
	// No admission starts after the call ended; let those underway commit, so settlement aborts and lists their calls.
	api.nested.End()
	if err != nil {
		call.Post(abi.Event{Kind: abi.EventToolDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	body, err := encodeToolResult(result)
	if err != nil {
		// A value that is not strict JSON faults the call, as the settling commit of tool.ts rejects it; it is not a host failure.
		call.Post(abi.Event{Kind: abi.EventToolDone, ID: call.ID, Phase: abi.OutcomeUnrecordable, Payload: wireError(fmt.Errorf("tool %s result: %w", p.ToolName, err))})
		return nil
	}
	call.Post(abi.Event{Kind: abi.EventToolDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}

func encodeToolResult(r durable.ToolExecutionResult) ([]byte, error) {
	out := struct {
		//portlint:allow emptydrop the reader decodes content into a slice, where an absent list and [] are both empty (hook.go, payload.go)
		Content []ai.ToolResultMessageContent `json:"output,omitempty"`
		IsError *bool                         `json:"isError,omitempty"`
		Details durable.JsonValue             `json:"details,omitempty"`
		//portlint:allow emptydrop the reader decodes diagnostics into a slice, where an absent list and [] are both empty (hook.go)
		Diagnostics []durable.ToolDiagnostic `json:"diagnostics,omitempty"`
		Usage       *ai.Usage                `json:"usage,omitempty"`
		Control     *durable.ToolControl     `json:"control,omitempty"`
		// StructuredOutput is for programs that call the tool; a present null is a value.
		StructuredOutput *durable.JsonValue `json:"structuredOutput,omitempty"`
	}{Content: r.Output, IsError: r.IsError, Diagnostics: r.Diagnostics, Usage: r.Usage, Control: r.Control}
	if r.HasDetails {
		out.Details = r.Details
	}
	if r.HasStructuredOutput {
		out.StructuredOutput = &r.StructuredOutput
	}
	return json.Marshal(out)
}

// toolAPI is the durable.ToolExecutionApi of one call. Its progress goes to the core as tool_progress events; every
// operation panics once the call settled, as the api of upstream's tool.ts throws.
type toolAPI struct {
	ToolBackend
	call   *sqlhost.Call
	info   ToolCall
	window *env.ShellOutputWindow
	ended  atomic.Bool
	// retained mirrors the output buffer the core keeps for the call, so RetainedOutput answers without a round trip.
	mu       sync.Mutex
	retained *output.OutputBuffer
	waits    *atomic.Uint32 // the host's details wait IDs: nonzero, unique for the life of the handle
	nested   *harness.NestedCalls
}

var _ durable.ToolExecutionApi = (*toolAPI)(nil)

func (a *toolAPI) live() {
	if a.ended.Load() {
		panic(fmt.Errorf("Tool call %s has settled", a.info.CallID))
	}
}

func (a *toolAPI) TaskId() durable.TaskId                 { return a.info.TaskID }
func (a *toolAPI) ConversationId() durable.ConversationId { return a.info.ConversationID }
func (a *toolAPI) CallId() string                         { return a.info.CallID }
func (a *toolAPI) OutputWindow() *env.ShellOutputWindow   { return a.window }

func (a *toolAPI) Output(chunk any, skipped ...env.ShellOutputSkip) {
	if len(skipped) == 0 {
		a.output(chunk, nil)
		return
	}
	a.output(chunk, &skipped[0])
}

// output sends a chunk. A skip travels as a length-prefixed meta object before the chunk (host/PROTOCOL.md), with the
// byte count also in the event header.
func (a *toolAPI) output(chunk any, skipped *env.ShellOutputSkip) {
	a.live()
	var data []byte
	switch c := chunk.(type) {
	case string:
		data = []byte(c)
	case []byte:
		data = c
	default:
		panic(fmt.Errorf("tool output chunk is %T, not a string or bytes", chunk))
	}
	a.retain(data, skipped)
	ev := abi.Event{Kind: abi.EventToolProgress, ID: a.call.ID, Phase: progressOutput, Payload: data}
	if skipped != nil && skipped.Bytes != 0 {
		meta, _ := json.Marshal(struct {
			Bytes           int  `json:"bytes"`
			Newlines        int  `json:"newlines"`
			EndsWithNewline bool `json:"endsWithNewline"`
		}{skipped.Bytes, skipped.Newlines, skipped.EndsWithNewline})
		framed := binary.LittleEndian.AppendUint32(nil, uint32(len(meta)))
		framed = append(append(framed, meta...), data...)
		ev.Skipped, ev.Payload = uint32(skipped.Bytes), framed
	}
	a.call.Post(ev)
}

// ExecuteTool calls another tool as a nested call: the host admits it in one commit and the core runs it as a pi.tool task
// of kind nested owned by this call.
func (a *toolAPI) ExecuteTool(ctx context.Context, name string, args map[string]any, options ...durable.NestedCallOptions) (durable.NestedToolExecutionResult, error) {
	return a.nested.Execute(ctx, nestedCaller{a}, name, args, options...)
}

// nestedCaller lends a call's backend to nested-call admission.
type nestedCaller struct{ *toolAPI }

// CommitFenced runs change in one commit that fails once the calling task is aborted or settled, as a task runtime's
// commit does.
func (c nestedCaller) CommitFenced(ctx context.Context, change func(tx durable.Tx) error) error {
	_, err := c.Commit(ctx, func(tx durable.Tx) (any, error) {
		record, err := tx.Task(c.info.TaskID)
		if err != nil {
			return nil, err
		}
		if record == nil || record.State.Status == durable.TaskTerminal {
			return nil, fmt.Errorf("Tool call %s has settled", c.info.CallID)
		}
		if record.AbortRequested {
			return nil, fmt.Errorf("Tool call %s has an abort mark", c.info.CallID)
		}
		return nil, change(tx)
	})
	return err
}

// retain pushes a chunk into the retained view, as the core's buffer takes it (tool.ts output: skipped text first).
func (a *toolAPI) retain(data []byte, skipped *env.ShellOutputSkip) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var err error
	if skipped == nil || skipped.Bytes == 0 {
		a.retained.PushBytes(data)
	} else {
		_, err = a.retained.PushBytesSkipping(data, output.OutputSkip(*skipped))
	}
	if err != nil {
		panic(err)
	}
}

// RetainedOutput is the output retained so far and whether earlier output was dropped.
func (a *toolAPI) RetainedOutput() (string, bool) {
	a.live()
	a.mu.Lock()
	defer a.mu.Unlock()
	retained := a.retained.Snapshot()
	return retained.Text, retained.DroppedBytes > 0
}

func (a *toolAPI) Diagnostic(d durable.ToolDiagnostic) {
	a.live()
	body, _ := json.Marshal(d)
	a.call.Post(abi.Event{Kind: abi.EventToolProgress, ID: a.call.ID, Phase: progressDiagnostic, Payload: body})
}

// Details sends the value and waits for the core's progress_ack. Cancelling ctx cancels only the wait.
func (a *toolAPI) Details(ctx context.Context, value durable.JsonValue) error {
	a.live()
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	waitID := a.waits.Add(1)
	expect, err := a.call.Expect(func(n abi.Notice) bool {
		return n.Kind == abi.NoticeProgressAck && len(n.Payload) >= 4 && binary.LittleEndian.Uint32(n.Payload) == waitID
	})
	if err != nil {
		return err
	}
	a.call.Post(abi.Event{Kind: abi.EventToolProgress, ID: a.call.ID, Phase: progressDetails, WaitID: waitID, Payload: body})
	notice, err := expect.Wait(ctx)
	if err != nil {
		return err
	}
	if len(notice.Payload) >= 5 && notice.Payload[4] != 0 {
		return fmt.Errorf("details were rejected: %s", notice.Payload[5:])
	}
	return nil
}
