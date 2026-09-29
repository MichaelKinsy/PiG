package runtime

// Ports packages/agent/src/harness/runtime/lane.ts.

import (
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// laneWatch carries the mutable initial snapshot separately from the event subscription installed before capture.
type laneWatch struct {
	agentharness.WatchHandle[agentharness.LaneSnapshot]
	mu       sync.Mutex
	snapshot agentharness.LaneSnapshot
}

func (watch *laneWatch) Snapshot() agentharness.LaneSnapshot {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	return watch.snapshot
}

func (watch *laneWatch) Resnapshot(ctx harness.Context) (agentharness.LaneSnapshot, error) {
	snapshot, err := watch.WatchHandle.Resnapshot(ctx)
	if err != nil {
		return agentharness.LaneSnapshot{}, err
	}
	watch.mu.Lock()
	watch.snapshot = snapshot
	watch.mu.Unlock()
	return snapshot, nil
}

// Watch installs a buffered subscription and captures its snapshot under the same Session read barrier. A failed capture unsubscribes before faulting the lane.
func (lane *Lane) Watch(ctx harness.Context) (agentharness.WatchHandle[agentharness.LaneSnapshot], error) {
	raw, err := lane.readLane(ctx, func(state LaneState, reader session.SessionReader) (any, error) {
		handle, err := lane.installWatch(ctx, agentharness.LaneSnapshot{}, func(event agentharness.HarnessEvent) bool {
			return event.Type() == agentharness.EventUsage || event.Lane == "" || event.Lane == lane.Name()
		}, func(ctx harness.Context, markBoundary func() error) (agentharness.LaneSnapshot, error) {
			raw, err := lane.readLane(ctx, func(latest LaneState, reader session.SessionReader) (any, error) {
				snapshot, err := lane.captureLaneSnapshot(ctx, latest, reader)
				if err != nil {
					return nil, err
				}
				if err := markBoundary(); err != nil {
					return nil, err
				}
				return snapshot, nil
			})
			if err != nil {
				return agentharness.LaneSnapshot{}, err
			}
			return raw.(agentharness.LaneSnapshot), nil
		})
		if err != nil {
			return nil, err
		}
		snapshot, err := lane.captureLaneSnapshot(ctx, state, reader)
		if err != nil {
			handle.Unsubscribe()
			return nil, err
		}
		return &laneWatch{WatchHandle: handle, snapshot: snapshot}, nil
	})
	if err != nil {
		return nil, err
	}
	return raw.(agentharness.WatchHandle[agentharness.LaneSnapshot]), nil
}

func (lane *Lane) captureLaneSnapshot(ctx harness.Context, state LaneState, reader session.SessionReader) (agentharness.LaneSnapshot, error) {
	captured := agentharness.CloneValue(state)
	transcript := []session.Entry{}
	if captured.TipID != nil {
		entries, err := reader.ScanBranch(ctx, session.StorageBranchScan{Start: *captured.TipID, StopAtType: session.EntryTypeCompaction, Order: session.OrderNewestFirst})
		if err != nil {
			return agentharness.LaneSnapshot{}, err
		}
		slices.Reverse(entries)
		transcript = entries
	}
	queues, err := ReadLaneQueues(ctx, reader, captured.Inbox)
	if err != nil {
		return agentharness.LaneSnapshot{}, err
	}
	var lastResult *session.OperationResultRecord
	if captured.LastOperationID != nil {
		stored, err := session.GetValue(ctx, reader, session.OperationResult(*captured.LastOperationID))
		if err != nil {
			return agentharness.LaneSnapshot{}, err
		}
		if stored == nil {
			return agentharness.LaneSnapshot{}, &session.SessionInvariantError{Message: "Lane " + restoreQuote(lane.Name()) + " is missing result " + *captured.LastOperationID}
		}
		lastResult = &stored.Value
	}
	stats, err := reader.GetStats(ctx)
	if err != nil {
		return agentharness.LaneSnapshot{}, err
	}
	var snapshot *agentharness.LaneSnapshotOperation
	if operation := captured.Operation; operation != nil {
		snapshot = &agentharness.LaneSnapshotOperation{ID: operation.Meta.OperationID, Kind: agentharness.OperationKind(operation.Meta.Intent.Kind), StartedAt: operation.Meta.StartedAt, FromTipID: operation.Meta.SourceTipID, Status: agentharness.OperationStatusOpen, RunningTools: []agentharness.LaneSnapshotTool{}}
		if operation.State.Control.Status == session.ControlCancelRequested {
			snapshot.Status = agentharness.OperationStatusAborting
		}
		readStreamingMessage := func(responseID string) error {
			frames, err := ReadAssistantFrames(ctx, reader, operation.Meta.OperationID, responseID)
			if err != nil {
				return err
			}
			message, err := ai.ReduceAssistantMessageFrames(frames)
			if err != nil {
				return err
			}
			if message != nil {
				converted := runtimeAssistantMessage(message)
				snapshot.StreamingMessage = &converted
			}
			return nil
		}
		state := operation.State
		switch state.At {
		case session.AtAssistantRetryWait:
			snapshot.Retry = &agentharness.LaneSnapshotRetry{Attempt: state.NextAttempt, MaxAttempts: state.GenerationContext.RetryPolicy.MaxAttempts, NextAttemptAt: state.NotBefore}
		case session.AtAssistantEffectPending:
			if err := readStreamingMessage(state.ResponseEntryID); err != nil {
				return agentharness.LaneSnapshot{}, err
			}
		case session.AtDeferredSuspended, session.AtDeferredEffectPending:
			entries, err := reader.GetEntries(ctx, []string{state.SourceEntryID})
			if err != nil {
				return agentharness.LaneSnapshot{}, err
			}
			source := entries[state.SourceEntryID]
			if source.Type != session.EntryTypeMessage || source.Message.Assistant == nil || source.Message.Assistant.Deferred == nil {
				return agentharness.LaneSnapshot{}, &session.SessionInvariantError{Message: "Deferred source is missing its assistant handle"}
			}
			snapshot.Deferred = &agentharness.LaneSnapshotDeferred{Handle: *source.Message.Assistant.Deferred, Poll: state.Poll}
			if state.At == session.AtDeferredEffectPending {
				if err := readStreamingMessage(state.ResponseEntryID); err != nil {
					return agentharness.LaneSnapshot{}, err
				}
			}
		case session.AtTools:
			tools, err := captureRunningTools(ctx, reader, operation.Meta.OperationID, state.Batch)
			if err != nil {
				return agentharness.LaneSnapshot{}, err
			}
			snapshot.RunningTools = tools
		case session.AtSummaryRetryWait:
			snapshot.Retry = &agentharness.LaneSnapshotRetry{Attempt: state.NextAttempt, MaxAttempts: state.SummaryContext.RetryPolicy.MaxAttempts, NextAttemptAt: state.NotBefore}
		}
	}
	_, faulted := lane.AssertOpen().(*harness.HarnessFault) //nolint:errorlint // Upstream instanceof inspects the outer error, not a wrapped cause.
	return agentharness.CloneValue(agentharness.LaneSnapshot{Lane: lane.Name(), Transcript: transcript, TipID: captured.TipID, LastResult: lastResult, Configuration: captured.Configuration, Stats: stats, Operation: snapshot, Queues: queues, Faulted: faulted}), nil
}

func captureRunningTools(ctx harness.Context, reader session.SessionReader, operationID string, batch session.ToolBatch) ([]agentharness.LaneSnapshotTool, error) {
	entries, err := reader.GetEntries(ctx, []string{batch.AssistantEntryID})
	if err != nil {
		return nil, err
	}
	assistant := entries[batch.AssistantEntryID]
	if assistant.Type != session.EntryTypeMessage || assistant.Message.Assistant == nil {
		return nil, &session.SessionInvariantError{Message: "Tool batch assistant entry is invalid"}
	}
	tools := []agentharness.LaneSnapshotTool{}
	for _, call := range batch.Calls {
		if call.Status == session.ToolCallPlanned || call.Status == session.ToolCallCompleted {
			continue
		}
		var block ai.ToolCall
		var valid bool
		if call.SourceIndex >= 0 && call.SourceIndex < len(assistant.Message.Assistant.Content) {
			block, valid = assistant.Message.Assistant.Content[call.SourceIndex].(ai.ToolCall)
		}
		if !valid {
			return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d does not name a tool-call block", call.SourceIndex)}
		}
		args, err := session.GetValue(ctx, reader, session.OperationToolArgs(operationID, batch.TurnID, call.SourceIndex))
		if err != nil {
			return nil, err
		}
		if call.Status == session.ToolCallEffectPending {
			if args == nil {
				return nil, &session.SessionInvariantError{Message: "Tool call " + block.ID + " is missing persisted arguments"}
			}
			checkpoint, err := session.GetValue(ctx, reader, session.PendingToolOutput(operationID, call.ResultEntryID))
			if err != nil {
				return nil, err
			}
			tool := agentharness.LaneSnapshotTool{Status: "running", ToolCallID: block.ID, ToolName: block.Name, Args: args.Value}
			if checkpoint != nil {
				tool.Result = &checkpoint.Value
			}
			tools = append(tools, tool)
			continue
		}
		staged, err := session.GetValue(ctx, reader, session.PendingEntryValue(call.ResultEntryID))
		if err != nil {
			return nil, err
		}
		if staged == nil || staged.Value.Type != session.PendingEntryMessage || staged.Value.Message.ToolResult == nil {
			return nil, &session.SessionInvariantError{Message: "Tool call " + call.ResultEntryID + " is missing its staged result"}
		}
		message := staged.Value.Message.ToolResult
		if message.ToolCallID != block.ID || message.ToolName != block.Name {
			return nil, &session.SessionInvariantError{Message: "Tool call " + call.ResultEntryID + " has a mismatched staged result"}
		}
		result := execution.ToolResultFromMessage(*message, call.Terminate)
		tool := agentharness.LaneSnapshotTool{Status: "settled", ToolCallID: block.ID, ToolName: block.Name, Args: block.Arguments, Result: &result, IsError: message.IsError}
		if args != nil && args.Value != nil {
			tool.Args = args.Value
		}
		tools = append(tools, tool)
	}
	return tools, nil
}
