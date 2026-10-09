package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Tool-call execution for one assistant message. Mirrors upstream
// packages/agent/src/agent-loop.ts executeToolCalls and its helpers.

// executedToolCallBatch is the outcome of one assistant message's tool calls.
type executedToolCallBatch struct {
	messages  []ToolResultMessage
	terminate bool
}

// preparedToolCall is a call that passed lookup, argument preparation,
// validation, and the before hooks, and may execute.
type preparedToolCall struct {
	call pendingToolCall
	tool AgentTool
	args json.RawMessage
}

// finalizedToolCall is a call's final result. executed is false for calls
// that never ran (unknown tool, invalid arguments, blocked, aborted).
type finalizedToolCall struct {
	call     pendingToolCall
	result   AgentToolResult
	isError  bool
	executed bool
	duration time.Duration
}

// toolCtx is the context tools and tool hooks run under: the run's context
// carrying the session state tools may export.
func (r *loopRun) toolCtx() context.Context {
	ctx := WithToolEnvironment(r.ctx, r.h.toolEnvironment(r.model, r.thinking))
	return ctx
}

// executeToolCalls runs a batch sequentially when the loop is configured for
// sequential execution or any call targets a tool whose ExecutionMode is
// sequential; otherwise in parallel.
func (r *loopRun) executeToolCalls(assistant *AssistantMessage, calls []pendingToolCall) executedToolCallBatch {
	ctx := WithToolCallHookContext(r.toolCtx(), r.toolCallHookContext(assistant))
	if r.h.cfg.ToolExecution == ToolModeSequential || r.hasSequentialToolCall(calls) {
		return r.executeToolCallsSequential(ctx, calls)
	}
	return r.executeToolCallsParallel(ctx, calls)
}

func (r *loopRun) hasSequentialToolCall(calls []pendingToolCall) bool {
	for _, call := range calls {
		if tool := r.h.findTool(call.name); tool != nil && tool.ExecutionMode() == ToolModeSequential {
			return true
		}
	}
	return false
}

// executeToolCallsSequential prepares, executes, and finalizes each call
// before the next one starts, emitting each call's tool-result message as soon
// as the call is finalized.
func (r *loopRun) executeToolCallsSequential(ctx context.Context, calls []pendingToolCall) executedToolCallBatch {
	finalized := make([]finalizedToolCall, 0, len(calls))
	messages := make([]ToolResultMessage, 0, len(calls))
	for _, call := range calls {
		r.h.emitToolExecutionStart(call)
		var f finalizedToolCall
		if outcome := r.h.prepareToolCall(ctx, call); outcome.prepared != nil {
			executed, err := r.h.executePreparedToolCall(ctx, *outcome.prepared)
			if err != nil {
				panic(runFailure{err: err})
			}
			f = r.h.finalizeExecutedToolCall(ctx, *outcome.prepared, executed)
		} else {
			f = *outcome.finalized
		}
		r.h.emitToolExecutionEnd(f)
		messages = append(messages, r.appendToolResult(f))
		finalized = append(finalized, f)
		if ctx.Err() != nil {
			break
		}
	}
	return executedToolCallBatch{messages: messages, terminate: shouldTerminateToolBatch(finalized)}
}

// executeToolCallsParallel emits tool_execution_start and prepares every call
// sequentially in source order, then executes the prepared calls
// concurrently. tool_execution_end follows completion order; tool-result
// messages follow source order once every call has finished (Promise.all).
//
// A tool implementing QueueOrderable has its shared-queue position reserved
// here too, in this same source-order loop, before any goroutine starts:
// upstream's Promise.all(calls.map(...)) runs each call's synchronous
// prefix — including a file-mutation-queue registration — in call order,
// before any call's async work begins, and Go's per-call goroutines below
// have no equivalent guarantee on their own (see QueueOrderable).
func (r *loopRun) executeToolCallsParallel(ctx context.Context, calls []pendingToolCall) executedToolCallBatch {
	outcomes := make([]toolCallOutcome, 0, len(calls))
	callCtxs := make([]context.Context, 0, len(calls))
	for _, call := range calls {
		r.h.emitToolExecutionStart(call)
		outcome := r.h.prepareToolCall(ctx, call)
		callCtx := ctx
		if outcome.prepared != nil {
			if orderer, ok := outcome.prepared.tool.(QueueOrderable); ok {
				if ticket, has := orderer.ReserveMutationOrder(outcome.prepared.args); has {
					callCtx = WithMutationTicket(ctx, ticket)
				}
			}
		}
		if outcome.finalized != nil {
			r.h.emitToolExecutionEnd(*outcome.finalized)
		}
		outcomes = append(outcomes, outcome)
		callCtxs = append(callCtxs, callCtx)
		if ctx.Err() != nil {
			break
		}
	}

	finalized := make([]finalizedToolCall, len(outcomes))
	var wg sync.WaitGroup
	var failureMu sync.Mutex
	var failure error
	for i, outcome := range outcomes {
		if outcome.prepared == nil {
			finalized[i] = *outcome.finalized
			continue
		}
		callCtx := callCtxs[i]
		// Pi awaits Promise.all over every prepared call, so all calls run at
		// once. A CPU-count cap would serialize I/O-bound tools on small machines.
		wg.Go(func() {
			var err error
			finalized[i], err = r.h.runPreparedToolCall(callCtx, *outcome.prepared)
			if err != nil {
				failureMu.Lock()
				if failure == nil {
					failure = err
				}
				failureMu.Unlock()
			}
		})
	}
	wg.Wait()
	// Promise.all rejects with the first rejected call; the run fails on its own goroutine once every call has settled.
	if failure != nil {
		panic(runFailure{err: failure})
	}

	messages := make([]ToolResultMessage, 0, len(finalized))
	for _, f := range finalized {
		messages = append(messages, r.appendToolResult(f))
	}
	return executedToolCallBatch{messages: messages, terminate: shouldTerminateToolBatch(finalized)}
}

// runPreparedToolCall executes and finalizes one call of a parallel batch,
// then emits its tool_execution_end. It runs on its own goroutine, so a sink
// failure is returned for the run to raise.
func (h *loopHost) runPreparedToolCall(ctx context.Context, prepared preparedToolCall) (finalizedToolCall, error) {
	var finalized finalizedToolCall
	if ctx.Err() != nil {
		// This call never reaches Execute, so a mutation ticket reserved for
		// it (executeToolCallsParallel's ReserveMutationOrder) must still be
		// retired here, or the chain's tail stays open and strands every
		// later same-path call forever. Upstream has no equivalent branch:
		// its registration happens inside tool.execute itself
		// (agent-loop.ts:616-623 aborts before that call), so an aborted
		// call never registers a file mutation in the first place. Wait
		// before Release, not just Release: this call must still take its
		// turn behind its real predecessor before handing off to whoever is
		// queued behind it, or a later caller could run concurrently with
		// that still-in-flight predecessor.
		if ticket, ok := MutationTicketFromContext(ctx); ok {
			ticket.Wait()
			ticket.Release()
		}
		finalized = immediateToolCall(prepared.call, errorToolResult("Operation aborted"))
	} else {
		executed, err := h.executePreparedToolCall(ctx, prepared)
		if err != nil {
			return finalizedToolCall{}, err
		}
		finalized = h.finalizeExecutedToolCall(ctx, prepared, executed)
	}
	return finalized, h.deliver(toolExecutionEndEvent(finalized))
}

// failToolCallsFromTruncatedMessage fails every tool call of an assistant
// message that hit the output token limit. Streamed tool-call arguments are
// finalized with a best-effort salvage parser, so a truncated message can
// yield arguments that parse and validate but are silently incomplete.
func (r *loopRun) failToolCallsFromTruncatedMessage(calls []pendingToolCall) executedToolCallBatch {
	messages := make([]ToolResultMessage, 0, len(calls))
	for _, call := range calls {
		r.h.emitToolExecutionStart(call)
		finalized := immediateToolCall(call, errorToolResult(`Tool call "`+call.name+
			`" was not executed: the response hit the output token limit, so its arguments may be truncated. Re-issue the tool call with complete arguments.`))
		r.h.emitToolExecutionEnd(finalized)
		messages = append(messages, r.appendToolResult(finalized))
	}
	return executedToolCallBatch{messages: messages}
}

// shouldTerminateToolBatch reports whether every finalized result of a
// non-empty batch asks the agent to stop.
func shouldTerminateToolBatch(finalized []finalizedToolCall) bool {
	if len(finalized) == 0 {
		return false
	}
	for _, f := range finalized {
		if !f.result.Terminate {
			return false
		}
	}
	return true
}

// toolCallOutcome is prepareToolCall's result: exactly one field is set.
type toolCallOutcome struct {
	prepared  *preparedToolCall
	finalized *finalizedToolCall
}

func immediateOutcome(call pendingToolCall, result AgentToolResult) toolCallOutcome {
	finalized := immediateToolCall(call, result)
	return toolCallOutcome{finalized: &finalized}
}

func immediateToolCall(call pendingToolCall, result AgentToolResult) finalizedToolCall {
	return finalizedToolCall{call: call, result: result, isError: true}
}

// errorToolResult is upstream createErrorToolResult: the text and an empty details object, and no isError. The
// caller reports the failure separately, as finalizedToolCall.isError (agent-loop.ts:906-910).
func errorToolResult(message string) AgentToolResult {
	return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, Details: map[string]any{}}
}

func (h *loopHost) findTool(name string) AgentTool { return findTool(h.tools(), name) }

func findTool(tools []AgentTool, name string) AgentTool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// toolCallHooks is the hook set every tool call of this run goes through.
func (h *loopHost) toolCallHooks() ToolCallHooks {
	return ToolCallHooks{BeforeToolCall: h.cfg.BeforeToolCall, AfterToolCall: h.cfg.AfterToolCall, BeforeToolCallHooks: h.cfg.BeforeToolCallHooks, AfterToolCallHooks: h.cfg.AfterToolCallHooks, PrepareToolResult: h.cfg.PrepareToolResult}
}

// prepareToolCall looks the tool up, prepares and validates the arguments,
// and runs the before hooks. Mirrors upstream prepareToolCall: hooks see the
// validated arguments, arguments a hook returns execute without
// revalidation, and a panic while preparing becomes an error result the way
// upstream's catch turns a thrown error into one.
func (h *loopHost) prepareToolCall(ctx context.Context, call pendingToolCall) toolCallOutcome {
	return prepareToolCall(ctx, h.tools(), h.toolCallHooks(), call)
}

// prepareToolCall resolves the call against tools; see Agent.prepareToolCall.
// Mirrors upstream prepareToolCall, whose config is ToolCallHooks and whose
// tools default to the context's (.upstream/v0.99.1/packages/agent/src/agent-loop.ts:707-716).
func prepareToolCall(ctx context.Context, tools []AgentTool, hooks ToolCallHooks, call pendingToolCall) (outcome toolCallOutcome) {
	tool := findTool(tools, call.name)
	if tool == nil {
		return immediateOutcome(call, errorToolResult("Tool "+call.name+" not found"))
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = immediateOutcome(call, errorToolResult(thrownMessage(recovered)))
		}
	}()
	args := json.RawMessage(call.args.String())
	if preparer, ok := tool.(ArgumentPreparer); ok {
		prepared, err := preparer.PrepareArguments(args)
		if err != nil {
			return immediateOutcome(call, errorToolResult(err.Error()))
		}
		if len(prepared) > 0 {
			args = prepared
		}
	}
	var validationErr error
	if schemaTool, ok := tool.(interface{ ArgumentSchema() json.RawMessage }); ok {
		args, validationErr = ai.ValidateToolArgumentsJSON(call.name, schemaTool.ArgumentSchema(), args)
	} else {
		args, validationErr = ai.ValidateToolArgumentsSchema(call.name, tool.Schema().Parameters, args)
	}
	if validationErr != nil {
		return immediateOutcome(call, errorToolResult(validationErr.Error()))
	}
	for _, hook := range hooks.before() {
		hookResult := hook(ctx, call.id, call.name, args)
		if ctx.Err() != nil {
			return immediateOutcome(call, errorToolResult("Operation aborted"))
		}
		if hookResult.Block {
			reason := hookResult.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			result := errorToolResult(reason)
			result.Terminate = hookResult.Terminate
			return immediateOutcome(call, result)
		}
		if hookResult.Args != nil {
			args = hookResult.Args
		}
	}
	if ctx.Err() != nil {
		return immediateOutcome(call, errorToolResult("Operation aborted"))
	}
	return toolCallOutcome{prepared: &preparedToolCall{call: call, tool: tool, args: args}}
}

// executePreparedToolCall runs the tool for the loop: every accepted update
// becomes a tool_execution_update event, and the call's duration is recorded.
// Updates may arrive on the tool's goroutines, so a sink failure is returned
// once the call settled, as upstream's awaited update promises reject the call.
func (h *loopHost) executePreparedToolCall(ctx context.Context, prepared preparedToolCall) (finalizedToolCall, error) {
	rawArgs := json.RawMessage(prepared.call.args.String())
	executed, err := executePreparedToolCall(ctx, prepared, func(partial AgentToolResult) error {
		return h.deliver(ToolExecutionUpdateEvent{
			ToolCallID:    prepared.call.id,
			ToolName:      prepared.call.name,
			PartialResult: partial,
			Args:          rawArgs,
		})
	})
	if err != nil {
		return finalizedToolCall{}, err
	}
	h.timings.RecordTool(prepared.call.name, executed.duration)
	return executed, nil
}

// executePreparedToolCall runs the tool. Progress updates are accepted only
// while Execute runs, and every accepted update is delivered to onUpdate
// before the call completes (upstream awaits its pending update emissions). A
// tool may call its update callback from its own goroutines. A returned error
// is upstream's thrown one: createErrorToolResult without isError. A result with
// IsError set is an error outcome that keeps its details and its own isError
// (upstream `isError: result.isError === true`, agent-loop.ts:840).
//
// An update the sink rejects does not stop the tool or the later updates. Once the tool returned and every update settled, the first rejection is returned and the result is dropped: upstream awaits `Promise.all(updateEvents)` in the try body and again in the catch, so the rejection leaves the function either way (agent-loop.ts:833-845).
func executePreparedToolCall(ctx context.Context, prepared preparedToolCall, onUpdate ToolUpdateSink) (finalizedToolCall, error) {
	var mu sync.Mutex
	accepting := true
	var firstRejection error
	var inflight sync.WaitGroup
	sink := func(partial AgentToolResult) {
		mu.Lock()
		if !accepting {
			mu.Unlock()
			return
		}
		inflight.Add(1)
		mu.Unlock()
		defer inflight.Done()
		if err := onUpdate(partial); err != nil {
			mu.Lock()
			if firstRejection == nil {
				firstRejection = err
			}
			mu.Unlock()
		}
	}

	start := time.Now()
	result, err := executeTool(ctx, prepared, sink)
	// upstream: agent-loop.ts:841-846 reads elapsed() before it awaits the update events.
	duration := time.Since(start)
	mu.Lock()
	accepting = false
	mu.Unlock()
	inflight.Wait()
	mu.Lock()
	rejection := firstRejection
	mu.Unlock()
	if rejection != nil {
		return finalizedToolCall{}, rejection
	}
	isError := result.IsError
	switch {
	case err != nil:
		result, isError = errorToolResult(err.Error()), true
	case result.Thrown:
		result.IsError, result.Thrown, isError = false, false, true
	}
	return finalizedToolCall{call: prepared.call, result: result, isError: isError, executed: true, duration: duration}, nil
}

// executeTool runs the tool. A panic becomes an error, as upstream's catch
// around tool.execute turns a thrown error into an error tool result instead
// of ending the run without one.
func executeTool(ctx context.Context, prepared preparedToolCall, onUpdate ToolUpdateCallback) (result AgentToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = AgentToolResult{}, errors.New(thrownMessage(recovered))
		}
	}()
	return prepared.tool.Execute(ctx, prepared.call.id, prepared.args, onUpdate)
}

// thrownMessage mirrors upstream's `error instanceof Error ? error.message :
// String(error)` for a recovered panic value.
func thrownMessage(recovered any) string {
	if err, ok := recovered.(error); ok {
		return err.Error()
	}
	return fmt.Sprint(recovered)
}

// runAfterToolCallHook calls one after hook; ok is false when it panicked.
func runAfterToolCallHook(ctx context.Context, hook AfterToolCallHook, prepared preparedToolCall, result AgentToolResult) (override AfterToolCallResult, message string, ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message, ok = thrownMessage(recovered), false
		}
	}()
	return hook(ctx, prepared.call.id, prepared.call.name, prepared.args, result), "", true
}

func (h *loopHost) finalizeExecutedToolCall(ctx context.Context, prepared preparedToolCall, executed finalizedToolCall) finalizedToolCall {
	return finalizeExecutedToolCall(ctx, h.toolCallHooks(), prepared, executed)
}

// finalizeExecutedToolCall applies the after hooks' overrides field by field.
// Mirrors upstream finalizeExecutedToolCall, including its catch: a hook that
// panics replaces the result with an error result. Structured content that
// the hook did not replace along with the content is dropped, because it may
// no longer match it (agent-loop.ts:877-889); a JSON null counts as absent, as
// `??` does.
//
// A hook's isError replaces the call's flag, not the result's own: the spread
// keeps the tool's (agent-loop.ts:890). Hooks see the call's flag in
// result.IsError, as upstream's context.isError beside the result. A hook that
// returns an empty override leaves the result as it was, as a hook that returns
// undefined does.
func finalizeExecutedToolCall(ctx context.Context, hooks ToolCallHooks, prepared preparedToolCall, executed finalizedToolCall) finalizedToolCall {
	result, isError := executed.result, executed.isError
	for _, hook := range hooks.after() {
		seen := result
		seen.IsError = isError
		override, message, ok := runAfterToolCallHook(ctx, hook, prepared, seen)
		if !ok {
			result, isError = errorToolResult(message), true
			break
		}
		if override.isZero() {
			continue
		}
		structuredContent := override.StructuredContent
		if isNullishJSON(structuredContent) {
			structuredContent = nil
			if override.Content == nil {
				structuredContent = result.StructuredContent
			}
		}
		if override.Content != nil {
			result.Content = override.Content
		}
		if override.Details != nil {
			result.Details = override.Details
		}
		if override.Usage != nil {
			result.Usage = override.Usage
		}
		if override.Terminate != nil {
			result.Terminate = *override.Terminate
		}
		hadStructuredContent := len(result.StructuredContent) > 0
		result.StructuredContent = structuredContent
		switch {
		case len(structuredContent) == 0:
			result.StructuredContentAppended = false
		case !hadStructuredContent:
			result.StructuredContentAppended = true
		}
		if override.IsError != nil {
			isError = *override.IsError
		}
	}
	if hooks.PrepareToolResult != nil {
		seen := result
		seen.IsError = isError
		prepared := hooks.PrepareToolResult(ctx, seen)
		isError = prepared.IsError
		prepared.IsError = result.IsError
		result = prepared
	}
	executed.result, executed.isError = result, isError
	return executed
}

// isZero reports an override that changes nothing: upstream's hook returned undefined.
func (r AfterToolCallResult) isZero() bool {
	return r.Content == nil && r.Details == nil && r.IsError == nil && r.Usage == nil && r.Terminate == nil && len(r.StructuredContent) == 0
}

// isNullishJSON reports upstream's null-or-undefined for a structured-content value.
func isNullishJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null"
}

func (h *loopHost) emitToolExecutionStart(call pendingToolCall) {
	label := ""
	if tool := h.findTool(call.name); tool != nil {
		label = tool.Label()
	}
	h.emit(ToolExecutionStartEvent{ToolCallID: call.id, ToolName: call.name, ToolLabel: label, Args: json.RawMessage(call.args.String())})
}

func (h *loopHost) emitToolExecutionEnd(finalized finalizedToolCall) {
	h.emit(toolExecutionEndEvent(finalized))
}

func toolExecutionEndEvent(finalized finalizedToolCall) ToolExecutionEndEvent {
	return ToolExecutionEndEvent{ToolCallID: finalized.call.id, ToolName: finalized.call.name, Result: finalized.result, IsError: finalized.isError, DurationMs: finalized.durationMs()}
}

// durationMs is upstream's durationMs on a finalized outcome: the milliseconds execute() took, rounded like Math.round, and
// absent when the tool did not run. upstream: agent-loop.ts:826-852,908
func (f finalizedToolCall) durationMs() *int64 {
	if !f.executed {
		return nil
	}
	return new(int64(math.Round(float64(f.duration) / float64(time.Millisecond))))
}

// appendToolResult emits a finalized call's tool-result message and records it.
func (r *loopRun) appendToolResult(finalized finalizedToolCall) ToolResultMessage {
	msg := createToolResultMessage(finalized, time.Now().UnixMilli())
	r.appendMessage(AgentMessage{ToolResult: &msg})
	return msg
}

func createToolResultMessage(finalized finalizedToolCall, timestamp int64) ToolResultMessage {
	result := finalized.result
	content := result.Content
	if content == nil {
		content = []ai.ToolResultMessageContent{}
	}
	return ToolResultMessage{
		Role:        RoleToolResult,
		ToolCallID:  finalized.call.id,
		ToolName:    finalized.call.name,
		Content:     content,
		Details:     result.Details,
		DetailsNull: result.DetailsNull(),
		Usage:       result.Usage,
		IsError:     finalized.isError,
		DurationMs:  finalized.durationMs(),
		Timestamp:   timestamp,
	}
}
