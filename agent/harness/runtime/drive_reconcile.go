package runtime

// Ports packages/agent/src/harness/runtime/drive/reconcile.ts.

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func cancelDeferredBestEffort(lane *Lane, drive *Drive, deferred session.OperationState, handle ai.DeferredHandle) {
	identity := deferred.Configuration.Model
	model := lane.Models.GetModel(identity.Provider, identity.ModelID)
	if model == nil {
		return
	}
	options := ai.DeferredCancelOptions{Signal: drive.CloseSignal, TelemetryContext: harness.GetTelemetryContext(drive.Context), TimeoutMs: deferred.StreamOptions.TimeoutMs, MaxRetries: deferred.StreamOptions.MaxRetries, MaxRetryDelayMs: deferred.StreamOptions.MaxRetryDelayMs}
	if deferred.StreamOptions.Headers != nil {
		options.Headers = make(ai.ProviderHeaders, len(deferred.StreamOptions.Headers))
		for key, value := range deferred.StreamOptions.Headers {
			options.Headers[key] = new(value)
		}
	}
	// upstream: packages/agent/src/harness/runtime/drive/reconcile.ts:cancelDeferredBestEffort
	_ = lane.Models.CancelDeferred(harness.WithAbortSignal(drive.Context, drive.CloseSignal), model, handle, options)
}

func readDeferredHandle(ctx harness.Context, lane *Lane, drive *Drive, deferred session.OperationState) (ai.DeferredHandle, error) {
	return SettleOperation(ctx, lane, deferred, func(_ LaneState, _ session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[ai.DeferredHandle], error) {
		handle, err := ReadDeferredSourceHandle(drive.Context, reader, deferred)
		if err != nil {
			return OperationCommand[ai.DeferredHandle]{}, err
		}
		return OperationCommand[ai.DeferredHandle]{Kind: CommandReturn, Result: handle}, nil
	})
}

func publishAbortedTerminal(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState) (ProcedureResult, error) {
	return SettleOperation(ctx, lane, capability, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[ProcedureResult], error) {
		if current.Control.Status != session.ControlCancelRequested {
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Cancellation reconciliation requires cancelled durable control"}
		}
		record, err := OperationResultRecord(meta, session.TerminalAborted, state.TipID, nil)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		cleanup, err := OperationCleanupWrites(ctx, reader, drive.OperationID, current)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		events := []agentharness.HarnessEvent{}
		switch meta.Intent.Kind {
		case session.OperationKindRun:
			switch current.At {
			case session.AtSummaryDeciding, session.AtSummaryReady, session.AtSummaryEffectPending, session.AtSummaryRetryWait:
				if current.Task.Boundary.Kind != session.BoundaryResumeCheckpoint || current.Task.Reason == "" {
					return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Cancelled run summary has an invalid result boundary"}
				}
				events = append(events, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.CompactionEndPayload{RunID: drive.OperationID, Reason: current.Task.Reason, Status: session.TerminalAborted, EndedAt: record.EndedAt}})
			}
			events = append(events, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.RunEndPayload{RunID: drive.OperationID, Status: session.TerminalAborted, FromTipID: meta.SourceTipID, TipID: state.TipID, EndedAt: record.EndedAt}})
		case session.OperationKindCompaction:
			events = append(events, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.CompactionEndPayload{RunID: drive.OperationID, Reason: session.SummaryReasonManual, Status: session.TerminalAborted, EndedAt: record.EndedAt}})
		default:
			events = append(events, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.NavigationEndPayload{RunID: drive.OperationID, Status: session.TerminalAborted, FromTipID: meta.SourceTipID, TipID: state.TipID, EndedAt: record.EndedAt}})
		}
		return OperationCommand[ProcedureResult]{Kind: CommandFinish, Writes: cleanup, Record: record, Materialize: func(session.CommitResult) ProcedureResult {
			return ProcedureResult{Kind: ProcedureSettled, Record: record}
		}, Events: func(session.CommitResult) []agentharness.HarnessEvent { return events }}, nil
	})
}

// ReconcileOperation advances a cancelled durable leaf without admitting ordinary work.
func ReconcileOperation(ctx harness.Context, lane *Lane, drive *Drive) (ProcedureResult, error) {
	operation := lane.SnapshotState().Operation
	if operation == nil || operation.Meta.OperationID != drive.OperationID {
		return ProcedureResult{}, &session.SessionInvariantError{Message: fmt.Sprintf("Drive %s has no matching operation to reconcile", drive.OperationID)}
	}
	if operation.State.Control.Status != session.ControlCancelRequested {
		return ProcedureResult{}, &session.SessionInvariantError{Message: fmt.Sprintf("Operation %s is not cancelled", drive.OperationID)}
	}
	drive.BeginAbort(func() error { return nil })
	drive.SignalAbort()
	state := operation.State
	switch state.At {
	case session.AtAssistantEffectPending:
		return RecoverCancelledAssistantEffect(ctx, lane, drive, state)
	case session.AtTools:
		return RunTools(ctx, lane, drive, state)
	case session.AtDeferredSuspended, session.AtDeferredEffectPending:
		handle, err := readDeferredHandle(ctx, lane, drive, state)
		if err != nil {
			return ProcedureResult{}, err
		}
		cancelDeferredBestEffort(lane, drive, state, handle)
		if state.At == session.AtDeferredEffectPending {
			return RecoverCancelledAssistantEffect(ctx, lane, drive, state)
		}
		return publishAbortedTerminal(ctx, lane, drive, state)
	case session.AtStarting, session.AtCheckpoint, session.AtAssistantReady, session.AtAssistantRetryWait, session.AtSummaryDeciding, session.AtSummaryReady, session.AtSummaryEffectPending, session.AtSummaryRetryWait, session.AtNavigationReadyToCommit:
		return publishAbortedTerminal(ctx, lane, drive, state)
	default:
		return ProcedureResult{}, &session.SessionInvariantError{Message: fmt.Sprintf("Unknown operation state %s", state.At)}
	}
}
