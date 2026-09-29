package runtime

// Ports packages/agent/src/harness/runtime/drive/structural.ts.

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

type structuralCancelled struct{}

func (*structuralCancelled) Error() string { return "Structural generation was cancelled" }

type structuralOutcome struct {
	Kind          string
	ResultEntryID string
	Compaction    compaction.CompactResult
	Branch        compaction.BranchSummaryResult
	FromHook      bool
	Error         *session.OperationError
}

type structuralPublication struct {
	Result        ProcedureResult
	FinishPending *BoundaryFinishPending
}

func summaryKind(task session.SummaryTask) string {
	if task.Boundary.Kind == session.BoundaryCommitNavigation {
		return session.PreparationBranchSummary
	}
	return session.PreparationCompaction
}
func compactionReason(task session.SummaryTask) (string, error) {
	if task.Reason != "" {
		return task.Reason, nil
	}
	if task.Boundary.Kind == session.BoundaryFinish {
		return session.SummaryReasonManual, nil
	}
	return "", &session.SessionInvariantError{Message: fmt.Sprintf("In-run compaction task %s is missing its reason", task.TaskID)}
}
func summaryContext(lane *Lane, resultEntryID string, configuration session.LaneConfiguration) session.SummaryContext {
	options := lane.ReadConfig().StreamOptions
	options.Deferred = &harness.AgentHarnessDeferredOption{Enabled: false}
	return session.SummaryContext{ResultEntryID: resultEntryID, Configuration: configuration, StreamOptions: options, RetryPolicy: NormalizedRetryPolicy(lane)}
}
func usageEvent(row session.UsageRow, index int, commit session.CommitResult, lane string) agentharness.HarnessEvent {
	row.Seq = commit.Seqs[index]
	return agentharness.HarnessEvent{Lane: lane, Payload: agentharness.UsagePayload{Row: row, Totals: commit.Stats.Usage}}
}

func readStructuralPreparation(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState, decision bool) (ContinueOperationResult[session.DurableStructuralPreparation], error) {
	return ContinueOperation(ctx, lane, capability, func(_ LaneState, current session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[session.DurableStructuralPreparation], error) {
		expected := summaryKind(current.Task)
		stored, err := session.GetValue(ctx, reader, session.OperationPreparation(drive.OperationID, current.Task.TaskID))
		if err != nil {
			return OperationCommand[session.DurableStructuralPreparation]{}, err
		}
		if stored == nil || stored.Value.Kind != expected {
			message := fmt.Sprintf("Structural task %s has invalid durable preparation", current.Task.TaskID)
			if decision {
				message = fmt.Sprintf("Structural task %s is missing its %s preparation", current.Task.TaskID, expected)
			}
			return OperationCommand[session.DurableStructuralPreparation]{}, &session.SessionInvariantError{Message: message}
		}
		if decision && current.Task.Boundary.Kind == session.BoundaryCommitNavigation {
			target := current.Task.Boundary.TargetID
			entries, err := reader.GetEntries(ctx, []string{target})
			if err != nil {
				return OperationCommand[session.DurableStructuralPreparation]{}, err
			}
			if _, ok := entries[target]; !ok {
				return OperationCommand[session.DurableStructuralPreparation]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Navigation target %s is missing", target)}
			}
		}
		return OperationCommand[session.DurableStructuralPreparation]{Kind: CommandReturn, Result: stored.Value}, nil
	})
}

func publishStructuralOutcome(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState, outcome structuralOutcome) (ProcedureResult, error) {
	var hookUsage *ai.Usage
	if outcome.FromHook {
		if outcome.Kind == session.PreparationCompaction {
			hookUsage = outcome.Compaction.Usage
		}
		if outcome.Kind == session.PreparationBranchSummary {
			hookUsage = outcome.Branch.Usage
		}
	}
	hookUsageID := ""
	if hookUsage != nil {
		hookUsageID = lane.Session.IdGenerator().Next(nil)
	}
	published, err := ContinueOperation(ctx, lane, capability, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[structuralPublication], error) {
		expected := summaryKind(current.Task)
		if (outcome.Kind == session.PreparationCompaction || outcome.Kind == session.PreparationBranchSummary) && outcome.Kind != expected {
			return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Structural %s result does not match %s task %s", outcome.Kind, expected, current.Task.TaskID)}
		}
		writes := []session.Write{}
		var eventFactories []func(session.CommitResult) []agentharness.HarnessEvent
		terminalTipID := state.TipID
		var terminalCompactionEndedAt *int64
		if hookUsage != nil {
			row := session.UsageRow{ID: hookUsageID, Usage: *hookUsage, Adjustment: false}
			index := len(writes)
			writes = append(writes, session.InsertUsage(row))
			eventFactories = append(eventFactories, func(commit session.CommitResult) []agentharness.HarnessEvent {
				return []agentharness.HarnessEvent{usageEvent(row, index, commit, lane.Name())}
			})
		}
		switch outcome.Kind {
		case session.PreparationCompaction:
			result := outcome.Compaction
			entry := session.Entry{ID: outcome.ResultEntryID, ParentID: state.TipID, Type: session.EntryTypeCompaction, Summary: result.Summary, RetainedTail: result.RetainedTail, TokensBefore: result.TokensBefore, Usage: result.Usage, FromHook: outcome.FromHook}
			if result.Details != nil {
				entry.Details = new(session.JsonValue(result.Details))
			}
			index := len(writes)
			writes = append(writes, session.InsertEntry(entry), session.SetValue(session.BranchTip(lane.Name()), new(entry.ID)))
			terminalTipID = new(entry.ID)
			eventFactories = append(eventFactories, func(commit session.CommitResult) []agentharness.HarnessEvent {
				return CommittedEntryEvents([]session.Entry{entry}, commit, lane.Name(), drive.OperationID, index)
			})
		case session.PreparationBranchSummary:
			boundary := current.Task.Boundary
			if boundary.Kind != session.BoundaryCommitNavigation {
				return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Summary task %s is not a navigation", current.Task.TaskID)}
			}
			result := outcome.Branch
			details := session.JsonValue(map[string]any{"readFiles": result.ReadFiles, "modifiedFiles": result.ModifiedFiles})
			entry := session.Entry{ID: outcome.ResultEntryID, ParentID: new(boundary.TargetID), Type: session.EntryTypeBranchSummary, FromID: meta.SourceTipID, Summary: result.Summary, Details: &details, Usage: result.Usage, FromHook: outcome.FromHook}
			writes = append(writes, session.SetValue(session.BranchTip(lane.Name()), new(boundary.TargetID)))
			index := len(writes)
			writes = append(writes, session.InsertEntry(entry), session.SetValue(session.BranchTip(lane.Name()), new(entry.ID)))
			if boundary.Label != nil {
				writes = append(writes, session.SetValue(session.EntryLabel(boundary.TargetID), *boundary.Label))
			}
			terminalTipID = new(entry.ID)
			eventFactories = append(eventFactories, func(commit session.CommitResult) []agentharness.HarnessEvent {
				return CommittedEntryEvents([]session.Entry{entry}, commit, lane.Name(), drive.OperationID, index)
			})
		}
		attempt := 0
		switch current.At {
		case session.AtSummaryReady:
			attempt = current.NextAttempt
		case session.AtSummaryEffectPending:
			attempt = current.Attempt
		}
		if attempt > 1 {
			payload := agentharness.RetryEndPayload{RunID: drive.OperationID, Step: current.Task.TaskID, Attempt: attempt, Success: outcome.Kind == session.PreparationCompaction || outcome.Kind == session.PreparationBranchSummary}
			if outcome.Kind == "failed" {
				payload.FinalError = new(outcome.Error.Message)
			}
			eventFactories = append(eventFactories, func(session.CommitResult) []agentharness.HarnessEvent {
				return []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: payload}}
			})
		}
		if outcome.Kind == session.PreparationCompaction {
			reason, err := compactionReason(current.Task)
			if err != nil {
				return OperationCommand[structuralPublication]{}, err
			}
			eventFactories = append(eventFactories, func(commit session.CommitResult) []agentharness.HarnessEvent {
				endedAt := commit.Timestamp
				if terminalCompactionEndedAt != nil {
					endedAt = *terminalCompactionEndedAt
				}
				return []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.CompactionEndPayload{RunID: drive.OperationID, Reason: reason, Status: session.TerminalCompleted, EntryID: outcome.ResultEntryID, EndedAt: endedAt}}}
			})
		}
		events := func(commit session.CommitResult) []agentharness.HarnessEvent {
			result := []agentharness.HarnessEvent{}
			for _, factory := range eventFactories {
				result = append(result, factory(commit)...)
			}
			return result
		}
		switch current.Task.Boundary.Kind {
		case session.BoundaryResumeCheckpoint:
			if outcome.Kind == session.PreparationBranchSummary {
				return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Run compaction boundary received a branch summary"}
			}
			resume := current.Task.Boundary.ResumeAfter
			if resume == nil {
				return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Run compaction is missing its resume checkpoint"}
			}
			if outcome.Kind == session.PreparationCompaction || (outcome.Kind == "declined" && current.Task.Reason == session.SummaryReasonThreshold) {
				if terminalTipID == nil {
					return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Run compaction has no Branch tip"}
				}
				continuation := resume.Continuation
				placement, err := PlanBoundaryInbox(ctx, lane, drive, state, current.OperationScope, reader, terminalTipID, outcome.Kind == "declined" && continuation.Kind == session.ContinuationMayFinish)
				if err != nil {
					return OperationCommand[structuralPublication]{}, err
				}
				if outcome.Kind == "declined" && placement.TriggerEntryID == nil && continuation.Kind == session.ContinuationMayFinish {
					ids := make([]string, len(placement.Entries))
					for i, entry := range placement.Entries {
						ids[i] = entry.ID
					}
					return OperationCommand[structuralPublication]{Kind: CommandReturn, Result: structuralPublication{FinishPending: &BoundaryFinishPending{EntryIDs: ids}}}, nil
				}
				index := len(writes)
				writes = append(writes, placement.Writes...)
				var next session.OperationState
				if placement.TriggerEntryID != nil || continuation.Kind == session.ContinuationNeedAssistant {
					trigger := resume.TriggerEntryID
					overflow := continuation.OverflowRecoveryUsed
					if placement.TriggerEntryID != nil {
						trigger = *placement.TriggerEntryID
						overflow = false
					}
					next = AssistantReadyAtBoundary(lane, state, current.OperationScope, trigger, overflow)
				} else {
					next = session.OperationState{OperationScope: current.OperationScope, At: session.AtCheckpoint, Continuation: resume.Continuation, TriggerEntryID: resume.TriggerEntryID}
				}
				return OperationCommand[structuralPublication]{Kind: CommandCommit, Writes: writes, OperationState: next, Lane: &LanePatch{SetTipID: true, TipID: placement.TipID, SetInbox: true, Inbox: placement.Inbox}, Materialize: func(session.CommitResult) structuralPublication {
					return structuralPublication{Result: ProcedureResult{Kind: ProcedureContinue}}
				}, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
					base := events(commit)
					if outcome.Kind != "compaction" {
						base = []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.CompactionEndPayload{RunID: drive.OperationID, Reason: session.SummaryReasonThreshold, Status: session.TerminalDeclined, EndedAt: commit.Timestamp}}}
					}
					return append(base, BoundaryPlacementEvents(placement, commit, index, lane.Name(), drive.OperationID)...)
				}}, nil
			}
			if state.TipID == nil {
				return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Failed run has no Branch tip"}
			}
			failure := outcome.Error
			status := session.TerminalFailed
			if outcome.Kind == "declined" {
				status = session.TerminalDeclined
				failure = &session.OperationError{Code: "compaction_declined", Message: "Overflow compaction was declined"}
			}
			cleanup, err := OperationCleanupWrites(ctx, reader, drive.OperationID, current)
			if err != nil {
				return OperationCommand[structuralPublication]{}, err
			}
			record, err := OperationResultRecord(meta, session.TerminalFailed, state.TipID, failure)
			if err != nil {
				return OperationCommand[structuralPublication]{}, err
			}
			reason, err := compactionReason(current.Task)
			if err != nil {
				return OperationCommand[structuralPublication]{}, err
			}
			end := agentharness.CompactionEndPayload{RunID: drive.OperationID, Reason: reason, Status: status, EndedAt: record.EndedAt}
			if outcome.Kind == "failed" {
				end.Error = outcome.Error
			}
			return OperationCommand[structuralPublication]{Kind: CommandFinish, Writes: append(writes, cleanup...), Record: record, Materialize: func(session.CommitResult) structuralPublication {
				return structuralPublication{Result: ProcedureResult{Kind: ProcedureSettled, Record: record}}
			}, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
				return append(events(commit), agentharness.HarnessEvent{Lane: lane.Name(), Payload: end}, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.RunEndPayload{RunID: drive.OperationID, Status: session.TerminalFailed, Error: failure, FromTipID: meta.SourceTipID, TipID: state.TipID, EndedAt: record.EndedAt}})
			}}, nil
		case session.BoundaryFinish, session.BoundaryCommitNavigation:
			navigation := current.Task.Boundary.Kind == session.BoundaryCommitNavigation
			if !navigation && outcome.Kind == session.PreparationBranchSummary {
				return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Compaction finish boundary received a branch summary"}
			}
			if navigation && outcome.Kind == session.PreparationCompaction {
				return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Navigation boundary received a compaction result"}
			}
			if !navigation && outcome.Kind != session.PreparationCompaction && state.TipID == nil {
				return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Standalone compaction has no Branch tip"}
			}
			status := session.TerminalCompleted
			var failure *session.OperationError
			switch outcome.Kind {
			case "declined":
				status = session.TerminalDeclined
			case "failed":
				status = session.TerminalFailed
				failure = outcome.Error
			}
			cleanup, err := OperationCleanupWrites(ctx, reader, drive.OperationID, current)
			if err != nil {
				return OperationCommand[structuralPublication]{}, err
			}
			record, err := OperationResultRecord(meta, status, terminalTipID, failure)
			if err != nil {
				return OperationCommand[structuralPublication]{}, err
			}
			var patch *LanePatch
			if outcome.Kind == session.PreparationCompaction || outcome.Kind == session.PreparationBranchSummary {
				patch = &LanePatch{SetTipID: true, TipID: terminalTipID}
			}
			if !navigation {
				terminalCompactionEndedAt = &record.EndedAt
			}
			return OperationCommand[structuralPublication]{Kind: CommandFinish, Writes: append(writes, cleanup...), Record: record, Lane: patch, Materialize: func(session.CommitResult) structuralPublication {
				return structuralPublication{Result: ProcedureResult{Kind: ProcedureSettled, Record: record}}
			}, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
				base := events(commit)
				if navigation {
					return append(base, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.NavigationEndPayload{RunID: drive.OperationID, Status: status, Error: failure, FromTipID: meta.SourceTipID, TipID: terminalTipID, EndedAt: record.EndedAt}})
				}
				if outcome.Kind != session.PreparationCompaction {
					base = append(base, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.CompactionEndPayload{RunID: drive.OperationID, Reason: session.SummaryReasonManual, Status: status, Error: failure, EndedAt: record.EndedAt}})
				}
				return base
			}}, nil
		default:
			return OperationCommand[structuralPublication]{}, &session.SessionInvariantError{Message: "Unknown structural result boundary"}
		}
	})
	if err != nil {
		return ProcedureResult{}, err
	}
	if published.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	if published.Value.FinishPending == nil {
		return published.Value.Result, nil
	}
	boundary := capability.Task.Boundary
	if boundary.Kind != session.BoundaryResumeCheckpoint || boundary.ResumeAfter == nil || boundary.ResumeAfter.Continuation.Kind != session.ContinuationMayFinish {
		return ProcedureResult{}, &session.SessionInvariantError{Message: "Structural finish mediation requires a resumable finish boundary"}
	}
	return FinishRunBoundary(ctx, lane, drive, capability, boundary.ResumeAfter.Continuation, published.Value.FinishPending.EntryIDs, []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.CompactionEndPayload{RunID: drive.OperationID, Reason: session.SummaryReasonThreshold, Status: session.TerminalDeclined, EndedAt: runtimeNow()}}})
}

func publishStructuralReady(ctx harness.Context, lane *Lane, drive *Drive, deciding session.OperationState) (ProcedureResult, error) {
	resultEntryID := lane.Session.IdGenerator().Next(nil)
	return continuedProcedure(ContinueOperation(ctx, lane, deciding, func(state LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[ProcedureResult], error) {
		next := session.OperationState{OperationScope: current.OperationScope, At: session.AtSummaryReady, Task: current.Task, SummaryContext: summaryContext(lane, resultEntryID, state.Configuration), NextAttempt: 1}
		return OperationCommand[ProcedureResult]{Kind: CommandCommit, Writes: []session.Write{}, OperationState: next, Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }}, nil
	}))
}

// RunStructuralDecision consumes the durable preparation and its decision hook without publishing stale cancelled output.
func RunStructuralDecision(ctx harness.Context, lane *Lane, drive *Drive, deciding session.OperationState) (ProcedureResult, error) {
	preparation, err := readStructuralPreparation(ctx, lane, drive, deciding, true)
	if err != nil {
		return ProcedureResult{}, err
	}
	if preparation.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	scope := agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}
	if deciding.Task.Boundary.Kind == session.BoundaryCommitNavigation {
		hook, err := lane.Hooks.RunBeforeNavigation(ctx, drive.Gate, agentharness.BeforeNavigationEvent{HookScope: scope, TargetID: deciding.Task.Boundary.TargetID, Preparation: branchPreparation(preparation.Value), CustomInstructions: deciding.Task.CustomInstructions})
		if err != nil {
			return ProcedureResult{}, err
		}
		if hook != nil && hook.Decline {
			return publishStructuralOutcome(ctx, lane, drive, deciding, structuralOutcome{Kind: "declined"})
		}
		if hook != nil && hook.Summary != nil {
			return publishStructuralOutcome(ctx, lane, drive, deciding, structuralOutcome{Kind: session.PreparationBranchSummary, ResultEntryID: lane.Session.IdGenerator().Next(nil), Branch: *hook.Summary, FromHook: true})
		}
		return publishStructuralReady(ctx, lane, drive, deciding)
	}
	reason, err := compactionReason(deciding.Task)
	if err != nil {
		return ProcedureResult{}, err
	}
	hook, err := lane.Hooks.RunBeforeCompaction(ctx, drive.Gate, agentharness.BeforeCompactionEvent{HookScope: scope, Reason: reason, Preparation: compactionPreparation(preparation.Value), CustomInstructions: deciding.Task.CustomInstructions})
	if err != nil {
		return ProcedureResult{}, err
	}
	if hook != nil && hook.Decline {
		return publishStructuralOutcome(ctx, lane, drive, deciding, structuralOutcome{Kind: "declined"})
	}
	if hook != nil && hook.Compaction != nil {
		return publishStructuralOutcome(ctx, lane, drive, deciding, structuralOutcome{Kind: session.PreparationCompaction, ResultEntryID: lane.Session.IdGenerator().Next(nil), Compaction: *hook.Compaction, FromHook: true})
	}
	return publishStructuralReady(ctx, lane, drive, deciding)
}

func effectPendingFromReady(ready session.OperationState) session.OperationState {
	return session.OperationState{OperationScope: ready.OperationScope, At: session.AtSummaryEffectPending, Task: ready.Task, SummaryContext: ready.SummaryContext, Attempt: ready.NextAttempt, UsageIDs: []string{}}
}
func retryWaitFromEffect(effect session.OperationState, message string) session.OperationState {
	return session.OperationState{OperationScope: effect.OperationScope, At: session.AtSummaryRetryWait, Task: effect.Task, SummaryContext: effect.SummaryContext, NextAttempt: effect.Attempt + 1, NotBefore: RetryNotBefore(effect.SummaryContext.RetryPolicy, effect.Attempt, runtimeNow()), ErrorMessage: message}
}
func readyFromRetryWait(retry session.OperationState) session.OperationState {
	return session.OperationState{OperationScope: retry.OperationScope, At: session.AtSummaryReady, Task: retry.Task, SummaryContext: retry.SummaryContext, NextAttempt: retry.NextAttempt}
}

func publishAttemptIntent(ctx harness.Context, lane *Lane, ready session.OperationState) (ContinueOperationResult[session.OperationState], error) {
	return ContinueOperation(ctx, lane, ready, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[session.OperationState], error) {
		next := effectPendingFromReady(current)
		return OperationCommand[session.OperationState]{Kind: CommandCommit, Writes: []session.Write{}, OperationState: next, Materialize: func(session.CommitResult) session.OperationState { return next }}, nil
	})
}
func publishNestedRequestIntent(ctx harness.Context, lane *Lane, effect session.OperationState, index int, usageID string) (ContinueOperationResult[session.OperationState], error) {
	return ContinueOperation(ctx, lane, effect, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[session.OperationState], error) {
		next := current
		next.Request = &session.SummaryRequest{Index: index, UsageID: usageID}
		return OperationCommand[session.OperationState]{Kind: CommandCommit, Writes: []session.Write{}, OperationState: next, Materialize: func(session.CommitResult) session.OperationState { return next }}, nil
	})
}
func publishNestedRequestOutcome(ctx harness.Context, lane *Lane, effect session.OperationState, usageID string, response *ai.AssistantMessage) error {
	_, err := SettleOperation(ctx, lane, effect, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[struct{}], error) {
		next := current
		next.UsageIDs = append(slices.Clone(current.UsageIDs), usageID)
		next.Request = nil
		row := session.UsageRow{ID: usageID, Usage: response.Usage, Adjustment: false}
		return OperationCommand[struct{}]{Kind: CommandCommit, Writes: []session.Write{session.InsertUsage(row)}, OperationState: next, Materialize: func(session.CommitResult) struct{} { return struct{}{} }, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{usageEvent(row, 0, commit, lane.Name())}
		}}, nil
	})
	return err
}

func requestStreamOptions(options ai.StreamOptions, stream harness.AgentHarnessStreamOptions, ctx harness.Context, onPayload func(any, *ai.Model) (any, error)) ai.StreamOptions {
	options.Transport = stream.Transport
	options.TimeoutMs = stream.TimeoutMs
	options.MaxRetries = stream.MaxRetries
	options.MaxRetryDelayMs = stream.MaxRetryDelayMs
	options.Headers = nil
	if stream.Headers != nil {
		options.Headers = make(ai.ProviderHeaders, len(stream.Headers))
		for key, value := range stream.Headers {
			options.Headers[key] = new(value)
		}
	}
	options.Metadata = stream.Metadata
	options.CacheRetention = ai.CacheRetentionNone
	options.Deferred = &ai.DeferredOption{Enabled: false}
	options.Signal = ctx
	options.TelemetryContext = harness.GetTelemetryContext(ctx)
	options.OnPayload = onPayload
	return options
}

type structuralAttemptResult struct {
	Outcome         structuralOutcome
	Retryable       bool
	CancelRequested bool
}

func structuralAdmissionError(err error) error {
	// Upstream catches only the outer AbortRequested; a wrapped application error must still reject the operation.
	if abort, ok := err.(*AbortRequested); ok { //nolint:errorlint // Pi instanceof does not unwrap application errors.
		if err := abort.Cancellation(); err != nil {
			return err
		}
		return &structuralCancelled{}
	}
	return err
}

type structuralRequestFailure struct{ cause error }

func (failure *structuralRequestFailure) Error() string { return failure.cause.Error() }

func performStructuralAttempt(ctx harness.Context, lane *Lane, drive *Drive, effect session.OperationState, model *ai.Model, preparation session.DurableStructuralPreparation) (structuralAttemptResult, error) {
	requestIndex := 0
	var lastResponse *ai.AssistantMessage
	request := func(requestContext harness.Context, aiContext ai.Context, options ai.StreamOptions) (_ *ai.AssistantMessage, requestErr error) {
		defer func() {
			if requestErr != nil {
				requestErr = &structuralRequestFailure{cause: requestErr}
			}
		}()
		base := effect.SummaryContext.StreamOptions
		base.Deferred = &harness.AgentHarnessDeferredOption{Enabled: false}
		hook, err := lane.Hooks.RunBeforeRequest(requestContext, drive.Gate, agentharness.BeforeRequestEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Model: model, Step: summaryKind(effect.Task), Attempt: effect.Attempt, StreamOptions: base})
		if err != nil {
			return nil, structuralAdmissionError(err)
		}
		stream := base
		if hook != nil && hook.StreamOptions != nil {
			stream = agentharness.ApplyStreamOptionsPatch(base, *hook.StreamOptions)
		}
		stream.Deferred = &harness.AgentHarnessDeferredOption{Enabled: false}
		usageID := lane.Session.IdGenerator().Next(nil)
		intent, err := publishNestedRequestIntent(ctx, lane, effect, requestIndex, usageID)
		requestIndex++
		if err != nil {
			return nil, err
		}
		if intent.CancelRequested {
			return nil, &structuralCancelled{}
		}
		admitted := harness.WithAbortSignal(requestContext, drive.Gate.Signal())
		var response *ai.AssistantMessage
		err = drive.Gate.Admit(func() error {
			response = lane.Models.CompleteSimple(admitted, model, aiContext, requestStreamOptions(options, stream, admitted, func(payload any, requestModel *ai.Model) (any, error) {
				hook, err := lane.Hooks.RunBeforePayload(admitted, drive.Gate, agentharness.BeforePayloadEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Model: requestModel, Payload: payload})
				if err != nil {
					return nil, err
				}
				if hook == nil {
					return nil, nil
				}
				return hook.Payload, nil
			}))
			return nil
		})
		if err != nil {
			return nil, structuralAdmissionError(err)
		}
		if response == nil {
			return nil, &session.SessionInvariantError{Message: "Structural provider returned no response"}
		}
		lastResponse = response
		if err := publishNestedRequestOutcome(ctx, lane, intent.Value, usageID, response); err != nil {
			return nil, err
		}
		return response, nil
	}
	custom := ""
	if effect.Task.CustomInstructions != nil {
		custom = *effect.Task.CustomInstructions
	}
	result := structuralAttemptResult{}
	var err error
	if summaryKind(effect.Task) == session.PreparationCompaction {
		var summary compaction.CompactResult
		summary, err = compaction.CompactWithRequest(ctx, compactionPreparation(preparation), compaction.CompactGenerationOptions{Model: model, CustomInstructions: custom, ThinkingLevel: effect.SummaryContext.Configuration.ThinkingLevel}, request)
		result.Outcome = structuralOutcome{Kind: session.PreparationCompaction, Compaction: summary}
	} else {
		var summary compaction.BranchSummaryResult
		summary, err = compaction.GenerateBranchSummaryWithRequest(ctx, branchPreparation(preparation), compaction.PreparedBranchSummaryOptions{CustomInstructions: custom}, request)
		result.Outcome = structuralOutcome{Kind: session.PreparationBranchSummary, Branch: summary}
	}
	result.Retryable = lastResponse != nil && ai.IsRetryableAssistantError(*lastResponse)
	if err == nil {
		return result, nil
	}
	// The generators return their coded response errors directly; request rejections retain their identity.
	switch failure := err.(type) { //nolint:errorlint // Pi distinguishes returned Result errors from thrown request errors, not wrapped causes.
	case *structuralRequestFailure:
		if _, cancelled := failure.cause.(*structuralCancelled); cancelled { //nolint:errorlint // Only the request's own outer cancellation marker is consumed.
			return structuralAttemptResult{CancelRequested: true}, nil
		}
		return structuralAttemptResult{}, failure.cause
	case *harness.CompactionError:
		result.Outcome = structuralOutcome{Kind: "error", Error: &session.OperationError{Code: string(failure.Code), Message: failure.Message}}
	case *harness.BranchSummaryError:
		result.Outcome = structuralOutcome{Kind: "error", Error: &session.OperationError{Code: string(failure.Code), Message: failure.Message}}
	default:
		return structuralAttemptResult{}, err
	}
	return result, nil
}

func publishAttemptResult(ctx harness.Context, lane *Lane, drive *Drive, effect session.OperationState, result structuralAttemptResult) (ProcedureResult, error) {
	if result.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	if result.Outcome.Kind == session.PreparationCompaction || result.Outcome.Kind == session.PreparationBranchSummary {
		result.Outcome.ResultEntryID = effect.SummaryContext.ResultEntryID
		return publishStructuralOutcome(ctx, lane, drive, effect, result.Outcome)
	}
	operation := lane.SnapshotState().Operation
	if result.Outcome.Error.Code == "aborted" && operation != nil && operation.State.Control.Status == session.ControlRunning {
		return ProcedureResult{}, &session.SessionInvariantError{Message: "Structural provider response is aborted while durable control is running"}
	}
	if result.Retryable && effect.Attempt < effect.SummaryContext.RetryPolicy.MaxAttempts {
		return publishStructuralRetry(ctx, lane, drive, effect, result.Outcome.Error.Message, false)
	}
	return publishStructuralOutcome(ctx, lane, drive, effect, structuralOutcome{Kind: "failed", Error: result.Outcome.Error})
}

func publishStructuralRetry(ctx harness.Context, lane *Lane, drive *Drive, effect session.OperationState, message string, recovery bool) (ProcedureResult, error) {
	retry := retryWaitFromEffect(effect, message)
	return continuedProcedure(ContinueOperation(ctx, lane, effect, func(LaneState, session.OperationState, session.OperationMeta, session.SessionReader) (OperationCommand[ProcedureResult], error) {
		return OperationCommand[ProcedureResult]{Kind: CommandCommit, Writes: []session.Write{}, OperationState: retry, Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.RetryScheduledPayload{RunID: drive.OperationID, Step: effect.Task.TaskID, Attempt: retry.NextAttempt, MaxAttempts: effect.SummaryContext.RetryPolicy.MaxAttempts, DelayMs: int64(ai.RetryDelayMs(effect.SummaryContext.RetryPolicy.BaseDelayMs, &effect.SummaryContext.RetryPolicy.MaxAgentDelayMs, effect.Attempt)), NotBefore: retry.NotBefore, ErrorMessage: message}}}
		}}, nil
	}))
}

// RunStructuralGeneration performs one durable structural attempt, accounting for each nested provider request separately.
func RunStructuralGeneration(ctx harness.Context, lane *Lane, drive *Drive, ready session.OperationState) (ProcedureResult, error) {
	preparation, err := readStructuralPreparation(ctx, lane, drive, ready, false)
	if err != nil {
		return ProcedureResult{}, err
	}
	if preparation.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	identity := ready.SummaryContext.Configuration.Model
	model := lane.Models.GetModel(identity.Provider, identity.ModelID)
	if model == nil {
		details := session.JsonValue(map[string]any{"provider": identity.Provider, "modelId": identity.ModelID})
		return publishStructuralOutcome(ctx, lane, drive, ready, structuralOutcome{Kind: "failed", Error: &session.OperationError{Code: "model_unavailable", Message: "The configured model is unavailable in this process", Details: &details}})
	}
	intent, err := publishAttemptIntent(ctx, lane, ready)
	if err != nil {
		return ProcedureResult{}, err
	}
	if intent.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	result, err := performStructuralAttempt(ctx, lane, drive, intent.Value, model, preparation.Value)
	if err != nil {
		return ProcedureResult{}, err
	}
	return publishAttemptResult(ctx, lane, drive, intent.Value, result)
}

// RunStructuralRetryWait consumes a retry wait without starting a provider request.
func RunStructuralRetryWait(ctx harness.Context, lane *Lane, drive *Drive, retry session.OperationState) (ProcedureResult, error) {
	if runtimeNow() < retry.NotBefore {
		if !drive.WaitForRetry {
			return ProcedureResult{Kind: ProcedureWaiting, Outcome: agentharness.DriveOutcome{Kind: agentharness.DriveWaiting, OperationID: drive.OperationID, Reason: agentharness.DriveWaitRetry, NotBefore: retry.NotBefore}}, nil
		}
		if err := drive.Gate.Admit(func() error { return WaitUntil(drive.Gate.Signal(), retry.NotBefore) }); err != nil {
			return ProcedureResult{}, err
		}
	}
	return continuedProcedure(ContinueOperation(ctx, lane, retry, func(_ LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[ProcedureResult], error) {
		return OperationCommand[ProcedureResult]{Kind: CommandCommit, Writes: []session.Write{}, OperationState: readyFromRetryWait(current), Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.RetryStartPayload{RunID: drive.OperationID, Step: current.Task.TaskID, Attempt: current.NextAttempt}}}
		}}, nil
	}))
}

// RecoverStructuralGeneration consumes an orphaned attempt without resuming its abandoned nested request.
func RecoverStructuralGeneration(ctx harness.Context, lane *Lane, drive *Drive, effect session.OperationState) (ProcedureResult, error) {
	failure := &session.OperationError{Code: "structural_interrupted", Message: "Structural summary attempt was interrupted and its external outcome is unknown"}
	if effect.Attempt >= effect.SummaryContext.RetryPolicy.MaxAttempts {
		return publishStructuralOutcome(ctx, lane, drive, effect, structuralOutcome{Kind: "failed", Error: failure})
	}
	return publishStructuralRetry(ctx, lane, drive, effect, failure.Message, true)
}

// PrepareCompactionThreshold prepares threshold compaction unless a newer compaction guards the triggering entry.
func PrepareCompactionThreshold(ctx harness.Context, lane *Lane, drive *Drive, checkpoint session.OperationState) (ContinueOperationResult[*StructuralPreparation], error) {
	settings := checkpoint.Settings.Compaction
	identity := lane.SnapshotState().Configuration.Model
	model := lane.Models.GetModel(identity.Provider, identity.ModelID)
	if !settings.Enabled || model == nil {
		return ContinueOperationResult[*StructuralPreparation]{}, nil
	}
	path, err := ReadBoundedEntries(ctx, lane, drive, checkpoint)
	if err != nil {
		return ContinueOperationResult[*StructuralPreparation]{}, err
	}
	if path.CancelRequested {
		return ContinueOperationResult[*StructuralPreparation]{CancelRequested: true}, nil
	}
	triggerIndex := slices.IndexFunc(path.Value, func(entry session.Entry) bool { return entry.ID == checkpoint.TriggerEntryID })
	newest := -1
	for i, entry := range slices.Backward(path.Value) {
		if entry.Type == session.EntryTypeCompaction {
			newest = i
			break
		}
	}
	if newest >= triggerIndex && newest != -1 {
		return ContinueOperationResult[*StructuralPreparation]{}, nil
	}
	if triggerIndex == -1 {
		return ContinueOperationResult[*StructuralPreparation]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Checkpoint trigger %s is missing from its Branch", checkpoint.TriggerEntryID)}
	}
	prepared, err := compaction.PrepareCompaction(path.Value, settings)
	if err != nil {
		return ContinueOperationResult[*StructuralPreparation]{}, err
	}
	if prepared == nil || !compaction.ShouldCompact(prepared.TokensBefore, model.Capabilities.ContextWindow, settings) {
		return ContinueOperationResult[*StructuralPreparation]{}, nil
	}
	return ContinueOperationResult[*StructuralPreparation]{Value: &StructuralPreparation{TaskID: lane.Session.IdGenerator().Next(nil), Preparation: DurableCompactionPreparation(*prepared)}}, nil
}

// PrepareOverflowCompaction prepares one overflow recovery before settling the assistant response.
func PrepareOverflowCompaction(ctx harness.Context, lane *Lane, drive *Drive, generation session.OperationState) (*StructuralPreparation, error) {
	if generation.GenerationContext.OverflowRecoveryUsed {
		return nil, nil
	}
	path, err := ReadBoundedEntries(ctx, lane, drive, generation)
	if err != nil {
		return nil, err
	}
	if path.CancelRequested {
		return nil, nil
	}
	prepared, err := compaction.PrepareCompaction(path.Value, generation.Settings.Compaction)
	if err != nil || prepared == nil {
		return nil, err
	}
	return &StructuralPreparation{TaskID: lane.Session.IdGenerator().Next(nil), Preparation: DurableCompactionPreparation(*prepared)}, nil
}

// CommitNavigation validates and atomically publishes unsummarized navigation and its terminal observation.
func CommitNavigation(ctx harness.Context, lane *Lane, drive *Drive, navigation session.OperationState) (ProcedureResult, error) {
	return continuedProcedure(ContinueOperation(ctx, lane, navigation, func(_ LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[ProcedureResult], error) {
		if current.TargetID != nil {
			entries, err := reader.GetEntries(ctx, []string{*current.TargetID})
			if err != nil {
				return OperationCommand[ProcedureResult]{}, err
			}
			if _, ok := entries[*current.TargetID]; !ok {
				return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Navigation target %s is missing", *current.TargetID)}
			}
		}
		if (current.TargetID == nil && meta.SourceTipID == nil) || (current.TargetID != nil && meta.SourceTipID != nil && *current.TargetID == *meta.SourceTipID) {
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Navigation target must differ from its source tip"}
		}
		if current.TargetID == nil && current.Label != nil {
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Root navigation cannot set a label"}
		}
		writes := []session.Write{session.SetValue(session.BranchTip(lane.Name()), current.TargetID)}
		if current.Label != nil && current.TargetID != nil {
			writes = append(writes, session.SetValue(session.EntryLabel(*current.TargetID), *current.Label))
		}
		cleanup, err := OperationCleanupWrites(ctx, reader, drive.OperationID, current)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		record, err := OperationResultRecord(meta, session.TerminalCompleted, current.TargetID, nil)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		return OperationCommand[ProcedureResult]{Kind: CommandFinish, Writes: append(writes, cleanup...), Record: record, Lane: &LanePatch{SetTipID: true, TipID: current.TargetID}, Materialize: func(session.CommitResult) ProcedureResult {
			return ProcedureResult{Kind: ProcedureSettled, Record: record}
		}, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.Name(), Payload: agentharness.NavigationEndPayload{RunID: drive.OperationID, Status: session.TerminalCompleted, FromTipID: meta.SourceTipID, TipID: current.TargetID, EndedAt: record.EndedAt}}}
		}}, nil
	}))
}
