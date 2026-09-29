package runtime

// Ports packages/agent/src/harness/runtime/drive/deferred.ts

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// ReadDeferredSourceHandle reads and validates the captured provider handle from the durable source entry.
func ReadDeferredSourceHandle(ctx harness.Context, reader session.SessionReader, deferred session.OperationState) (ai.DeferredHandle, error) {
	entries, err := reader.GetEntries(ctx, []string{deferred.SourceEntryID})
	if err != nil {
		return ai.DeferredHandle{}, err
	}
	source, ok := entries[deferred.SourceEntryID]
	if !ok || source.Type != session.EntryTypeMessage || source.Message.Assistant == nil || source.Message.Assistant.StopReason != ai.StopReasonDeferred || source.Message.Assistant.Deferred == nil {
		return ai.DeferredHandle{}, &session.SessionInvariantError{Message: "Deferred source " + deferred.SourceEntryID + " is missing its assistant handle"}
	}
	handle := *source.Message.Assistant.Deferred
	identity := deferred.Configuration.Model
	if handle.ID == "" || handle.Provider != identity.Provider || handle.ModelID != identity.ModelID || handle.API != source.Message.Assistant.API {
		return ai.DeferredHandle{}, &session.SessionInvariantError{Message: "Deferred source " + deferred.SourceEntryID + " has an invalid handle"}
	}
	return handle, nil
}

type preparedDeferredPoll struct {
	source        ai.DeferredHandle
	model         *ai.Model
	poll          int
	streamOptions harness.AgentHarnessStreamOptions
}

func publishPollIntent(ctx harness.Context, lane *Lane, drive *Drive, deferred session.OperationState, prepared preparedDeferredPoll, recovery bool) (ContinueOperationResult[session.OperationState], error) {
	at := runtimeNow()
	responseID := lane.Session.IdGenerator().Next(&at)
	usageID := lane.Session.IdGenerator().Next(&at)
	return ContinueOperation(ctx, lane, deferred, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[session.OperationState], error) {
		next := session.OperationState{OperationScope: current.OperationScope, At: session.AtDeferredEffectPending, StepID: deferred.StepID, SourceEntryID: deferred.SourceEntryID, Poll: prepared.poll, ResponseEntryID: responseID, UsageID: usageID, Configuration: deferred.Configuration, StreamOptions: deferred.StreamOptions}
		writes := []session.Write{}
		if deferred.At == session.AtDeferredEffectPending {
			writes = append(writes, session.DeleteList(session.PendingAssistantFrames(drive.OperationID, deferred.ResponseEntryID)))
		}
		return OperationCommand[session.OperationState]{Kind: CommandCommit, Writes: writes, OperationState: next, Materialize: func(session.CommitResult) session.OperationState { drive.DeferredPermits--; return next }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{
				{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.RunResumePayload{RunID: drive.OperationID}},
				{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.TurnStartPayload{RunID: drive.OperationID, TurnID: fmt.Sprintf("%s:poll:%d", next.StepID, next.Poll)}},
			}
		}}, nil
	})
}

func performDeferredPoll(ctx harness.Context, lane *Lane, drive *Drive, prepared preparedDeferredPoll, intent session.OperationState, recovery bool) (message *ai.AssistantMessage, err error) {
	response := OpenAssistantResponse(lane, drive, intent.ResponseEntryID, recovery)
	metadata := execution.AssistantResponseMetadata{}
	headers := make(ai.ProviderHeaders, len(prepared.streamOptions.Headers))
	for name, value := range prepared.streamOptions.Headers {
		headers[name] = new(value)
	}
	admitted := harness.WithAbortSignal(ctx, drive.Gate.Signal())
	var stream *ai.AssistantMessageEventStream
	err = drive.Gate.Admit(func() error {
		stream = lane.Models.StreamDeferred(admitted, prepared.model, prepared.source, ai.DeferredFetchOptions{Wait: new(float64(0)), StreamOptions: ai.StreamOptions{
			Signal: drive.Gate.Signal(), TelemetryContext: harness.GetTelemetryContext(admitted), TimeoutMs: prepared.streamOptions.TimeoutMs, MaxRetries: prepared.streamOptions.MaxRetries, MaxRetryDelayMs: prepared.streamOptions.MaxRetryDelayMs, Headers: headers,
			OnPayload: func(payload any, model *ai.Model) (any, error) {
				hook, err := lane.Hooks.RunBeforePayload(ctx, drive.Gate, agentharness.BeforePayloadEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Model: model, Payload: payload})
				if err != nil {
					return nil, err
				}
				if hook != nil {
					return hook.Payload, nil
				}
				return nil, nil
			},
			OnResponse: func(_ context.Context, result ai.ProviderResponse, _ *ai.Model) error {
				metadata = execution.AssistantResponseMetadata{Status: new(result.Status), Headers: result.Headers}
				return nil
			},
		}})
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := response.Close(); closeErr != nil {
			err = closeErr
		}
	}()
	return execution.ConsumeAssistantStream(ctx, stream, response.Observer, func(ctx harness.Context, message *ai.AssistantMessage) (*ai.AssistantMessage, error) {
		return response.AfterResponse(ctx, message, metadata)
	})
}

func pollDeferred(ctx harness.Context, lane *Lane, drive *Drive, expected session.OperationState, recovery bool) (ProcedureResult, error) {
	source, err := ContinueOperation(ctx, lane, expected, func(_ LaneState, _ session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[ai.DeferredHandle], error) {
		handle, err := ReadDeferredSourceHandle(ctx, reader, expected)
		return OperationCommand[ai.DeferredHandle]{Kind: CommandReturn, Result: handle}, err
	})
	if err != nil {
		return ProcedureResult{}, err
	}
	if source.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	if drive.DeferredPermits == 0 {
		return ProcedureResult{Kind: ProcedureWaiting, Outcome: agentharness.DriveOutcome{Kind: agentharness.DriveWaiting, OperationID: drive.OperationID, Reason: agentharness.DriveWaitDeferred, Deferred: &source.Value}}, nil
	}
	identity := expected.Configuration.Model
	model := lane.Models.GetModel(identity.Provider, identity.ModelID)
	if model == nil {
		return PublishConfigurationFailure(ctx, lane, drive, expected, generationConfigurationError("model_unavailable", identity))
	}
	options := expected.StreamOptions
	options.Deferred = &harness.AgentHarnessDeferredOption{Enabled: false}
	poll := expected.Poll
	if expected.At == session.AtDeferredSuspended {
		poll++
	}
	hook, err := lane.Hooks.RunBeforeRequest(ctx, drive.Gate, agentharness.BeforeRequestEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Model: model, Step: "deferred", Attempt: poll, StreamOptions: options})
	if err != nil {
		return ProcedureResult{}, err
	}
	if hook != nil && hook.StreamOptions != nil {
		options = agentharness.ApplyStreamOptionsPatch(options, *hook.StreamOptions)
	}
	options.Deferred = &harness.AgentHarnessDeferredOption{Enabled: false}
	prepared := preparedDeferredPoll{source: source.Value, model: model, poll: poll, streamOptions: options}
	intent, err := publishPollIntent(ctx, lane, drive, expected, prepared, recovery)
	if err != nil {
		return ProcedureResult{}, err
	}
	if intent.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	response, err := performDeferredPoll(ctx, lane, drive, prepared, intent.Value, recovery)
	if err != nil {
		return ProcedureResult{}, err
	}
	return PublishResponse(ctx, lane, drive, intent.Value, response, recovery)
}

// RunDeferredSuspended polls a suspended request only when this drive holds a permit.
func RunDeferredSuspended(ctx harness.Context, lane *Lane, drive *Drive, deferred session.OperationState) (ProcedureResult, error) {
	return pollDeferred(ctx, lane, drive, deferred, false)
}

// RecoverDeferredPoll replaces an unknown poll under fresh ids at the same poll number.
func RecoverDeferredPoll(ctx harness.Context, lane *Lane, drive *Drive, deferred session.OperationState) (ProcedureResult, error) {
	return pollDeferred(ctx, lane, drive, deferred, true)
}

// RunDeferred advances or reports the durable wait for a deferred operation.
func RunDeferred(ctx harness.Context, lane *Lane, drive *Drive, deferred session.OperationState) (ProcedureResult, error) {
	if deferred.At == session.AtDeferredSuspended {
		return RunDeferredSuspended(ctx, lane, drive, deferred)
	}
	return RecoverDeferredPoll(ctx, lane, drive, deferred)
}
