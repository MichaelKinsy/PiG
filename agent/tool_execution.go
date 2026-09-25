package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	return WithToolEnvironment(r.ctx, r.a.toolEnvironment(r.model, r.thinking))
}

// executeToolCalls runs a batch sequentially when the loop is configured for
// sequential execution or any call targets a tool whose ExecutionMode is
// sequential; otherwise in parallel.
func (r *loopRun) executeToolCalls(calls []pendingToolCall) executedToolCallBatch {
	if r.cfg.toolExecution == ToolModeSequential || r.hasSequentialToolCall(calls) {
		return r.executeToolCallsSequential(calls)
	}
	return r.executeToolCallsParallel(calls)
}

func (r *loopRun) hasSequentialToolCall(calls []pendingToolCall) bool {
	for _, call := range calls {
		if tool := r.a.findTool(call.name); tool != nil && tool.ExecutionMode() == ToolModeSequential {
			return true
		}
	}
	return false
}

// executeToolCallsSequential prepares, executes, and finalizes each call
// before the next one starts, emitting each call's tool-result message as soon
// as the call is finalized.
func (r *loopRun) executeToolCallsSequential(calls []pendingToolCall) executedToolCallBatch {
	ctx := r.toolCtx()
	finalized := make([]finalizedToolCall, 0, len(calls))
	messages := make([]ToolResultMessage, 0, len(calls))
	for _, call := range calls {
		r.a.emitToolExecutionStart(call)
		var f finalizedToolCall
		if outcome := r.a.prepareToolCall(ctx, call); outcome.prepared != nil {
			f = r.a.finalizeExecutedToolCall(ctx, *outcome.prepared, r.a.executePreparedToolCall(ctx, *outcome.prepared))
		} else {
			f = *outcome.finalized
		}
		r.a.emitToolExecutionEnd(f)
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
func (r *loopRun) executeToolCallsParallel(calls []pendingToolCall) executedToolCallBatch {
	ctx := r.toolCtx()
	outcomes := make([]toolCallOutcome, 0, len(calls))
	callCtxs := make([]context.Context, 0, len(calls))
	for _, call := range calls {
		r.a.emitToolExecutionStart(call)
		outcome := r.a.prepareToolCall(ctx, call)
		callCtx := ctx
		if outcome.prepared != nil {
			if orderer, ok := outcome.prepared.tool.(QueueOrderable); ok {
				if ticket, has := orderer.ReserveMutationOrder(outcome.prepared.args); has {
					callCtx = WithMutationTicket(ctx, ticket)
				}
			}
		}
		if outcome.finalized != nil {
			r.a.emitToolExecutionEnd(*outcome.finalized)
		}
		outcomes = append(outcomes, outcome)
		callCtxs = append(callCtxs, callCtx)
		if ctx.Err() != nil {
			break
		}
	}

	finalized := make([]finalizedToolCall, len(outcomes))
	var wg sync.WaitGroup
	for i, outcome := range outcomes {
		if outcome.prepared == nil {
			finalized[i] = *outcome.finalized
			continue
		}
		callCtx := callCtxs[i]
		// Pi awaits Promise.all over every prepared call, so all calls run at
		// once. A CPU-count cap would serialize I/O-bound tools on small machines.
		wg.Go(func() {
			finalized[i] = r.a.runPreparedToolCall(callCtx, *outcome.prepared)
		})
	}
	wg.Wait()

	messages := make([]ToolResultMessage, 0, len(finalized))
	for _, f := range finalized {
		messages = append(messages, r.appendToolResult(f))
	}
	return executedToolCallBatch{messages: messages, terminate: shouldTerminateToolBatch(finalized)}
}

// runPreparedToolCall executes and finalizes one call of a parallel batch,
// then emits its tool_execution_end.
func (a *Agent) runPreparedToolCall(ctx context.Context, prepared preparedToolCall) finalizedToolCall {
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
		finalized = a.finalizeExecutedToolCall(ctx, prepared, a.executePreparedToolCall(ctx, prepared))
	}
	a.emitToolExecutionEnd(finalized)
	return finalized
}

// failToolCallsFromTruncatedMessage fails every tool call of an assistant
// message that hit the output token limit. Streamed tool-call arguments are
// finalized with a best-effort salvage parser, so a truncated message can
// yield arguments that parse and validate but are silently incomplete.
func (r *loopRun) failToolCallsFromTruncatedMessage(calls []pendingToolCall) executedToolCallBatch {
	messages := make([]ToolResultMessage, 0, len(calls))
	for _, call := range calls {
		r.a.emitToolExecutionStart(call)
		finalized := immediateToolCall(call, errorToolResult(`Tool call "`+call.name+
			`" was not executed: the response hit the output token limit, so its arguments may be truncated. Re-issue the tool call with complete arguments.`))
		r.a.emitToolExecutionEnd(finalized)
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

func errorToolResult(message string) AgentToolResult {
	return AgentToolResult{Content: message, IsError: true}
}

func (a *Agent) findTool(name string) AgentTool {
	for _, t := range a.opts.Tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// prepareToolCall looks the tool up, prepares and validates the arguments,
// and runs the before hooks. Mirrors upstream prepareToolCall: hooks see the
// validated arguments, arguments a hook returns execute without
// revalidation, and a panic while preparing becomes an error result the way
// upstream's catch turns a thrown error into one.
func (a *Agent) prepareToolCall(ctx context.Context, call pendingToolCall) (outcome toolCallOutcome) {
	tool := a.findTool(call.name)
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
	// Mirrors upstream validateToolArguments (validation.ts).
	if err := validateToolArgs(call.name, tool.Schema().Parameters, args); err != nil {
		return immediateOutcome(call, errorToolResult(err.Error()))
	}
	for _, hook := range a.opts.BeforeToolCall {
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

// executePreparedToolCall runs the tool. Progress updates are accepted only
// while Execute runs, and every accepted update is delivered before the call
// completes (upstream awaits its pending update emissions). A tool may call
// onUpdate from its own goroutines.
func (a *Agent) executePreparedToolCall(ctx context.Context, prepared preparedToolCall) finalizedToolCall {
	rawArgs := json.RawMessage(prepared.call.args.String())
	var mu sync.Mutex
	accepting := true
	var inflight sync.WaitGroup
	onUpdate := func(content string, details any) {
		mu.Lock()
		if !accepting {
			mu.Unlock()
			return
		}
		inflight.Add(1)
		mu.Unlock()
		defer inflight.Done()
		a.emit(ToolExecutionUpdateEvent{
			ToolCallID: prepared.call.id,
			ToolName:   prepared.call.name,
			Content:    content,
			Details:    details,
			Args:       rawArgs,
		})
	}

	start := time.Now()
	result, err := executeTool(ctx, prepared, onUpdate)
	mu.Lock()
	accepting = false
	mu.Unlock()
	inflight.Wait()
	duration := time.Since(start)
	a.timings.RecordTool(prepared.call.name, duration)
	if err != nil {
		result = errorToolResult(err.Error())
	}
	return finalizedToolCall{call: prepared.call, result: result, isError: result.IsError, executed: true, duration: duration}
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

// finalizeExecutedToolCall applies the after hooks' overrides field by field.
// Mirrors upstream finalizeExecutedToolCall, including its catch: a hook that
// panics replaces the result with an error result.
func (a *Agent) finalizeExecutedToolCall(ctx context.Context, prepared preparedToolCall, executed finalizedToolCall) finalizedToolCall {
	result := executed.result
	for _, hook := range a.opts.AfterToolCall {
		result.IsError = executed.isError
		override, message, ok := runAfterToolCallHook(ctx, hook, prepared, result)
		if !ok {
			result = errorToolResult(message)
			executed.isError = true
			break
		}
		if override.Content != nil {
			result.Content = *override.Content
		}
		if override.Images != nil {
			result.Images = append([]ai.ImageContent(nil), (*override.Images)...)
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
		if override.IsError != nil {
			executed.isError = *override.IsError
		}
	}
	result.IsError = executed.isError
	if a.opts.PrepareToolResult != nil {
		result = a.opts.PrepareToolResult(ctx, result)
		executed.isError = result.IsError
	}
	executed.result = result
	return executed
}

func (a *Agent) emitToolExecutionStart(call pendingToolCall) {
	label := ""
	if tool := a.findTool(call.name); tool != nil {
		label = tool.Label()
	}
	a.emit(ToolExecutionStartEvent{ToolCallID: call.id, ToolName: call.name, ToolLabel: label, Args: json.RawMessage(call.args.String())})
}

func (a *Agent) emitToolExecutionEnd(finalized finalizedToolCall) {
	result := finalized.result
	result.IsError = finalized.isError
	a.emit(ToolExecutionEndEvent{ToolCallID: finalized.call.id, ToolName: finalized.call.name, Result: result, Duration: finalized.duration})
	if finalized.executed {
		a.emit(TimingEvent{Kind: "tool", Name: finalized.call.name, Duration: finalized.duration, Snapshot: a.timings.Snapshot()})
	}
}

// appendToolResult emits a finalized call's tool-result message and records it.
func (r *loopRun) appendToolResult(finalized finalizedToolCall) ToolResultMessage {
	msg := createToolResultMessage(finalized, time.Now().UnixMilli())
	r.appendMessage(AgentMessage{ToolResult: &msg})
	return msg
}

func createToolResultMessage(finalized finalizedToolCall, timestamp int64) ToolResultMessage {
	result := finalized.result
	content := make([]ai.ToolResultMessageContent, 0, 1+len(result.Images))
	if result.Content != "" {
		content = append(content, ai.TextContent{Text: result.Content})
	}
	for _, image := range result.Images {
		content = append(content, image)
	}
	return ToolResultMessage{
		Role:       RoleToolResult,
		ToolCallID: finalized.call.id,
		ToolName:   finalized.call.name,
		Content:    content,
		Details:    result.Details,
		Usage:      result.Usage,
		IsError:    finalized.isError,
		Timestamp:  timestamp,
	}
}
