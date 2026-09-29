package runtime

// Ports packages/agent/src/harness/runtime/drive/tools.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

const interruptionMarker = "[Tool execution was interrupted. The preceding output is the latest durable progress snapshot; newer live output may be missing, and the external outcome is unknown.]"

type ToolInvocationEnded struct{}

func (*ToolInvocationEnded) Error() string {
	return "Tool invocation no longer owns its durable effect"
}

type toolOutcome struct {
	toolCall  ai.ToolCall
	message   ai.ToolResultMessage
	terminate bool
}
type toolCallTask struct{ completion <-chan error }

func currentBatch(lane *Lane) *session.OperationState {
	state := lane.SnapshotState()
	if state.Operation == nil || state.Operation.State.At != session.AtTools {
		return nil
	}
	return &state.Operation.State
}

func findCall(batch session.ToolBatch, sourceIndex int, resultEntryID string) *session.ToolCall {
	for _, call := range batch.Calls {
		if call.SourceIndex == sourceIndex && call.ResultEntryID == resultEntryID {
			return &call
		}
	}
	return nil
}

func replaceCall(batch session.ToolBatch, replacement session.ToolCall) session.ToolBatch {
	batch.Calls = append([]session.ToolCall{}, batch.Calls...)
	for i, call := range batch.Calls {
		if call.SourceIndex == replacement.SourceIndex && call.ResultEntryID == replacement.ResultEntryID {
			batch.Calls[i] = replacement
		}
	}
	return batch
}

func validateMemoName(name string) error {
	if name == "" {
		return errors.New("Tool invocation memo name must not be empty")
	}
	if strings.Contains(name, ":") {
		return errors.New("Tool invocation memo name must not contain ':'")
	}
	return nil
}

type toolInvocation struct {
	lane   *Lane
	drive  *Drive
	batch  session.ToolBatch
	call   session.ToolCall
	active atomic.Bool
}

func (inv *toolInvocation) InvocationID() string { return inv.call.ResultEntryID }
func (inv *toolInvocation) OperationID() string  { return inv.drive.OperationID }
func (inv *toolInvocation) TurnID() string       { return inv.batch.TurnID }
func (inv *toolInvocation) ownsEffect(state LaneState) bool {
	if state.Operation == nil || state.Operation.State.At != session.AtTools {
		return false
	}
	call := findCall(state.Operation.State.Batch, inv.call.SourceIndex, inv.call.ResultEntryID)
	return call != nil && call.Status == session.ToolCallEffectPending
}
func (inv *toolInvocation) GetMemo(_ context.Context, name string) (any, bool, error) {
	if err := validateMemoName(name); err != nil {
		return nil, false, err
	}
	if !inv.active.Load() {
		return nil, false, &ToolInvocationEnded{}
	}
	stored, err := Command(inv.drive.Context, inv.lane, func(state LaneState, reader session.SessionReader) (LaneCommand[*session.StoredValue[any]], error) {
		if !inv.ownsEffect(state) {
			return LaneCommand[*session.StoredValue[any]]{Kind: CommandReject, Error: &ToolInvocationEnded{}}, nil
		}
		value, err := session.GetValue(inv.drive.Context, reader, session.OperationToolMemo(inv.drive.OperationID, inv.call.ResultEntryID, name))
		return LaneCommand[*session.StoredValue[any]]{Kind: CommandReturn, Result: value}, err
	})
	if err != nil || stored == nil {
		return nil, false, err
	}
	return stored.Value, true, nil
}
func (inv *toolInvocation) SetMemo(_ context.Context, name string, value *any) error {
	if err := validateMemoName(name); err != nil {
		return err
	}
	if !inv.active.Load() {
		return &ToolInvocationEnded{}
	}
	_, err := Command(inv.drive.Context, inv.lane, func(state LaneState, _ session.SessionReader) (LaneCommand[struct{}], error) {
		if !inv.ownsEffect(state) {
			return LaneCommand[struct{}]{Kind: CommandReject, Error: &ToolInvocationEnded{}}, nil
		}
		address := session.OperationToolMemo(inv.drive.OperationID, inv.call.ResultEntryID, name)
		var write session.Write = session.DeleteValue(address)
		if value != nil {
			write = session.SetValue(address, *value)
		}
		return LaneCommand[struct{}]{Kind: CommandCommit, Writes: []session.Write{write}, Next: state, Materialize: func(session.CommitResult) struct{} { return struct{}{} }}, nil
	})
	return err
}

func syntheticOutcome(call ai.ToolCall, content []ai.ToolResultMessageContent, checkpoint *harness.AgentToolResult) toolOutcome {
	message := ai.ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: content, IsError: true, Timestamp: runtimeNow()}
	if checkpoint != nil {
		message.Details = checkpoint.Details
		message.Usage = checkpoint.Usage
	}
	return toolOutcome{toolCall: call, message: message}
}
func abortedOutcome(call ai.ToolCall) toolOutcome {
	return syntheticOutcome(call, []ai.ToolResultMessageContent{ai.TextContent{Text: "Tool execution was cancelled before completion."}}, nil)
}
func interruptedOutcome(call ai.ToolCall, checkpoint *harness.AgentToolResult) toolOutcome {
	content := []ai.ToolResultMessageContent{}
	if checkpoint != nil {
		content = append(content, checkpoint.Content...)
	}
	content = append(content, ai.TextContent{Text: interruptionMarker})
	return syntheticOutcome(call, content, checkpoint)
}
func truncatedOutcome(call ai.ToolCall) toolOutcome {
	name, _ := json.Marshal(call.Name)
	return syntheticOutcome(call, []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Tool call %s was not executed because the assistant response hit the output token limit, so its arguments may be truncated. Re-issue the tool call with complete arguments.", name)}}, nil)
}
func outcomeFromFinalizedCall(call execution.FinalizedToolCall) toolOutcome {
	return toolOutcome{toolCall: *call.ToolCall, message: execution.CreateToolResultMessage(call), terminate: call.Terminate}
}
func immediateToolOutcome(call *execution.ImmediateToolOutcome) toolOutcome {
	return outcomeFromFinalizedCall(execution.FinalizedToolCall{ToolCall: call.ToolCall, Result: call.Result, IsError: call.IsError, Terminate: call.Terminate})
}

func publishToolIntent(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState, planned session.ToolCall, call ai.ToolCall, args map[string]any, replay string, recovery bool) (ContinueOperationResult[session.ToolCall], error) {
	return ContinueOperation(ctx, lane, run, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[session.ToolCall], error) {
		pending := session.ToolCall{Status: session.ToolCallEffectPending, SourceIndex: planned.SourceIndex, ResultEntryID: planned.ResultEntryID, Replay: replay}
		return OperationCommand[session.ToolCall]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.OperationToolArgs(drive.OperationID, current.Batch.TurnID, planned.SourceIndex), args)}, OperationState: WithToolBatch(current, replaceCall(current.Batch, pending)), Materialize: func(session.CommitResult) session.ToolCall { return pending }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.ToolStartPayload{RunID: drive.OperationID, TurnID: current.Batch.TurnID, ToolCallID: call.ID, ToolName: call.Name, Args: args}}}
		}}, nil
	})
}

func publishToolOutcome(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState, call session.ToolCall, outcome toolOutcome, recovery bool) error {
	_, err := SettleOperation(ctx, lane, capability, func(_ LaneState, run session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[struct{}], error) {
		memos, err := session.ScanValues(ctx, reader, session.OperationToolMemoPrefix(drive.OperationID, &call.ResultEntryID))
		if err != nil {
			return OperationCommand[struct{}]{}, err
		}
		terminate := run.Control.Status == session.ControlRunning && outcome.terminate
		ready := session.ToolCall{Status: session.ToolCallOutcomeReady, SourceIndex: call.SourceIndex, ResultEntryID: call.ResultEntryID, Terminate: terminate}
		writes := []session.Write{session.SetValue(session.PendingEntryValue(call.ResultEntryID), session.PendingEntry{Type: session.PendingEntryMessage, Message: agent.AgentMessage{ToolResult: new(runtimeToolMessage(outcome.message))}}), session.DeleteValue(session.PendingToolOutput(drive.OperationID, call.ResultEntryID))}
		for _, memo := range memos {
			writes = append(writes, session.DeleteValue(memo.Address))
		}
		return OperationCommand[struct{}]{Kind: CommandCommit, Writes: writes, OperationState: WithToolBatch(run, replaceCall(run.Batch, ready)), Materialize: func(session.CommitResult) struct{} { return struct{}{} }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			events := []agentharness.HarnessEvent{}
			if call.Status == session.ToolCallPlanned {
				events = append(events, agentharness.HarnessEvent{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.ToolStartPayload{RunID: drive.OperationID, TurnID: run.Batch.TurnID, ToolCallID: outcome.toolCall.ID, ToolName: outcome.toolCall.Name, Args: outcome.toolCall.Arguments}})
			}
			return append(events, agentharness.HarnessEvent{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.ToolEndPayload{RunID: drive.OperationID, TurnID: run.Batch.TurnID, ToolCallID: outcome.toolCall.ID, ToolName: outcome.toolCall.Name, Result: execution.ToolResultFromMessage(runtimeToolMessage(outcome.message), terminate), IsError: outcome.message.IsError, Terminate: terminate}})
		}}, nil
	})
	return err
}

func clearReplayCheckpoint(ctx harness.Context, lane *Lane, drive *Drive, batch session.ToolBatch, call session.ToolCall, toolCall ai.ToolCall) (map[string]any, error) {
	return Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[map[string]any], error) {
		stored, err := session.GetValue(ctx, reader, session.OperationToolArgs(drive.OperationID, batch.TurnID, call.SourceIndex))
		if err != nil {
			return LaneCommand[map[string]any]{}, err
		}
		if stored == nil {
			return LaneCommand[map[string]any]{}, &session.SessionInvariantError{Message: "Tool call " + call.ResultEntryID + " is missing persisted arguments"}
		}
		return LaneCommand[map[string]any]{Kind: CommandCommit, Writes: []session.Write{session.DeleteValue(session.PendingToolOutput(drive.OperationID, call.ResultEntryID))}, Next: state, Materialize: func(session.CommitResult) map[string]any { return stored.Value }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: true, Payload: agentharness.ToolStartPayload{RunID: drive.OperationID, TurnID: batch.TurnID, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: stored.Value}}}
		}}, nil
	})
}
func readCheckpoint(ctx harness.Context, lane *Lane, drive *Drive, call session.ToolCall) (*harness.AgentToolResult, error) {
	return Command(ctx, lane, func(_ LaneState, reader session.SessionReader) (LaneCommand[*harness.AgentToolResult], error) {
		stored, err := session.GetValue(ctx, reader, session.PendingToolOutput(drive.OperationID, call.ResultEntryID))
		var value *harness.AgentToolResult
		if stored != nil {
			value = &stored.Value
		}
		return LaneCommand[*harness.AgentToolResult]{Kind: CommandReturn, Result: value}, err
	})
}

func performToolInvocation(ctx harness.Context, lane *Lane, drive *Drive, batch session.ToolBatch, call session.ToolCall, cleared *execution.ClearedToolCall, toolContext any, recovery bool) (toolOutcome, error) {
	inv := &toolInvocation{lane: lane, drive: drive, batch: batch, call: call}
	inv.active.Store(true)
	progress := OpenToolProgress(lane, drive, batch.TurnID, call.SourceIndex, call.ResultEntryID)
	var updates sync.WaitGroup
	var updateMu sync.Mutex
	var updateErr error
	executed, err := execution.ExecuteToolCall(ctx, cleared, drive.Gate, func(partial harness.AgentToolResult, options harness.AgentHarnessToolUpdateOptions) {
		// Bind recipients and publication order before returning the non-awaitable update callback. The invocation joins every delivery before after_tool.
		delivery, err := lane.prepareBatch(ctx, []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.ToolUpdatePayload{RunID: drive.OperationID, TurnID: batch.TurnID, ToolCallID: cleared.ToolCall.ID, ToolName: cleared.ToolCall.Name, PartialResult: partial}}})
		if err != nil {
			updateMu.Lock()
			updateErr = err
			updateMu.Unlock()
		} else if delivery != nil {
			updates.Go(func() {
				if err := delivery(); err != nil {
					updateMu.Lock()
					updateErr = err
					updateMu.Unlock()
				}
			})
		}
		if options.Checkpoint {
			progress.Write(partial)
		}
	}, toolContext, inv)
	inv.active.Store(false)
	progress.Seal()
	updates.Wait()
	if drainErr := progress.Drain(); drainErr != nil {
		return toolOutcome{}, drainErr
	}
	if err != nil {
		var aborted *AbortRequested
		if !errors.As(err, &aborted) {
			return toolOutcome{}, err
		}
		if err := aborted.Cancellation(); err != nil {
			return toolOutcome{}, err
		}
		if recovery {
			return interruptedOutcome(*cleared.ToolCall, nil), nil
		}
		return abortedOutcome(*cleared.ToolCall), nil
	}
	if updateErr != nil {
		return toolOutcome{}, updateErr
	}
	hook, err := lane.Hooks.RunAfterTool(ctx, drive.Gate, agentharness.AfterToolEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, ToolCallID: cleared.ToolCall.ID, ToolName: cleared.ToolCall.Name, Args: cleared.Args, Content: executed.Result.Content, Details: executed.Result.Details, HasDetails: executed.Result.Details != nil, IsError: executed.IsError, Usage: executed.Result.Usage})
	if err != nil {
		if _, ok := errors.AsType[*AbortRequested](err); !ok {
			return toolOutcome{}, err
		}
		hook = nil
	}
	var patch *execution.AfterToolPatch
	if hook != nil {
		patch = &execution.AfterToolPatch{Content: hook.Content, Details: hook.Details, SetDetails: hook.SetDetails, IsError: hook.IsError, Usage: hook.Usage, Terminate: hook.Terminate}
	}
	return outcomeFromFinalizedCall(execution.FinalizeToolCall(cleared, executed, patch)), nil
}

func prepareToolInvocation(ctx harness.Context, lane *Lane, drive *Drive, sources ToolBatchSource, call session.ToolCall, tools []harness.AgentHarnessTool) (*execution.ClearedToolCall, *toolOutcome, error) {
	toolCall, err := ToolCallFor(sources, call)
	if err != nil {
		return nil, nil, err
	}
	if sources.Assistant.StopReason == ai.StopReasonLength {
		return nil, new(truncatedOutcome(toolCall)), nil
	}
	prepared, immediate := execution.PrepareToolCall(&toolCall, tools)
	if immediate != nil {
		return nil, new(immediateToolOutcome(immediate)), nil
	}
	hook, err := lane.Hooks.RunBeforeTool(ctx, drive.Gate, agentharness.BeforeToolEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: prepared.Args})
	if err != nil {
		var aborted *AbortRequested
		if !errors.As(err, &aborted) {
			return nil, nil, err
		}
		if err := aborted.Cancellation(); err != nil {
			return nil, nil, err
		}
		return nil, new(abortedOutcome(toolCall)), nil
	}
	var decision *execution.BeforeToolDecision
	if hook != nil {
		decision = &execution.BeforeToolDecision{Args: hook.Args}
		if hook.Block != nil {
			decision.Block = &execution.ToolBlock{Reason: hook.Block.Reason, Terminate: hook.Block.Terminate}
		}
	}
	cleared, immediate := execution.ApplyBeforeToolDecision(prepared, decision)
	if immediate != nil {
		return nil, new(immediateToolOutcome(immediate)), nil
	}
	return cleared, nil, nil
}

// A task's owner always joins completion, including when a later call fails preparation.
func startToolTask(invoke func() error) toolCallTask {
	done := make(chan error, 1)
	go func() { done <- invoke(); close(done) }()
	return toolCallTask{completion: done}
}

func startToolInvocation(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState, sources ToolBatchSource, call session.ToolCall, tools []harness.AgentHarnessTool, toolContext any, recovery bool) (toolCallTask, error) {
	cleared, outcome, err := prepareToolInvocation(ctx, lane, drive, sources, call, tools)
	if err != nil {
		return toolCallTask{}, err
	}
	if outcome != nil {
		return startToolTask(func() error { return publishToolOutcome(ctx, lane, drive, run, call, *outcome, recovery) }), nil
	}
	replay := cleared.Tool.Replay
	if replay == "" {
		replay = "never"
	}
	pending, err := publishToolIntent(ctx, lane, drive, run, call, *cleared.ToolCall, cleared.Args, replay, recovery)
	if err != nil {
		return toolCallTask{}, err
	}
	return startToolTask(func() error {
		if pending.CancelRequested {
			return publishToolOutcome(ctx, lane, drive, run, call, abortedOutcome(*cleared.ToolCall), recovery)
		}
		outcome, err := performToolInvocation(ctx, lane, drive, run.Batch, pending.Value, cleared, toolContext, recovery)
		if err != nil {
			return err
		}
		return publishToolOutcome(ctx, lane, drive, run, pending.Value, outcome, recovery)
	}), nil
}

func recoverToolInvocation(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState, sources ToolBatchSource, call session.ToolCall, tools map[string]harness.AgentHarnessTool, toolContext any, cancelled bool) (toolCallTask, error) {
	toolCall, err := ToolCallFor(sources, call)
	if err != nil {
		return toolCallTask{}, err
	}
	tool, present := tools[toolCall.Name]
	if !cancelled && call.Replay == "safe" && present && tool.Replay == "safe" {
		args, err := clearReplayCheckpoint(ctx, lane, drive, run.Batch, call, toolCall)
		if err != nil {
			return toolCallTask{}, err
		}
		cleared := &execution.ClearedToolCall{ToolCall: &toolCall, Tool: &tool, Args: args}
		return startToolTask(func() error {
			outcome, err := performToolInvocation(ctx, lane, drive, run.Batch, call, cleared, toolContext, true)
			if err != nil {
				return err
			}
			return publishToolOutcome(ctx, lane, drive, run, call, outcome, true)
		}), nil
	}
	checkpoint, err := readCheckpoint(ctx, lane, drive, call)
	if err != nil {
		return toolCallTask{}, err
	}
	return startToolTask(func() error {
		return publishToolOutcome(ctx, lane, drive, run, call, interruptedOutcome(toolCall, checkpoint), true)
	}), nil
}

type toolExecutionContext struct {
	tools       []harness.AgentHarnessTool
	toolsByName map[string]harness.AgentHarnessTool
	toolContext any
}

func runSequential(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState, sources ToolBatchSource, execution *toolExecutionContext, recovery bool) (ProcedureResult, error) {
	for transition := 0; transition <= len(run.Batch.Calls)*2+1; transition++ {
		if err := MaterializeReady(ctx, lane, drive, run, sources, recovery); err != nil {
			return ProcedureResult{}, err
		}
		current := currentBatch(lane)
		if current == nil {
			return ProcedureResult{Kind: ProcedureContinue}, nil
		}
		var call *session.ToolCall
		for _, candidate := range current.Batch.Calls {
			if candidate.Status != session.ToolCallCompleted {
				call = &candidate
				break
			}
		}
		if call == nil {
			return ProcedureResult{}, &session.SessionInvariantError{Message: "Tool batch remained open after every call completed"}
		}
		if call.Status == session.ToolCallOutcomeReady {
			return ProcedureResult{}, &session.SessionInvariantError{Message: "Ready tool outcome was not materialized"}
		}
		if current.Control.Status == session.ControlCancelRequested {
			toolCall, err := ToolCallFor(sources, *call)
			if err != nil {
				return ProcedureResult{}, err
			}
			outcome := abortedOutcome(toolCall)
			if call.Status != session.ToolCallPlanned {
				checkpoint, err := readCheckpoint(ctx, lane, drive, *call)
				if err != nil {
					return ProcedureResult{}, err
				}
				outcome = interruptedOutcome(toolCall, checkpoint)
			}
			if err := publishToolOutcome(ctx, lane, drive, *current, *call, outcome, recovery); err != nil {
				return ProcedureResult{}, err
			}
			continue
		}
		if execution == nil {
			return ProcedureResult{}, &session.SessionInvariantError{Message: "Running tool batch is missing execution context"}
		}
		var task toolCallTask
		var err error
		if call.Status == session.ToolCallPlanned {
			task, err = startToolInvocation(ctx, lane, drive, *current, sources, *call, execution.tools, execution.toolContext, recovery)
		} else {
			task, err = recoverToolInvocation(ctx, lane, drive, *current, sources, *call, execution.toolsByName, execution.toolContext, false)
		}
		if err != nil {
			return ProcedureResult{}, err
		}
		if err := <-task.completion; err != nil {
			return ProcedureResult{}, err
		}
	}
	return ProcedureResult{}, &session.SessionInvariantError{Message: "Sequential tool batch exceeded its bounded transition count"}
}

func runParallel(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState, sources ToolBatchSource, execution toolExecutionContext, recovery bool) (ProcedureResult, error) {
	var placement sync.Mutex
	jobs := []<-chan error{}
	var prepareErr error
	for _, call := range run.Batch.Calls {
		if call.Status == session.ToolCallCompleted || call.Status == session.ToolCallOutcomeReady {
			continue
		}
		var task toolCallTask
		var err error
		if call.Status == session.ToolCallPlanned {
			task, err = startToolInvocation(ctx, lane, drive, run, sources, call, execution.tools, execution.toolContext, recovery)
		} else {
			current := currentBatch(lane)
			cancelled := current != nil && current.Control.Status == session.ControlCancelRequested
			task, err = recoverToolInvocation(ctx, lane, drive, run, sources, call, execution.toolsByName, execution.toolContext, cancelled)
		}
		if err != nil {
			prepareErr = err
			break
		}
		job := startToolTask(func() error {
			if err := <-task.completion; err != nil {
				return err
			}
			placement.Lock()
			defer placement.Unlock()
			return MaterializeReady(ctx, lane, drive, run, sources, recovery)
		})
		jobs = append(jobs, job.completion)
	}
	var completionErr error
	for _, job := range jobs {
		if err := <-job; err != nil && completionErr == nil {
			completionErr = err
		}
	}
	if prepareErr != nil {
		return ProcedureResult{}, prepareErr
	}
	if completionErr != nil {
		return ProcedureResult{}, completionErr
	}
	if err := MaterializeReady(ctx, lane, drive, run, sources, recovery); err != nil {
		return ProcedureResult{}, err
	}
	return ProcedureResult{Kind: ProcedureContinue}, nil
}

// RunTools executes, recovers, stages, and source-orders a complete durable tool batch.
func RunTools(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState) (ProcedureResult, error) {
	recovery := false
	for _, call := range run.Batch.Calls {
		recovery = recovery || call.Status == session.ToolCallEffectPending || call.Status == session.ToolCallOutcomeReady
	}
	if recovery {
		if err := lane.EmitBatch(ctx, []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: true, Payload: agentharness.TurnStartPayload{RunID: drive.OperationID, TurnID: run.Batch.TurnID}}}); err != nil {
			return ProcedureResult{}, err
		}
	}
	sources, err := ReadToolBatchSource(ctx, lane, drive, run.Batch)
	if err != nil {
		return ProcedureResult{}, err
	}
	if err := MaterializeReady(ctx, lane, drive, run, sources, recovery); err != nil {
		return ProcedureResult{}, err
	}
	current := currentBatch(lane)
	if current == nil {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	if current.Control.Status == session.ControlCancelRequested {
		return runSequential(ctx, lane, drive, *current, sources, nil, recovery)
	}
	config := lane.ReadConfig()
	active := map[string]bool{}
	for _, name := range run.Batch.Configuration.ActiveToolNames {
		active[name] = true
	}
	execution := toolExecutionContext{tools: []harness.AgentHarnessTool{}, toolsByName: map[string]harness.AgentHarnessTool{}}
	for _, tool := range config.Tools {
		if active[tool.Name] {
			execution.tools = append(execution.tools, tool)
			execution.toolsByName[tool.Name] = tool
		}
	}
	config = lane.ReadConfig()
	if config.ToolContext != nil {
		execution.toolContext, err = config.ToolContext(ctx)
		if err != nil {
			return ProcedureResult{}, err
		}
	}
	if run.Settings.ToolExecution == session.ToolExecutionSequential {
		return runSequential(ctx, lane, drive, *current, sources, &execution, recovery)
	}
	return runParallel(ctx, lane, drive, *current, sources, execution, recovery)
}
