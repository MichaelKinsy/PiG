package coding

// Ports packages/coding-agent/src/core/nested-tool-calls.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/usagetotals"
)

// NestedCallLimits are the limits of the nested-call record on a tool result: arguments over the per-call or total size are omitted, calls beyond the count are dropped, and the record is marked incomplete when any of that happens.
//
// upstream: nested-tool-calls.ts:26-31 (NESTED_CALL_LIMITS)
var NestedCallLimits = struct {
	MaxCalls                int
	MaxArgumentBytesPerCall int
	MaxArgumentBytesTotal   int
	MaxErrorChars           int
}{MaxCalls: 256, MaxArgumentBytesPerCall: 8 * 1024, MaxArgumentBytesTotal: 32 * 1024, MaxErrorChars: 500}

// NestedCallSummary is what the nested calls of one model-issued tool call leave on its tool result message.
//
// upstream: nested-tool-calls.ts:36-41 (NestedCallSummary)
type NestedCallSummary struct {
	// Calls becomes `nestedCalls`. Nil when no nested call was made.
	Calls *ai.NestedToolCalls
	// Usage is the summed usage of the nested results, added to the message's usage.
	Usage *ai.Usage
}

// NestedCallRecorder collects the nested calls of one model-issued tool call, including calls made by nested tools. Its snapshot becomes `nestedCalls` on the tool result message. It is safe for concurrent use.
//
// upstream: nested-tool-calls.ts:47-101 (NestedCallRecorder)
type NestedCallRecorder struct {
	mu        sync.Mutex
	calls     []*ai.NestedToolCallRecord
	startedAt map[*ai.NestedToolCallRecord]time.Time
	complete  bool
	argBytes  int
	// usage is the summed usage of every nested result, including calls dropped from the record.
	usage *ai.Usage
}

// NewNestedCallRecorder returns an empty, complete recorder.
func NewNestedCallRecorder() *NestedCallRecorder {
	return &NestedCallRecorder{startedAt: map[*ai.NestedToolCallRecord]time.Time{}, complete: true}
}

// jsonBytesLike is JSON.stringify for the size accounting of upstream's `encoder.encode(JSON.stringify(arguments)).length`: no HTML escaping, the line and paragraph separators written literally, a lone surrogate written as an escape.
func jsonBytesLike(value any) ([]byte, error) {
	return jsstring.MarshalJSON(value)
}

// Start records a call as it starts. It returns nil when the call is dropped.
//
// upstream: nested-tool-calls.ts:56-77
func (r *NestedCallRecorder) Start(toolCall agent.AgentToolCall) *ai.NestedToolCallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) >= NestedCallLimits.MaxCalls {
		r.complete = false
		return nil
	}
	record := &ai.NestedToolCallRecord{ID: toolCall.ID, Name: toolCall.Name, Status: ai.NestedToolCallUnfinished}
	arguments := toolCall.Arguments
	if arguments == nil {
		arguments = ai.JsonObject{}
	}
	encoded, err := jsonBytesLike(arguments)
	if err != nil {
		// upstream: JSON.stringify throws for a value with no JSON form and the call fails before it starts; arguments come from a decoded JSON object, so this is not reachable through a Session.
		encoded = []byte("{}")
	}
	size := len(encoded)
	if size > NestedCallLimits.MaxArgumentBytesPerCall || r.argBytes+size > NestedCallLimits.MaxArgumentBytesTotal {
		record.ArgumentsBytes = &size
		r.complete = false
	} else {
		// The copy is decoded from the arguments in the model's member order; the size above does not depend on it.
		if ordered, err := toolCall.ArgumentsJSON(); err == nil {
			_ = record.SetArgumentsJSON(ordered) // an error leaves the record without arguments, as a failed decode did
		}
		r.argBytes += size
	}
	r.calls = append(r.calls, record)
	r.startedAt[record] = time.Now()
	return record
}

// Finish sets the status, duration and truncated error text of a record Start returned. A nil record is ignored.
//
// upstream: nested-tool-calls.ts:79-85
func (r *NestedCallRecorder) Finish(record *ai.NestedToolCallRecord, isError bool, errorText string) {
	if record == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if isError {
		record.Status = ai.NestedToolCallError
	} else {
		record.Status = ai.NestedToolCallOK
	}
	started, ok := r.startedAt[record]
	if !ok {
		started = time.Now()
	}
	// Math.round(performance.now() - start)
	duration := (time.Since(started) + 500*time.Microsecond).Milliseconds()
	record.DurationMs = &duration
	delete(r.startedAt, record)
	if isError && errorText != "" {
		record.Error = sliceUTF16(errorText, NestedCallLimits.MaxErrorChars)
	}
}

// sliceUTF16 is `text.slice(0, units)`: it counts UTF-16 code units, as JavaScript strings do.
func sliceUTF16(text string, units int) string {
	encoded := utf16.Encode([]rune(text))
	if len(encoded) <= units {
		return text
	}
	return jsstring.FromUTF16(encoded[:units])
}

// AddUsage adds the usage of a nested result, including for calls dropped from the record.
//
// upstream: nested-tool-calls.ts:87-89
func (r *NestedCallRecorder) AddUsage(usage ai.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.usage == nil {
		r.usage = &usage
		return
	}
	combined := usagetotals.CombineUsage(*r.usage, usage)
	r.usage = &combined
}

// TotalUsage returns the summed usage of every nested result, or nil when none reported usage.
//
// upstream: nested-tool-calls.ts:91-93
func (r *NestedCallRecorder) TotalUsage() *ai.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.usage == nil {
		return nil
	}
	return new(*r.usage)
}

// Snapshot returns a copy of the record so far, or nil when no nested call was made.
//
// upstream: nested-tool-calls.ts:95-100
func (r *NestedCallRecorder) Snapshot() *ai.NestedToolCalls {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 && r.complete {
		return nil
	}
	calls := make([]ai.NestedToolCallRecord, len(r.calls))
	complete := r.complete
	for i, call := range r.calls {
		calls[i] = *call
		if call.Status == ai.NestedToolCallUnfinished {
			complete = false
		}
	}
	return &ai.NestedToolCalls{Calls: calls, Complete: complete}
}

// NestedToolCallOptions are the options of one nested call. The call's cancellation is the context passed to [NestedToolCallRunner.Execute].
//
// upstream: nested-tool-calls.ts:103-108 (NestedToolCallOptions)
type NestedToolCallOptions struct {
	// OnUpdate receives partial results of the nested tool, in addition to `tool_execution_update` events. An error is the callback's throw: that update raises no `tool_execution_update` event and the call rejects with the first error.
	OnUpdate agent.ToolUpdateSink
}

// NestedToolCallHost is what the runner needs from its session.
//
// upstream: nested-tool-calls.ts:130-143 (NestedToolCallHost)
type NestedToolCallHost interface {
	// GetTools returns the tools nested calls resolve against.
	GetTools() []agent.AgentTool
	// IsSequential reports whether every nested call runs exclusively, as when the agent executes tool calls sequentially.
	IsSequential() bool
	// RunToolCall runs the call through the tool pipeline, with hooks that report parentToolCallID. onUpdate forwards partial results. It returns onUpdate's first error, as upstream's runToolCall rejects, and then the hooks have not run.
	RunToolCall(ctx context.Context, toolCall agent.AgentToolCall, parentToolCallID string, onUpdate agent.ToolUpdateSink) (agent.AgentToolCallOutcome, error)
	// Emit publishes a `tool_execution_*` event of a nested call. Its ParentToolCallID is set.
	Emit(event agent.AgentEvent)
}

// callScope is shared by the calls below one model-issued call: they share its recorder.
type callScope struct {
	recorder *NestedCallRecorder
	nextID   int
	// holdsQueue is set inside a call that holds the exclusive queue, so its own nested calls do not wait on it.
	holdsQueue bool
}

// NestedToolCallRunner runs the tool calls that a tool makes while it runs (`ctx.executeTool()`), for example from codemode scripts. The agent loop does not know about them: the session runs each one through the agent's tool pipeline with its own hooks, emits `tool_execution_*` events with ParentToolCallID, and records the calls and their usage on the model-issued call's tool result message. Nothing here runs until a tool calls `ctx.executeTool()`.
//
// upstream: nested-tool-calls.ts:160-261 (NestedToolCallRunner)
type NestedToolCallRunner struct {
	host NestedToolCallHost
	mu   sync.Mutex
	// scopes are keyed by the id of the calling tool call.
	scopes map[string]*callScope
	// queueTail is closed when the last exclusive call has finished; it serializes nested calls that must not run concurrently.
	queueTail chan struct{}
}

// NewNestedToolCallRunner returns a runner over host.
func NewNestedToolCallRunner(host NestedToolCallHost) *NestedToolCallRunner {
	settled := make(chan struct{})
	close(settled)
	return &NestedToolCallRunner{host: host, scopes: map[string]*callScope{}, queueTail: settled}
}

func nestedTextOf(result agent.AgentToolResult) string {
	var parts []string
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Execute runs name on behalf of the call callerID. The nested call gets the id `<callerID>/<n>`. It never fails for tool failures: they come back as an outcome with IsError set. It returns the first error of options.OnUpdate, as upstream's promise rejects; the call then records no result and emits no `tool_execution_end`. args must be a JSON object; null or empty arguments are `{}`.
//
// upstream: nested-tool-calls.ts:171-248 (execute)
func (r *NestedToolCallRunner) Execute(ctx context.Context, callerID, name string, args json.RawMessage, options NestedToolCallOptions) (agent.AgentToolCallOutcome, error) {
	// The caller's next call waits for the prefix below (id, start event, reservation),
	// which Pi's single thread runs in call order without any wait.
	defer extension.CallInitiated(ctx)
	r.mu.Lock()
	scope := r.scopes[callerID]
	if scope == nil {
		scope = &callScope{recorder: NewNestedCallRecorder(), nextID: 1}
		r.scopes[callerID] = scope
	}
	id := fmt.Sprintf("%s/%d", callerID, scope.nextID)
	scope.nextID++
	r.mu.Unlock()

	toolCall := agent.AgentToolCall{ID: id, Name: name, Arguments: ai.JsonObject{}}
	argsErr := decodeNestedArguments(args, &toolCall)
	record := scope.recorder.Start(toolCall)
	rawArguments, _ := toolCall.ArgumentsJSON()
	label := ""
	for _, tool := range r.host.GetTools() {
		if tool.Name() == name {
			label = tool.Label()
			break
		}
	}
	r.host.Emit(agent.ToolExecutionStartEvent{ToolCallID: id, ToolName: name, ToolLabel: label, Args: rawArguments, ParentToolCallID: callerID})

	// upstream: agent-loop.ts executeToolCallsParallel: a call reserves its place in a tool's order in call order, before its
	// goroutine runs. The reservation is released however the call ends: a tool that honors the ticket (runQueued, the MCP
	// call lane) releases it itself, and a ticket's Release must run exactly once, so the release below is guarded.
	if argsErr == nil {
		for _, tool := range r.host.GetTools() {
			if tool.Name() != name {
				continue
			}
			if orderer, ok := tool.(agent.QueueOrderable); ok {
				if reserved, has := orderer.ReserveMutationOrder(rawArguments); has {
					ticket := &agent.MutationTicket{Wait: reserved.Wait, Release: sync.OnceFunc(reserved.Release)}
					ctx = agent.WithMutationTicket(ctx, ticket)
					defer func() {
						ticket.Wait()
						ticket.Release()
					}()
				}
			}
			break
		}
	}
	extension.CallInitiated(ctx)

	exclusive := !scope.holdsQueue && r.host.IsSequential()
	if !scope.holdsQueue && !exclusive {
		for _, tool := range r.host.GetTools() {
			if tool.Name() == name {
				exclusive = tool.ExecutionMode() == agent.ToolModeSequential
				break
			}
		}
	}
	var release chan struct{}
	if exclusive {
		release = make(chan struct{})
		r.mu.Lock()
		previous := r.queueTail
		r.queueTail = release
		r.mu.Unlock()
		<-previous
	}
	r.mu.Lock()
	r.scopes[id] = &callScope{recorder: scope.recorder, nextID: 1, holdsQueue: scope.holdsQueue || exclusive}
	r.mu.Unlock()

	var outcome agent.AgentToolCallOutcome
	var runErr error
	func() {
		defer func() {
			r.mu.Lock()
			delete(r.scopes, id)
			r.mu.Unlock()
			if release != nil {
				close(release)
			}
		}()
		if argsErr != nil {
			outcome = agent.AgentToolCallOutcome{ToolCall: toolCall, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: argsErr.Error()}}, Details: map[string]any{}}, IsError: true}
			return
		}
		outcome, runErr = r.host.RunToolCall(ctx, toolCall, callerID, func(partial agent.AgentToolResult) error {
			if options.OnUpdate != nil {
				if err := options.OnUpdate(partial); err != nil {
					return err
				}
			}
			r.host.Emit(agent.ToolExecutionUpdateEvent{ToolCallID: id, ToolName: name, PartialResult: partial, Args: rawArguments, ParentToolCallID: callerID})
			return nil
		})
	}()
	if runErr != nil {
		return agent.AgentToolCallOutcome{}, runErr
	}

	scope.recorder.Finish(record, outcome.IsError, nestedTextOf(outcome.Result))
	// Nested results are not persisted, so their usage is only counted through the recorder.
	if outcome.Result.Usage != nil {
		scope.recorder.AddUsage(*outcome.Result.Usage)
	}
	r.host.Emit(agent.ToolExecutionEndEvent{ToolCallID: id, ToolName: name, Result: outcome.Result, IsError: outcome.IsError, DurationMs: outcome.DurationMs, ParentToolCallID: callerID})
	return outcome, nil
}

// decodeNestedArguments reads `args ?? {}`. A value that is not a JSON object cannot be a tool call's arguments, so the call fails as validation of the pipeline would.
func decodeNestedArguments(args json.RawMessage, into *ai.ToolCall) error {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if err := into.SetArgumentsJSON(trimmed); err != nil {
		return fmt.Errorf("Invalid tool arguments: expected a JSON object")
	}
	return nil
}

// TakeRecord removes and returns the record of the nested calls a model-issued call made, or nil when it made none.
//
// upstream: nested-tool-calls.ts:250-256 (takeRecord)
func (r *NestedToolCallRunner) TakeRecord(toolCallID string) *NestedCallSummary {
	r.mu.Lock()
	scope := r.scopes[toolCallID]
	delete(r.scopes, toolCallID)
	r.mu.Unlock()
	if scope == nil {
		return nil
	}
	return &NestedCallSummary{Calls: scope.recorder.Snapshot(), Usage: scope.recorder.TotalUsage()}
}

// Clear drops every scope. The session calls it when the agent run ends.
//
// upstream: nested-tool-calls.ts:258-260 (clear)
func (r *NestedToolCallRunner) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.scopes)
}
