package runtime

// Ports packages/agent/src/harness/runtime/drive/checkpoint.ts

import (
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// StartRun consumes before_run and commits its injected messages with the initial checkpoint.
func StartRun(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState) (ProcedureResult, error) {
	prompt, err := ContinueOperation(ctx, lane, run, func(_ LaneState, _ session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[[]agent.AgentMessage], error) {
		if meta.Intent.Kind != session.OperationKindRun {
			return OperationCommand[[]agent.AgentMessage]{}, &session.SessionInvariantError{Message: "Run operation has non-run intent"}
		}
		entries, err := reader.GetEntries(ctx, meta.Intent.PromptEntryIDs)
		if err != nil {
			return OperationCommand[[]agent.AgentMessage]{}, err
		}
		messages := make([]agent.AgentMessage, 0, len(meta.Intent.PromptEntryIDs))
		for _, id := range meta.Intent.PromptEntryIDs {
			entry, ok := entries[id]
			if !ok || entry.Type != session.EntryTypeMessage {
				return OperationCommand[[]agent.AgentMessage]{}, &session.SessionInvariantError{Message: "Run prompt entry " + id + " is missing its message"}
			}
			messages = append(messages, entry.Message)
		}
		return OperationCommand[[]agent.AgentMessage]{Kind: CommandReturn, Result: messages}, nil
	})
	if err != nil {
		return ProcedureResult{}, err
	}
	if prompt.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	hook, err := lane.Hooks.RunBeforeRun(ctx, drive.Gate, agentharness.BeforeRunEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Prompt: prompt.Value, Resources: lane.ReadConfig().Resources})
	if err != nil {
		return ProcedureResult{}, err
	}
	reserved := []session.Entry{}
	if hook != nil {
		if slices.ContainsFunc(hook.Messages, session.IsPendingAssistant) {
			return ProcedureResult{}, &session.SessionInvariantError{Message: "before_run returned a pending assistant message"}
		}
		for _, message := range hook.Messages {
			id := lane.Session.IdGenerator().Next(nil)
			reserved = append(reserved, session.Entry{ID: id, Type: session.EntryTypeMessage, Message: message})
		}
	}
	result, err := ContinueOperation(ctx, lane, run, func(state LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[ProcedureResult], error) {
		entries := ChainEntries(state.TipID, reserved)
		trigger := state.TipID
		if len(entries) != 0 {
			trigger = &entries[len(entries)-1].ID
		}
		if trigger == nil {
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Run start has no trigger entry"}
		}
		next := session.OperationState{OperationScope: current.OperationScope, At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: *trigger}
		writes := make([]session.Write, 0, len(entries)+1)
		for _, entry := range entries {
			writes = append(writes, session.InsertEntry(entry))
		}
		if len(entries) != 0 {
			writes = append(writes, session.SetValue(session.BranchTip(lane.Name()), trigger))
		}
		return OperationCommand[ProcedureResult]{Kind: CommandCommit, Writes: writes, OperationState: next, Lane: &LanePatch{SetTipID: true, TipID: trigger}, Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
			return CommittedEntryEvents(entries, commit, lane.Name(), drive.OperationID, 0)
		}}, nil
	})
	if result.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, err
	}
	return result.Value, err
}

type checkpointPlan struct {
	result   ProcedureResult
	finish   bool
	entryIDs []string
}

// RunCheckpoint advances a durable run boundary with at most one commit.
func RunCheckpoint(ctx harness.Context, lane *Lane, drive *Drive, run session.OperationState) (ProcedureResult, error) {
	threshold, err := PrepareCompactionThreshold(ctx, lane, drive, run)
	if err != nil {
		return ProcedureResult{}, err
	}
	if threshold.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	planned, err := ContinueOperation(ctx, lane, run, func(state LaneState, current session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[checkpointPlan], error) {
		placement, err := PlanBoundaryInbox(ctx, lane, drive, state, current.OperationScope, reader, state.TipID, threshold.Value == nil && current.Continuation.Kind == session.ContinuationMayFinish)
		if err != nil {
			return OperationCommand[checkpointPlan]{}, err
		}
		command := OperationCommand[checkpointPlan]{Kind: CommandCommit, Writes: placement.Writes, Lane: &LanePatch{SetTipID: true, TipID: placement.TipID, SetInbox: true, Inbox: placement.Inbox}, Materialize: func(session.CommitResult) checkpointPlan {
			return checkpointPlan{result: ProcedureResult{Kind: ProcedureContinue}}
		}, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
			return BoundaryPlacementEvents(placement, commit, 0, lane.Name(), drive.OperationID)
		}}
		switch {
		case placement.TriggerEntryID != nil:
			command.OperationState = AssistantReadyAtBoundary(lane, state, current.OperationScope, *placement.TriggerEntryID, false)
		case threshold.Value != nil:
			command.OperationState = session.OperationState{OperationScope: current.OperationScope, At: session.AtSummaryDeciding, Task: session.SummaryTask{TaskID: threshold.Value.TaskID, Reason: "threshold", Boundary: session.ResultBoundary{Kind: session.BoundaryResumeCheckpoint, ResumeAfter: &session.CheckpointData{Continuation: current.Continuation, TriggerEntryID: current.TriggerEntryID}}}}
			command.Writes = append(command.Writes, session.SetValue(session.OperationPreparation(drive.OperationID, threshold.Value.TaskID), threshold.Value.Preparation))
			command.Events = func(commit session.CommitResult) []agentharness.HarnessEvent {
				return append(BoundaryPlacementEvents(placement, commit, 0, lane.Name(), drive.OperationID), agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.CompactionStartPayload{RunID: drive.OperationID, Reason: "threshold", StartedAt: commit.Timestamp}})
			}
		case current.Continuation.Kind == session.ContinuationNeedAssistant:
			command.OperationState = AssistantReadyAtBoundary(lane, state, current.OperationScope, current.TriggerEntryID, current.Continuation.OverflowRecoveryUsed)
		default:
			ids := make([]string, len(placement.Entries))
			for i, entry := range placement.Entries {
				ids[i] = entry.ID
			}
			return OperationCommand[checkpointPlan]{Kind: CommandReturn, Result: checkpointPlan{finish: true, entryIDs: ids}}, nil
		}
		return command, nil
	})
	if err != nil {
		return ProcedureResult{}, err
	}
	if planned.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	if !planned.Value.finish {
		return planned.Value.result, nil
	}
	if run.Continuation.Kind != session.ContinuationMayFinish {
		return ProcedureResult{}, &session.SessionInvariantError{Message: "Checkpoint finish mediation requires a finish continuation"}
	}
	return FinishRunBoundary(ctx, lane, drive, run, run.Continuation, planned.Value.entryIDs, nil)
}
