package runtime

// Ports packages/agent/src/harness/runtime/lane.ts.

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func (lane *Lane) GetTipID(harness.Context) (*string, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	return lane.SnapshotState().TipID, nil
}
func (lane *Lane) GetResult(ctx harness.Context, operationID string) (*session.OperationResultRecord, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	stored, err := session.GetValue(ctx, lane.Session, session.OperationResult(operationID))
	if err != nil || stored == nil {
		return nil, err
	}
	return &stored.Value, nil
}
func (lane *Lane) GetModel(harness.Context) (*ai.Model, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	model := lane.SnapshotState().Configuration.Model
	return lane.Models.GetModel(model.Provider, model.ModelID), nil
}
func (lane *Lane) GetThinkingLevel(harness.Context) (ai.ThinkingLevel, error) {
	if err := lane.AssertOpen(); err != nil {
		return "", err
	}
	return lane.SnapshotState().Configuration.ThinkingLevel, nil
}
func (lane *Lane) GetActiveTools(harness.Context) ([]string, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	return lane.SnapshotState().Configuration.ActiveToolNames, nil
}
func (lane *Lane) SetModel(ctx harness.Context, model agentharness.ModelIdentity) error {
	return lane.setConfiguration(ctx, func(config session.LaneConfiguration) session.LaneConfiguration { config.Model = model; return config }, func(previous, value session.LaneConfiguration) agentharness.ConfigUpdatePayload {
		return agentharness.ConfigUpdatePayload{Property: agentharness.ConfigModel, Previous: previous.Model, Value: value.Model}
	})
}
func (lane *Lane) SetThinkingLevel(ctx harness.Context, level ai.ThinkingLevel) error {
	return lane.setConfiguration(ctx, func(config session.LaneConfiguration) session.LaneConfiguration {
		config.ThinkingLevel = level
		return config
	}, func(previous, value session.LaneConfiguration) agentharness.ConfigUpdatePayload {
		return agentharness.ConfigUpdatePayload{Property: agentharness.ConfigThinkingLevel, Previous: previous.ThinkingLevel, Value: value.ThinkingLevel}
	})
}
func (lane *Lane) SetActiveTools(ctx harness.Context, names []string) error {
	return lane.setConfiguration(ctx, func(config session.LaneConfiguration) session.LaneConfiguration {
		config.ActiveToolNames = names
		return config
	}, func(previous, value session.LaneConfiguration) agentharness.ConfigUpdatePayload {
		return agentharness.ConfigUpdatePayload{Property: agentharness.ConfigActiveTools, Previous: previous.ActiveToolNames, Value: value.ActiveToolNames}
	})
}
func (lane *Lane) setConfiguration(ctx harness.Context, update func(session.LaneConfiguration) session.LaneConfiguration, event func(session.LaneConfiguration, session.LaneConfiguration) agentharness.ConfigUpdatePayload) error {
	_, err := lane.Command(ctx, func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
		next := state
		next.Configuration = update(state.Configuration)
		return LaneCommand[any]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.LaneConfig(lane.name), next.Configuration)}, Next: next, Materialize: func(session.CommitResult) any { return nil }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.name, Payload: event(state.Configuration, next.Configuration)}}
		}}, nil
	})
	return err
}

func capturedModel(operation *session.Operation) *agentharness.ModelIdentity {
	state := operation.State
	var identity agentharness.ModelIdentity
	switch state.At {
	case session.AtAssistantReady, session.AtAssistantEffectPending, session.AtAssistantRetryWait:
		identity = state.GenerationContext.Configuration.Model
	case session.AtTools:
		identity = state.Batch.Configuration.Model
	case session.AtDeferredSuspended, session.AtDeferredEffectPending:
		identity = state.Configuration.Model
	case session.AtSummaryReady, session.AtSummaryEffectPending, session.AtSummaryRetryWait:
		identity = state.SummaryContext.Configuration.Model
	default:
		return nil
	}
	return &identity
}

func (lane *Lane) InspectExecution(ctx harness.Context) (agentharness.LaneExecutionInfo, error) {
	raw, err := lane.readLane(ctx, func(state LaneState, _ session.SessionReader) (any, error) {
		result := agentharness.LaneExecutionInfo{Lane: lane.name, TipID: state.TipID, ConfiguredModel: state.Configuration.Model, LastOperationID: state.LastOperationID}
		if operation := state.Operation; operation != nil {
			status := agentharness.OperationStatusOpen
			if operation.State.Control.Status == session.ControlCancelRequested {
				status = agentharness.OperationStatusAborting
			}
			result.Current = &agentharness.CurrentOperationInfo{ID: operation.Meta.OperationID, Kind: agentharness.OperationKind(operation.Meta.Intent.Kind), Status: status, StartedAt: operation.Meta.StartedAt, CapturedModel: capturedModel(operation)}
		}
		return result, nil
	})
	if err != nil {
		return agentharness.LaneExecutionInfo{}, err
	}
	return raw.(agentharness.LaneExecutionInfo), nil
}

type idleObservation struct {
	idle   bool
	drive  *Drive
	change <-chan struct{}
}

func (lane *Lane) idleObservation(state LaneState) idleObservation {
	lane.mu.RLock()
	defer lane.mu.RUnlock()
	return idleObservation{idle: state.Operation == nil && lane.ActiveDrive == nil, drive: lane.ActiveDrive, change: lane.stateChange}
}
func awaitIdleObservation(ctx harness.Context, observation idleObservation) error {
	if observation.drive != nil {
		_, err := observation.drive.Completion(ctx)
		return err
	}
	return harness.AwaitWithContext(ctx, observation.change)
}

// WaitForIdle waits for durable operations even when no drive pass has been installed.
func (lane *Lane) WaitForIdle(ctx harness.Context) error {
	for {
		observation, err := Command(ctx, lane, func(state LaneState, _ session.SessionReader) (LaneCommand[idleObservation], error) {
			return LaneCommand[idleObservation]{Kind: CommandReturn, Result: lane.idleObservation(state)}, nil
		})
		if err != nil {
			return err
		}
		if observation.idle {
			return nil
		}
		if err := awaitIdleObservation(ctx, observation); err != nil {
			return err
		}
	}
}

// RunWhenIdle owns the idle window until callback returns. Reads remain coherent; competing commands wait, and close drains an already-running callback.
func (lane *Lane) RunWhenIdle(ctx harness.Context, callback func(harness.Context) error) (err error) {
	var owner chan struct{}
	for {
		observation, err := Command(ctx, lane, func(state LaneState, _ session.SessionReader) (LaneCommand[idleObservation], error) {
			observation := lane.idleObservation(state)
			if observation.idle {
				lane.mu.Lock()
				owner = make(chan struct{})
				lane.idleOwner = owner
				lane.signalStateChangeLocked()
				lane.mu.Unlock()
			}
			return LaneCommand[idleObservation]{Kind: CommandReturn, Result: observation}, nil
		})
		if err != nil {
			return err
		}
		if observation.idle {
			break
		}
		if err := awaitIdleObservation(ctx, observation); err != nil {
			return err
		}
	}
	defer func() {
		lane.mu.Lock()
		if lane.idleOwner == owner {
			lane.idleOwner = nil
		}
		close(owner)
		lane.signalStateChangeLocked()
		lane.mu.Unlock()
		if recovered := recover(); recovered != nil {
			err = lanePanic(recovered)
		}
	}()
	if err := lane.AssertOpen(); err != nil {
		return err
	}
	return callback(ctx)
}

func (lane *Lane) FindEntries(ctx harness.Context, query *session.BranchScan) ([]session.Entry, error) {
	if err := lane.AssertOpen(); err != nil {
		return nil, err
	}
	scan := session.BranchScan{}
	if query != nil {
		scan = *query
	}
	start := scan.Start
	if start == nil {
		start = lane.SnapshotState().TipID
	}
	if start == nil {
		return []session.Entry{}, nil
	}
	order := scan.Order
	if order == "" {
		order = session.OrderNewestFirst
	}
	return lane.Session.ScanBranch(ctx, session.StorageBranchScan{Start: *start, StopAtType: scan.StopAtType, StopAtID: scan.StopAtID, Type: scan.Type, CustomType: scan.CustomType, Order: order, Limit: scan.Limit, Cursor: scan.Cursor})
}
func (lane *Lane) FindEntry(ctx harness.Context, query *session.BranchScan) (*session.Entry, error) {
	scan := session.BranchScan{}
	if query != nil {
		scan = *query
	}
	limit := 1
	if scan.Limit != nil {
		limit = min(*scan.Limit, 1)
	}
	scan.Limit = &limit
	entries, err := lane.FindEntries(ctx, &scan)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return &entries[0], nil
}
func (lane *Lane) Mismatch(expected string, current, last *string) *harness.OperationMismatch {
	return &harness.OperationMismatch{Lane: lane.name, ExpectedOperationID: expected, CurrentOperationID: current, LastOperationID: last, Message: fmt.Sprintf("Operation %s does not own lane %q", expected, lane.name)}
}
func currentOperationID(state LaneState) *string {
	if state.Operation == nil {
		return nil
	}
	return &state.Operation.Meta.OperationID
}

func (lane *Lane) expectedOpen() error {
	err := lane.AssertOpen()
	if _, closed := err.(*harness.HarnessClosed); closed { //nolint:errorlint // Upstream instanceof checks the outer error, not a wrapped cause.
		return &harness.Closed{Message: err.Error()}
	}
	return err
}
