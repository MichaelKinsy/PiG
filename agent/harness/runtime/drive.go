package runtime

// Ports packages/agent/src/harness/runtime/drive.ts

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

func currentDriveOperation(lane *Lane, drive *Drive) (*session.Operation, error) {
	operation := lane.SnapshotState().Operation
	if operation == nil || operation.Meta.OperationID != drive.OperationID {
		return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Drive %s has no matching current operation", drive.OperationID)}
	}
	return operation, nil
}

// DriveOperation runs one installed pass through durable procedures until settlement or a durable wait.
func DriveOperation(ctx harness.Context, lane *Lane, drive *Drive) (agentharness.DriveOutcome, error) {
	operation, err := currentDriveOperation(lane, drive)
	if err != nil {
		return agentharness.DriveOutcome{}, err
	}
	if operation.State.Control.Status == session.ControlRunning {
		err := lane.Hooks.RunBeforeDrive(ctx, drive.Gate, agentharness.BeforeDriveEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Operation: agentharness.OperationKind(operation.Meta.Intent.Kind)})
		if err != nil {
			var aborted *AbortRequested
			if !errors.As(err, &aborted) {
				return agentharness.DriveOutcome{}, err
			}
			if err := aborted.Cancellation(); err != nil {
				return agentharness.DriveOutcome{}, err
			}
		}
	}
	for {
		operation, err = currentDriveOperation(lane, drive)
		if err != nil {
			return agentharness.DriveOutcome{}, err
		}
		state := operation.State
		var result ProcedureResult
		if state.Control.Status == session.ControlCancelRequested {
			result, err = ReconcileOperation(ctx, lane, drive)
		} else {
			switch state.At {
			case session.AtStarting:
				result, err = StartRun(ctx, lane, drive, state)
			case session.AtCheckpoint:
				result, err = RunCheckpoint(ctx, lane, drive, state)
			case session.AtAssistantReady, session.AtAssistantRetryWait:
				result, err = RunGeneration(ctx, lane, drive, state)
			case session.AtAssistantEffectPending:
				result, err = RecoverAssistantGeneration(ctx, lane, drive, state)
			case session.AtTools:
				result, err = RunTools(ctx, lane, drive, state)
			case session.AtDeferredSuspended, session.AtDeferredEffectPending:
				result, err = RunDeferred(ctx, lane, drive, state)
			case session.AtSummaryDeciding:
				result, err = RunStructuralDecision(ctx, lane, drive, state)
			case session.AtSummaryReady:
				result, err = RunStructuralGeneration(ctx, lane, drive, state)
			case session.AtSummaryEffectPending:
				result, err = RecoverStructuralGeneration(ctx, lane, drive, state)
			case session.AtSummaryRetryWait:
				result, err = RunStructuralRetryWait(ctx, lane, drive, state)
			case session.AtNavigationReadyToCommit:
				result, err = CommitNavigation(ctx, lane, drive, state)
			default:
				return agentharness.DriveOutcome{}, &session.SessionInvariantError{Message: fmt.Sprintf("Invalid operation state %q", state.At)}
			}
		}
		if err != nil {
			var aborted *AbortRequested
			if !errors.As(err, &aborted) {
				return agentharness.DriveOutcome{}, err
			}
			if err := aborted.Cancellation(); err != nil {
				return agentharness.DriveOutcome{}, err
			}
			result = ProcedureResult{Kind: ProcedureContinue}
		}
		if result.Kind == ProcedureSettled {
			return agentharness.DriveOutcome{Kind: agentharness.DriveSettled, Outcome: &result.Record}, nil
		}
		if result.Kind == ProcedureWaiting {
			return result.Outcome, nil
		}
		next, err := currentDriveOperation(lane, drive)
		if err != nil {
			return agentharness.DriveOutcome{}, err
		}
		if next == operation && next.State.Control.Status != session.ControlCancelRequested {
			return agentharness.DriveOutcome{}, &session.SessionInvariantError{Message: "Drive procedure made no progress from " + string(state.At)}
		}
	}
}
