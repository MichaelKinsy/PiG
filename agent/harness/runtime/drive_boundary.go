package runtime

// Ports packages/agent/src/harness/runtime/drive/boundary.ts.

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// BoundaryFinishPending asks the finish hook to mediate a planned boundary.
type BoundaryFinishPending struct{ EntryIDs []string }

// BoundaryPlacement contains ordered input placement without committing it. Nil TriggerEntryID and Queues mean absent.
type BoundaryPlacement struct {
	Entries        []session.Entry
	Writes         []session.Write
	TipID          *string
	Inbox          []session.InboxItem
	TriggerEntryID *string
	Queues         []agentharness.LaneQueuedItem
}

// NormalizedRetryPolicy captures the current retry budget for one operation step.
func NormalizedRetryPolicy(lane *Lane) session.NormalizedRetryPolicy {
	retry := lane.ReadConfig().RetryPolicy
	attempts := 1
	if retry.Enabled {
		attempts = retry.MaxRetries + 1
	}
	limit := ai.DefaultMaxAgentRetryDelayMs
	if retry.MaxAgentDelayMs != nil {
		limit = *retry.MaxAgentDelayMs
	}
	return session.NormalizedRetryPolicy{MaxAttempts: attempts, BaseDelayMs: retry.BaseDelayMs, MaxAgentDelayMs: limit}
}

// AssistantReadyAtBoundary captures a fresh assistant step after boundary input placement.
func AssistantReadyAtBoundary(lane *Lane, state LaneState, scope session.OperationScope, triggerEntryID string, overflowRecoveryUsed bool) session.OperationState {
	return session.OperationState{OperationScope: scope, At: session.AtAssistantReady, NextAttempt: 1,
		GenerationContext: session.GenerationContext{StepID: lane.Session.IdGenerator().Next(nil), TriggerEntryID: triggerEntryID, Configuration: state.Configuration, StreamOptions: lane.ReadConfig().StreamOptions, RetryPolicy: NormalizedRetryPolicy(lane), OverflowRecoveryUsed: overflowRecoveryUsed},
	}
}

// PlanBoundaryInbox materializes one boundary's lane-owned input in admission order without committing it.
func PlanBoundaryInbox(ctx harness.Context, lane *Lane, drive *Drive, state LaneState, scope session.OperationScope, reader session.SessionReader, tipID *string, followUpWhenNoTrigger bool) (BoundaryPlacement, error) {
	selectedIDs := make(map[string]bool)
	steerSelected := false
	for _, item := range state.Inbox {
		if item.Kind == session.InboxWrite {
			selectedIDs[item.EntryID] = true
		}
		if item.Kind == session.InboxSteer && (scope.Settings.SteeringMode == agent.QueueModeAll || !steerSelected) {
			selectedIDs[item.EntryID] = true
			steerSelected = true
		}
	}
	type pendingItem struct {
		item    session.InboxItem
		pending session.PendingEntry
	}
	load := func() ([]pendingItem, error) {
		pending := make([]pendingItem, 0, len(selectedIDs))
		for _, item := range state.Inbox {
			if selectedIDs[item.EntryID] {
				pending = append(pending, pendingItem{item: item})
			}
		}
		err := parallelReads(len(pending), func(index int) error {
			stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(pending[index].item.EntryID))
			if err != nil {
				return err
			}
			item := pending[index].item
			if stored == nil {
				return &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its payload", item.Kind, item.EntryID)}
			}
			if item.Kind != session.InboxWrite && stored.Value.Type != session.PendingEntryMessage {
				return &session.SessionInvariantError{Message: fmt.Sprintf("Queued %s entry %s is not a message", item.Kind, item.EntryID)}
			}
			pending[index].pending = stored.Value
			return nil
		})
		return pending, err
	}
	projects := func(value session.PendingEntry) bool {
		return value.Type == session.PendingEntryMessage || lane.ReadConfig().EntryProjectors[value.CustomType] != nil
	}
	pending, err := load()
	if err != nil {
		return BoundaryPlacement{}, err
	}
	if followUpWhenNoTrigger && !slices.ContainsFunc(pending, func(item pendingItem) bool { return projects(item.pending) }) {
		followSelected := false
		for _, item := range state.Inbox {
			if item.Kind == session.InboxFollowUp && (scope.Settings.FollowUpMode == agent.QueueModeAll || !followSelected) {
				selectedIDs[item.EntryID] = true
				followSelected = true
			}
		}
		pending, err = load()
		if err != nil {
			return BoundaryPlacement{}, err
		}
	}
	placement := BoundaryPlacement{Entries: []session.Entry{}, Writes: []session.Write{}, TipID: tipID, Inbox: []session.InboxItem{}}
	for _, selected := range pending {
		entry := session.Entry{ID: selected.item.EntryID, ParentID: placement.TipID}
		if selected.pending.Type == session.PendingEntryMessage {
			entry.Type = session.EntryTypeMessage
			entry.Message = selected.pending.Message
		} else {
			entry.Type = session.EntryTypeCustom
			entry.CustomType = selected.pending.CustomType
			entry.Data = selected.pending.CustomPayload
		}
		placement.TipID = new(entry.ID)
		if projects(selected.pending) {
			placement.TriggerEntryID = new(entry.ID)
		}
		placement.Entries = append(placement.Entries, entry)
		placement.Writes = append(placement.Writes, session.InsertEntry(entry))
	}
	for _, item := range pending {
		placement.Writes = append(placement.Writes, session.DeleteValue(session.PendingEntryValue(item.item.EntryID)))
	}
	for _, item := range state.Inbox {
		if !selectedIDs[item.EntryID] {
			placement.Inbox = append(placement.Inbox, item)
		}
	}
	if len(pending) > 0 {
		placement.Queues, err = ReadLaneQueues(ctx, reader, placement.Inbox)
		if err != nil {
			return BoundaryPlacement{}, err
		}
		if placement.Queues == nil {
			placement.Queues = []agentharness.LaneQueuedItem{}
		}
		placement.Writes = append(placement.Writes, session.SetValue(session.BranchTip(lane.Name()), placement.TipID))
	}
	return placement, nil
}

// BoundaryPlacementEvents materializes committed input and the optional queue update in order.
func BoundaryPlacementEvents(placement BoundaryPlacement, commit session.CommitResult, firstWriteIndex int, lane, runID string) []agentharness.HarnessEvent {
	events := CommittedEntryEvents(placement.Entries, commit, lane, runID, firstWriteIndex)
	if placement.Queues != nil {
		events = append(events, agentharness.HarnessEvent{Lane: lane, Payload: agentharness.QueueUpdatePayload{Queues: placement.Queues}})
	}
	return events
}

func continuedProcedure(result ContinueOperationResult[ProcedureResult], err error) (ProcedureResult, error) {
	if err != nil {
		return ProcedureResult{}, err
	}
	if result.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	return result.Value, nil
}

// FinishRunBoundary replans after before_run_end, publishing renewed work or the terminal result on the same mutation line.
func FinishRunBoundary(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState, continuation session.Continuation, plannedEntryIDs []string, pendingEvents []agentharness.HarnessEvent) (ProcedureResult, error) {
	bounded, err := ReadBoundedContext(ctx, lane, drive, capability)
	if err != nil {
		return ProcedureResult{}, err
	}
	if bounded.CancelRequested {
		return ProcedureResult{Kind: ProcedureContinue}, nil
	}
	hook, err := lane.Hooks.RunBeforeRunEnd(ctx, drive.Gate, agentharness.BeforeRunEndEvent{HookScope: agentharness.HookScope{Lane: lane.Name(), RunID: drive.OperationID}, Messages: bounded.Value})
	if err != nil {
		return ProcedureResult{}, err
	}
	var followUp *session.Entry
	if hook != nil && hook.FollowUp != nil {
		followUp = &session.Entry{ID: lane.Session.IdGenerator().Next(nil), Type: session.EntryTypeMessage, Message: agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: *hook.FollowUp}}, Timestamp: runtimeNow()}}}
	}
	return continuedProcedure(ContinueOperation(ctx, lane, capability, func(state LaneState, current session.OperationState, meta session.OperationMeta, reader session.SessionReader) (OperationCommand[ProcedureResult], error) {
		placement, err := PlanBoundaryInbox(ctx, lane, drive, state, current.OperationScope, reader, state.TipID, true)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		patch := &LanePatch{SetTipID: true, TipID: placement.TipID, SetInbox: true, Inbox: placement.Inbox}
		baseEvents := func(commit session.CommitResult) []agentharness.HarnessEvent {
			return append(slices.Clone(pendingEvents), BoundaryPlacementEvents(placement, commit, 0, lane.Name(), drive.OperationID)...)
		}
		if placement.TriggerEntryID != nil {
			return OperationCommand[ProcedureResult]{Kind: CommandCommit, Writes: placement.Writes, OperationState: AssistantReadyAtBoundary(lane, state, current.OperationScope, *placement.TriggerEntryID, false), Lane: patch, Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }, Events: baseEvents}, nil
		}
		ids := make([]string, len(placement.Entries))
		for i, entry := range placement.Entries {
			ids[i] = entry.ID
		}
		if slices.Equal(ids, plannedEntryIDs) && followUp != nil {
			entry := *followUp
			entry.ParentID = placement.TipID
			writeIndex := len(placement.Writes)
			writes := append(slices.Clone(placement.Writes), session.InsertEntry(entry), session.SetValue(session.BranchTip(lane.Name()), new(entry.ID)))
			patch.TipID = new(entry.ID)
			return OperationCommand[ProcedureResult]{Kind: CommandCommit, Writes: writes, OperationState: AssistantReadyAtBoundary(lane, state, current.OperationScope, entry.ID, false), Lane: patch, Materialize: func(session.CommitResult) ProcedureResult { return ProcedureResult{Kind: ProcedureContinue} }, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
				entry.Seq = commit.Seqs[writeIndex]
				entry.Timestamp = commit.Timestamp
				return append(baseEvents(commit), EntryLifecycleEvents(entry, lane.Name(), drive.OperationID)...)
			}}, nil
		}
		if placement.TipID == nil {
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Completed run has no tip"}
		}
		if continuation.IncludeFinalAssistant && current.LatestAssistantEntryID == nil {
			return OperationCommand[ProcedureResult]{}, &session.SessionInvariantError{Message: "Completed run is missing its final assistant"}
		}
		record, err := OperationResultRecord(meta, session.TerminalCompleted, placement.TipID, nil)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		cleanup, err := OperationCleanupWrites(ctx, reader, drive.OperationID, current)
		if err != nil {
			return OperationCommand[ProcedureResult]{}, err
		}
		return OperationCommand[ProcedureResult]{Kind: CommandFinish, Writes: append(placement.Writes, cleanup...), Record: record, Lane: patch, Materialize: func(session.CommitResult) ProcedureResult {
			return ProcedureResult{Kind: ProcedureSettled, Record: record}
		}, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
			return append(baseEvents(commit), agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.RunEndPayload{RunID: drive.OperationID, Status: session.TerminalCompleted, FromTipID: meta.SourceTipID, TipID: placement.TipID, EndedAt: record.EndedAt}})
		}}, nil
	}))
}
