package runtime

// Ports packages/agent/src/harness/runtime/drive/response.ts

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// AssistantResponseLifecycle owns frame persistence, hook mediation, and live message publication for one reserved response.
type AssistantResponseLifecycle struct {
	Observer      execution.AssistantStreamObserver
	AfterResponse func(harness.Context, *ai.AssistantMessage, execution.AssistantResponseMetadata) (*ai.AssistantMessage, error)
	Close         func() error
}

// OpenAssistantResponse persists frames off the provider loop and drains them before after_response.
func OpenAssistantResponse(lane *Lane, drive *Drive, responseEntryID string, recovery bool) AssistantResponseLifecycle {
	progress := OpenFrameProgress(lane, drive, responseEntryID)
	encoder := &ai.AssistantMessageFrameEncoder{}
	emit := func(ctx harness.Context, payload agentharness.HarnessEventPayload) error {
		return lane.EmitBatch(ctx, []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: recovery, Payload: payload}})
	}
	closeResponse := func() error { progress.Seal(); return progress.Drain() }
	encode := func(event ai.AssistantMessageEvent) (ai.AssistantMessageFrame, error) {
		frame, err := encoder.Encode(event)
		if err == nil && frame != nil {
			progress.Write(frame)
		}
		return frame, err
	}
	return AssistantResponseLifecycle{
		Observer: execution.AssistantStreamObserver{
			Start: func(ctx harness.Context, message *ai.AssistantMessage, event ai.StartEvent) error {
				if _, err := encode(event); err != nil {
					return err
				}
				return emit(ctx, agentharness.MessageStartPayload{RunID: drive.OperationID, Message: agent.AgentMessage{Assistant: new(runtimeAssistantMessage(message))}})
			},
			Update: func(ctx harness.Context, message *ai.AssistantMessage, event ai.AssistantMessageEvent) error {
				frame, err := encode(event)
				if err != nil {
					return err
				}
				return emit(ctx, agentharness.MessageUpdatePayload{RunID: drive.OperationID, Message: agent.AgentMessage{Assistant: new(runtimeAssistantMessage(message))}, Event: event, Frame: frame})
			},
			End: func(ctx harness.Context, message *ai.AssistantMessage) error {
				return emit(ctx, agentharness.MessageEndPayload{RunID: drive.OperationID, EntryID: responseEntryID, Message: agent.AgentMessage{Assistant: new(runtimeAssistantMessage(message))}})
			},
		},
		AfterResponse: func(ctx harness.Context, message *ai.AssistantMessage, metadata execution.AssistantResponseMetadata) (*ai.AssistantMessage, error) {
			if err := closeResponse(); err != nil {
				return nil, err
			}
			result, err := lane.Hooks.RunAfterResponse(ctx, drive.Gate, agentharness.AfterResponseEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Status: metadata.Status, Headers: metadata.Headers, Message: runtimeAssistantMessage(message)})
			if err != nil {
				return nil, err
			}
			if result != nil && result.Message != nil {
				return new(result.Message.LLMMessage()), nil
			}
			return message, nil
		},
		Close: closeResponse,
	}
}

// PublishConfigurationFailure settles a non-retryable configuration failure without reserving response ids.
func PublishConfigurationFailure(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState, failure session.OperationError) (ProcedureResult, error) {
	result, err := ContinueOperation(ctx, lane, capability, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[ProcedureResult], error) {
		if state.TipID == nil {
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Failed run has no Branch tip"}
		}
		record, err := OperationResultRecord(meta, "failed", state.TipID, &failure)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		cleanup, err := OperationCleanupWrites(ctx, reader, drive.OperationID, current)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		return OperationCommand[ProcedureResult]{Kind: CommandFinish, Writes: cleanup, Record: record,
			Materialize: func(session.CommitResult) ProcedureResult {
				return ProcedureResult{Kind: ProcedureSettled, Record: record}
			},
			Events: func(session.CommitResult) []agentharness.HarnessEvent {
				return []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.RunEndPayload{RunID: drive.OperationID, Status: "failed", Error: &failure, FromTipID: meta.SourceTipID, TipID: state.TipID, EndedAt: record.EndedAt}}}
			},
		}, nil
	})
	if result.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, err
	}
	return result.Value, err
}

func uuidV7Timestamp(id string) (int64, error) {
	if len(id) < 13 {
		return 0, &session.SessionInvariantError{Message: "Invalid reserved UUIDv7 " + id}
	}
	value, err := strconv.ParseInt(strings.ReplaceAll(id[:13], "-", ""), 16, 64)
	if err != nil {
		return 0, &session.SessionInvariantError{Message: "Invalid reserved UUIDv7 " + id}
	}
	return value, nil
}

func responseProviderError(source string, message ai.AssistantMessage) *session.OperationError {
	text := message.ErrorMessage
	if text == "" {
		text = fmt.Sprintf("%s request ended with %s", source, message.StopReason)
	}
	return &session.OperationError{Code: "assistant_error", Message: text}
}

// PublishResponse classifies and atomically settles one assistant generation or deferred poll under its reserved ids.
func PublishResponse(ctx harness.Context, lane *Lane, drive *Drive, intent session.OperationState, response *ai.AssistantMessage, recovery bool) (ProcedureResult, error) {
	overflow := intent.At == session.AtAssistantEffectPending && (ai.IsContextOverflow(*response, intent.ContextWindow) || ai.IsRecoverableLength(*response, intent.IntendedOutputLimit))
	var preparation *StructuralPreparation
	var err error
	if overflow && !intent.GenerationContext.OverflowRecoveryUsed {
		preparation, err = PrepareOverflowCompaction(ctx, lane, drive, intent)
		if err != nil {
			return ProcedureResult{}, err
		}
	}
	return SettleOperation(ctx, lane, intent, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[ProcedureResult], error) {
		assistant := current.At == session.AtAssistantEffectPending
		source := "Deferred"
		configuration := current.Configuration
		turnID := fmt.Sprintf("%s:poll:%d", current.StepID, current.Poll)
		if assistant {
			source = "Assistant"
			configuration = current.GenerationContext.Configuration
			turnID = current.GenerationContext.StepID
		}
		scope := current.OperationScope
		scope.LatestAssistantEntryID = &intent.ResponseEntryID
		committed := *response
		var settled *session.OperationState
		var failure *session.OperationError
		checkpoint := func() *session.OperationState {
			return &session.OperationState{OperationScope: scope, At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationMayFinish, IncludeFinalAssistant: true}, TriggerEntryID: intent.ResponseEntryID}
		}
		switch {
		case current.Control.Status == session.ControlCancelRequested:
			committed.StopReason = ai.StopReasonAborted
			if committed.ErrorMessage == "" {
				committed.ErrorMessage = source + " request was cancelled"
			}
			settled = checkpoint()
		case response.StopReason == ai.StopReasonAborted:
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: source + " response is aborted while durable control is running"}
		case assistant && overflow:
			committed.StopReason = ai.StopReasonError
			if committed.ErrorMessage == "" {
				committed.ErrorMessage = "Assistant request exceeded the context window"
			}
			if current.GenerationContext.OverflowRecoveryUsed || preparation == nil {
				failure = responseProviderError(source, committed)
			} else {
				settled = &session.OperationState{OperationScope: scope, At: session.AtSummaryDeciding, Task: session.SummaryTask{TaskID: preparation.TaskID, Reason: "overflow", Boundary: session.ResultBoundary{Kind: session.BoundaryResumeCheckpoint, ResumeAfter: &session.CheckpointData{Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant, OverflowRecoveryUsed: true}, TriggerEntryID: current.GenerationContext.TriggerEntryID}}}}
			}
		case response.StopReason == ai.StopReasonDeferred:
			handle := response.Deferred
			valid := handle != nil && handle.ID != "" && handle.Provider == configuration.Model.Provider && handle.ModelID == configuration.Model.ModelID && handle.API == response.API
			if assistant && !valid {
				committed.StopReason = ai.StopReasonError
				committed.ErrorMessage = "Provider returned an invalid deferred handle"
				failure = responseProviderError(source, committed)
			} else {
				settled = &session.OperationState{OperationScope: scope, At: session.AtDeferredSuspended, StepID: current.StepID, SourceEntryID: intent.ResponseEntryID, Poll: current.Poll, Configuration: configuration, StreamOptions: current.StreamOptions}
				if assistant {
					settled.StepID = current.GenerationContext.StepID
					settled.Poll = 0
					settled.StreamOptions = current.GenerationContext.StreamOptions
				}
			}
		case response.StopReason == ai.StopReasonError:
			if assistant && (recovery || ai.IsRetryableAssistantError(*response)) && current.Attempt < current.GenerationContext.RetryPolicy.MaxAttempts {
				message := response.ErrorMessage
				if message == "" {
					message = "Assistant request failed"
				}
				settled = &session.OperationState{OperationScope: scope, At: session.AtAssistantRetryWait, GenerationContext: current.GenerationContext, NextAttempt: current.Attempt + 1, NotBefore: RetryNotBefore(current.GenerationContext.RetryPolicy, current.Attempt, runtimeNow()), ErrorMessage: message}
			} else {
				failure = responseProviderError(source, *response)
			}
		default:
			calls := []session.ToolCall{}
			for index, content := range response.Content {
				if _, ok := content.(ai.ToolCall); !ok {
					continue
				}
				timestamp, err := uuidV7Timestamp(intent.ResponseEntryID)
				if err != nil {
					return OperationCommand[ProcedureResult]{}, err
				}
				id := lane.Session.IdGenerator().Next(&timestamp)
				calls = append(calls, session.ToolCall{Status: session.ToolCallPlanned, SourceIndex: index, ResultEntryID: id})
			}
			switch {
			case len(calls) != 0:
				settled = &session.OperationState{OperationScope: scope, At: session.AtTools, Batch: session.ToolBatch{AssistantEntryID: intent.ResponseEntryID, Configuration: configuration, TurnID: turnID, Calls: calls}}
			case response.StopReason == ai.StopReasonToolUse:
				committed.StopReason = ai.StopReasonError
				committed.ErrorMessage = "Provider reported tool use without any tool calls"
				failure = responseProviderError(source, committed)
			default:
				settled = checkpoint()
			}
		}
		entry := session.Entry{ID: intent.ResponseEntryID, ParentID: state.TipID, Type: session.EntryTypeMessage, Message: agent.AgentMessage{Assistant: new(runtimeAssistantMessage(&committed))}}
		usage := session.UsageRow{ID: intent.UsageID, Usage: committed.Usage, EntryID: &intent.ResponseEntryID}
		var record *session.OperationResultRecord
		cleanup := []session.Write{session.DeleteList(session.PendingAssistantFrames(drive.OperationID, intent.ResponseEntryID))}
		if failure != nil {
			value, err := OperationResultRecord(meta, "failed", &intent.ResponseEntryID, failure)
			if err != nil {
				return OperationCommand[ProcedureResult]{}, err
			}
			record = &value
			cleanup, err = OperationCleanupWrites(ctx, reader, drive.OperationID, current)
			if err != nil {
				return OperationCommand[ProcedureResult]{}, err
			}
		}
		writes := []session.Write{session.InsertEntry(entry), session.InsertUsage(usage), session.SetValue(session.BranchTip(lane.Name()), &intent.ResponseEntryID)}
		writes = append(writes, cleanup...)
		if settled != nil && settled.At == session.AtSummaryDeciding && preparation != nil {
			writes = append(writes, session.SetValue(session.OperationPreparation(drive.OperationID, preparation.TaskID), preparation.Preparation))
		}
		events := func(commit session.CommitResult) []agentharness.HarnessEvent {
			placed := entry
			placed.Seq = commit.Seqs[0]
			placed.Timestamp = commit.Timestamp
			row := usage
			row.Seq = commit.Seqs[1]
			batch := []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.EntryAddedPayload{Entry: placed}}, {Lane: lane.Name(), Payload: agentharness.UsagePayload{Row: row, Totals: commit.Stats.Usage}}}
			add := func(payload agentharness.HarnessEventPayload, recovered bool) {
				batch = append(batch, agentharness.HarnessEvent{Lane: lane.Name(), Recovery: recovered, Payload: payload})
			}
			at := session.OperationAt("")
			if settled != nil {
				at = settled.At
			}
			if assistant {
				if !recovery && current.Attempt > 1 && at != session.AtAssistantRetryWait {
					success := committed.StopReason != ai.StopReasonError && committed.StopReason != ai.StopReasonAborted
					var finalError *string
					if !success {
						text := committed.ErrorMessage
						if text == "" {
							text = "Assistant request ended with " + string(committed.StopReason)
						}
						finalError = &text
					}
					add(agentharness.RetryEndPayload{RunID: drive.OperationID, Step: turnID, Attempt: current.Attempt, Success: success, FinalError: finalError}, false)
				}
				if !recovery && at == session.AtAssistantRetryWait {
					policy := current.GenerationContext.RetryPolicy
					add(agentharness.RetryScheduledPayload{RunID: drive.OperationID, Step: turnID, Attempt: settled.NextAttempt, MaxAttempts: policy.MaxAttempts, DelayMs: int64(ai.RetryDelayMs(policy.BaseDelayMs, &policy.MaxAgentDelayMs, current.Attempt)), NotBefore: settled.NotBefore, ErrorMessage: settled.ErrorMessage}, false)
				}
				if !recovery && at != session.AtTools && at != session.AtAssistantRetryWait {
					add(agentharness.TurnEndPayload{RunID: drive.OperationID, TurnID: turnID, Message: runtimeAssistantMessage(&committed), ToolResults: []agent.ToolResultMessage{}}, false)
				}
				if at == session.AtSummaryDeciding {
					add(agentharness.CompactionStartPayload{RunID: drive.OperationID, Reason: "overflow", StartedAt: commit.Timestamp}, false)
				}
			} else if at != session.AtTools {
				add(agentharness.TurnEndPayload{RunID: drive.OperationID, TurnID: turnID, Message: runtimeAssistantMessage(&committed), ToolResults: []agent.ToolResultMessage{}}, recovery)
			}
			if (!assistant || !recovery) && at == session.AtDeferredSuspended && committed.Deferred != nil {
				add(agentharness.RunSuspendPayload{RunID: drive.OperationID, Reason: "deferred", Deferred: *committed.Deferred, Poll: settled.Poll}, !assistant && recovery)
			}
			if record != nil {
				add(agentharness.RunEndPayload{RunID: drive.OperationID, Status: "failed", Error: failure, FromTipID: meta.SourceTipID, TipID: &intent.ResponseEntryID, EndedAt: record.EndedAt}, false)
			}
			return batch
		}
		command := OperationCommand[ProcedureResult]{Kind: CommandCommit, Writes: writes, Lane: &LanePatch{SetTipID: true, TipID: &intent.ResponseEntryID}, Events: events, Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }}
		switch {
		case record != nil:
			command.Kind = CommandFinish
			command.Record = *record
			command.Materialize = func(session.CommitResult) ProcedureResult {
				return ProcedureResult{Kind: ProcedureSettled, Record: *record}
			}
		case settled != nil:
			command.OperationState = *settled
		default:
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Response settlement is missing its next state"}
		}
		return command, nil
	})
}
