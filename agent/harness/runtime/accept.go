package runtime

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/runtime/lane.ts (accept, acceptRun, acceptCompaction, acceptNavigation).

func selectAcceptedInbox(inbox []session.InboxItem, steering, followUp agent.QueueMode) (selected, remainder []session.InboxItem) {
	selected, remainder = []session.InboxItem{}, []session.InboxItem{}
	steerTaken, followUpTaken := false, false
	for _, item := range inbox {
		eligible := item.Kind == session.InboxWrite || item.Kind == session.InboxNextRun ||
			(item.Kind == session.InboxSteer && (steering == "all" || !steerTaken)) ||
			(item.Kind == session.InboxFollowUp && (followUp == "all" || !followUpTaken))
		if !eligible {
			remainder = append(remainder, item)
			continue
		}
		selected = append(selected, item)
		if item.Kind == session.InboxSteer {
			steerTaken = true
		}
		if item.Kind == session.InboxFollowUp {
			followUpTaken = true
		}
	}
	return selected, remainder
}

func capturedSettings(config Config) session.RunSettings {
	return session.RunSettings{Compaction: config.Compaction, SteeringMode: config.SteeringMode, FollowUpMode: config.FollowUpMode, ToolExecution: string(config.ToolExecution)}
}

// Accept atomically appends input and durable operation preparation, publishes control state and awaits direct listeners without driving effects.
func (lane *Lane) Accept(ctx context.Context, request agentharness.OperationRequest) (agentharness.OperationAdmission, error) {
	if err := lane.AssertOpen(); err != nil {
		if _, closed := err.(*harness.HarnessClosed); closed { //nolint:errorlint // Upstream instanceof checks the outer error, not a wrapped cause.
			return agentharness.OperationAdmission{}, &harness.Closed{Message: err.Error()}
		}
		return agentharness.OperationAdmission{}, err
	}
	startedAt := time.Now().UnixMilli()
	operationID := ""
	if request.OperationID != nil {
		operationID = *request.OperationID
	} else {
		operationID = lane.Session.IdGenerator().Next(&startedAt)
	}
	config := lane.ReadConfig()
	switch request.Kind {
	case agentharness.RequestCompaction:
		return lane.acceptCompaction(ctx, request, operationID, startedAt, config)
	case agentharness.RequestNavigation:
		return lane.acceptNavigation(ctx, request, operationID, startedAt, config)
	default:
		return lane.acceptRun(ctx, request, operationID, startedAt, config)
	}
}

func (lane *Lane) busy(state LaneState) *harness.LaneBusy {
	return &harness.LaneBusy{Lane: lane.name, OperationID: state.Operation.Meta.OperationID, OperationKind: state.Operation.Meta.Intent.Kind, Message: "Lane " + restoreQuote(lane.name) + " already has an active operation"}
}

func (lane *Lane) acceptRun(ctx context.Context, request agentharness.OperationRequest, operationID string, startedAt int64, config Config) (agentharness.OperationAdmission, error) {
	messages := request.PromptMessages
	switch request.Kind {
	case agentharness.RequestPrompt:
		if request.PromptText != nil {
			content := make(ai.UserContentBlocks, 0, 1+len(request.Images))
			if *request.PromptText != "" {
				content = append(content, ai.TextContent{Text: *request.PromptText})
			}
			for _, image := range request.Images {
				content = append(content, image)
			}
			messages = nil
			if len(content) > 0 {
				messages = []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: content, Timestamp: startedAt}}}
			}
		}
	case agentharness.RequestSkill:
		index := slices.IndexFunc(config.Resources.Skills, func(skill harness.Skill) bool { return skill.Name == request.Name })
		if index < 0 {
			return agentharness.OperationAdmission{}, &harness.UnknownSkill{Name: request.Name, Message: "Unknown skill: " + request.Name}
		}
		instructions := ""
		if request.AdditionalInstructions != nil {
			instructions = *request.AdditionalInstructions
		}
		content := harness.FormatSkillInvocation(config.Resources.Skills[index], instructions)
		messages = []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: content}}, Timestamp: startedAt}}}
	case agentharness.RequestPromptTemplate:
		index := slices.IndexFunc(config.Resources.PromptTemplates, func(template harness.PromptTemplate) bool { return template.Name == request.Name })
		if index < 0 {
			return agentharness.OperationAdmission{}, &harness.UnknownTemplate{Name: request.Name, Message: "Unknown prompt template: " + request.Name}
		}
		content := harness.FormatPromptTemplateInvocation(config.Resources.PromptTemplates[index], request.Args)
		messages = nil
		if content != "" {
			messages = []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: content}}, Timestamp: startedAt}}}
		}
	}
	if slices.ContainsFunc(messages, session.IsPendingAssistant) {
		return agentharness.OperationAdmission{}, &harness.InvalidMessage{Lane: lane.name, Reason: "pending_assistant", Message: "Cannot accept a pending assistant message"}
	}
	prompt := make([]session.Entry, len(messages))
	promptIDs := make([]string, len(messages))
	for index, message := range messages {
		id := lane.Session.IdGenerator().Next(&startedAt)
		prompt[index] = session.Entry{ID: id, Type: session.EntryTypeMessage, Message: message}
		promptIDs[index] = id
	}
	return Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[agentharness.OperationAdmission], error) {
		if state.Operation != nil {
			return LaneCommand[agentharness.OperationAdmission]{Kind: CommandReject, Error: lane.busy(state)}, nil
		}
		selected, inbox := selectAcceptedInbox(state.Inbox, config.SteeringMode, config.FollowUpMode)
		captured := make([]session.Entry, len(selected))
		err := parallelReads(len(selected), func(index int) error {
			item := selected[index]
			stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(item.EntryID))
			if err != nil {
				return err
			}
			if stored == nil {
				return &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its payload", item.Kind, item.EntryID)}
			}
			if item.Kind != session.InboxWrite && stored.Value.Type != session.PendingEntryMessage {
				return &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is not a message", item.Kind, item.EntryID)}
			}
			if stored.Value.Type == session.PendingEntryMessage && session.IsPendingAssistant(stored.Value.Message) {
				return &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s contains a pending assistant", item.Kind, item.EntryID)}
			}
			captured[index] = pendingEntryWrite(item.EntryID, stored.Value)
			return nil
		})
		if err != nil {
			return LaneCommand[agentharness.OperationAdmission]{}, err
		}
		hasConversation := slices.ContainsFunc(selected, func(item session.InboxItem) bool { return item.Kind != session.InboxWrite })
		if len(prompt) == 0 && !hasConversation {
			return LaneCommand[agentharness.OperationAdmission]{Kind: CommandReject, Error: &harness.InvalidMessage{Lane: lane.name, Reason: "empty", Message: "Acceptance must append at least one message"}}, nil
		}
		entries := ChainEntries(state.TipID, append(captured, prompt...))
		tip := entries[len(entries)-1].ID
		meta := session.OperationMeta{OperationID: operationID, Lane: lane.name, SourceTipID: state.TipID, StartedAt: startedAt, Intent: session.OperationIntent{Kind: session.OperationKindRun, PromptEntryIDs: promptIDs}}
		operationState := session.OperationState{At: session.AtStarting, OperationScope: session.OperationScope{Control: session.Control{Status: session.ControlRunning}, Settings: capturedSettings(config)}}
		queues, err := ReadLaneQueues(ctx, reader, inbox)
		if err != nil {
			return LaneCommand[agentharness.OperationAdmission]{}, err
		}
		next := state
		next.TipID, next.Inbox, next.Operation = &tip, inbox, &session.Operation{Meta: meta, State: operationState}
		writes := make([]session.Write, 0, len(entries)+len(selected)+4)
		for _, entry := range entries {
			writes = append(writes, session.InsertEntry(entry))
		}
		for _, item := range selected {
			writes = append(writes, session.DeleteValue(session.PendingEntryValue(item.EntryID)))
		}
		writes = append(writes, session.SetValue(session.BranchTip(lane.name), &tip), session.SetValue(session.OperationMetaValue(operationID), meta), session.SetValue(session.OperationStateValue(operationID), operationState), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, &operationID, inbox, state.LastOperationID)))
		return LaneCommand[agentharness.OperationAdmission]{Kind: CommandCommit, Writes: writes, Next: next,
			Materialize: func(session.CommitResult) agentharness.OperationAdmission {
				return agentharness.OperationAdmission{OperationID: operationID, Kind: agentharness.OperationRun, StartedAt: startedAt}
			},
			Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
				events := []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.RunStartPayload{RunID: operationID, StartedAt: startedAt}}}
				events = append(events, CommittedEntryEvents(entries, commit, lane.name, operationID, 0)...)
				if len(selected) > 0 {
					events = append(events, agentharness.HarnessEvent{Lane: lane.name, Payload: agentharness.QueueUpdatePayload{Queues: queues}})
				}
				return events
			}}, nil
	})
}

func (lane *Lane) acceptCompaction(ctx context.Context, request agentharness.OperationRequest, operationID string, startedAt int64, config Config) (agentharness.OperationAdmission, error) {
	taskID := lane.Session.IdGenerator().Next(&startedAt)
	return Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[agentharness.OperationAdmission], error) {
		if state.Operation != nil {
			return LaneCommand[agentharness.OperationAdmission]{Kind: CommandReject, Error: lane.busy(state)}, nil
		}
		var path []session.Entry
		if state.TipID != nil {
			var err error
			path, err = reader.ScanBranch(ctx, session.StorageBranchScan{Start: *state.TipID, StopAtType: session.EntryTypeCompaction, Order: session.OrderNewestFirst})
			if err != nil {
				return LaneCommand[agentharness.OperationAdmission]{}, err
			}
			slices.Reverse(path)
		}
		prepared, err := compaction.PrepareCompaction(path, config.Compaction)
		if err != nil {
			return LaneCommand[agentharness.OperationAdmission]{}, err
		}
		if prepared == nil {
			return LaneCommand[agentharness.OperationAdmission]{Kind: CommandReject, Error: &harness.NothingToCompact{Lane: lane.name, Message: "Lane " + restoreQuote(lane.name) + " has nothing to compact"}}, nil
		}
		meta := session.OperationMeta{OperationID: operationID, Lane: lane.name, SourceTipID: state.TipID, StartedAt: startedAt, Intent: session.OperationIntent{Kind: session.OperationKindCompaction, CustomInstructions: request.CustomInstructions}}
		operationState := session.OperationState{At: session.AtSummaryDeciding, OperationScope: session.OperationScope{Control: session.Control{Status: session.ControlRunning}, Settings: capturedSettings(config)}, Task: session.SummaryTask{TaskID: taskID, Reason: session.SummaryReasonManual, CustomInstructions: request.CustomInstructions, Boundary: session.ResultBoundary{Kind: session.BoundaryFinish}}}
		next := state
		next.Operation = &session.Operation{Meta: meta, State: operationState}
		return LaneCommand[agentharness.OperationAdmission]{Kind: CommandCommit, Next: next, Writes: []session.Write{
			session.SetValue(session.OperationPreparation(operationID, taskID), DurableCompactionPreparation(*prepared)),
			session.SetValue(session.OperationMetaValue(operationID), meta), session.SetValue(session.OperationStateValue(operationID), operationState), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, &operationID, state.Inbox, state.LastOperationID))},
			Materialize: func(session.CommitResult) agentharness.OperationAdmission {
				return agentharness.OperationAdmission{OperationID: operationID, Kind: agentharness.OperationCompaction, StartedAt: startedAt}
			},
			Events: func(session.CommitResult) []agentharness.HarnessEvent {
				return []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.CompactionStartPayload{RunID: operationID, Reason: "manual", StartedAt: startedAt}}}
			}}, nil
	})
}

func (lane *Lane) acceptNavigation(ctx context.Context, request agentharness.OperationRequest, operationID string, startedAt int64, config Config) (agentharness.OperationAdmission, error) {
	taskID := lane.Session.IdGenerator().Next(&startedAt)
	targetID := request.TargetID
	options := agentharness.NavigateOptions{}
	if request.NavigateOptions != nil {
		options = *request.NavigateOptions
	}
	for {
		observedTip := lane.SnapshotState().TipID
		var preparation *compaction.BranchPreparation
		if options.Summarize && observedTip != nil && targetID != nil {
			target, err := lane.Session.GetEntries(ctx, []string{*targetID})
			if err != nil {
				return agentharness.OperationAdmission{}, err
			}
			if _, exists := target[*targetID]; exists {
				var paths [2][]session.Entry
				err := parallelReads(2, func(index int) error {
					start := *observedTip
					if index == 1 {
						start = *targetID
					}
					var err error
					paths[index], err = lane.Session.ScanBranch(ctx, session.StorageBranchScan{Start: start, Order: session.OrderNewestFirst})
					return err
				})
				if err != nil {
					return agentharness.OperationAdmission{}, err
				}
				oldIDs := make(map[string]int, len(paths[0]))
				for index, entry := range paths[0] {
					oldIDs[entry.ID] = index
				}
				end := len(paths[0])
				for _, entry := range paths[1] {
					if index, common := oldIDs[entry.ID]; common {
						end = index
						break
					}
				}
				entries := paths[0][:end]
				slices.Reverse(entries)
				prepared := compaction.PrepareBranchEntries(entries, 0)
				preparation = &prepared
			}
		}
		accepted, err := Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[*agentharness.OperationAdmission], error) {
			if state.Operation != nil {
				return LaneCommand[*agentharness.OperationAdmission]{Kind: CommandReject, Error: lane.busy(state)}, nil
			}
			if !sameOptionalString(state.TipID, observedTip) {
				return LaneCommand[*agentharness.OperationAdmission]{Kind: CommandReturn}, nil
			}
			reason, message := "", ""
			switch {
			case sameOptionalString(targetID, state.TipID):
				reason, message = "current_tip", "Navigation target must differ from the current tip"
			case targetID == nil && options.Label != nil:
				reason, message = "root_label", "Root navigation cannot set a label"
			case options.Summarize && state.TipID == nil:
				reason, message = "source_root", "Summarized navigation requires non-root source and target entries"
			case options.Summarize && targetID == nil:
				reason, message = "target_root", "Summarized navigation requires non-root source and target entries"
			}
			if reason != "" {
				return LaneCommand[*agentharness.OperationAdmission]{Kind: CommandReject, Error: &harness.InvalidNavigation{Lane: lane.name, Reason: reason, Message: message}}, nil
			}
			if targetID != nil {
				entries, err := reader.GetEntries(ctx, []string{*targetID})
				if err != nil {
					return LaneCommand[*agentharness.OperationAdmission]{}, err
				}
				if _, found := entries[*targetID]; !found {
					return LaneCommand[*agentharness.OperationAdmission]{Kind: CommandReject, Error: &harness.UnknownTarget{TargetID: *targetID, Message: "Unknown target: " + *targetID}}, nil
				}
			}
			meta := session.OperationMeta{OperationID: operationID, Lane: lane.name, SourceTipID: state.TipID, StartedAt: startedAt, Intent: session.OperationIntent{Kind: session.OperationKindNavigation, TargetID: targetID, Summarize: options.Summarize, Label: options.Label, CustomInstructions: options.CustomInstructions}}
			operationState := session.OperationState{At: session.AtNavigationReadyToCommit, OperationScope: session.OperationScope{Control: session.Control{Status: session.ControlRunning}, Settings: capturedSettings(config)}, TargetID: targetID, Label: options.Label}
			writes := []session.Write{}
			if options.Summarize {
				if state.TipID == nil || targetID == nil || preparation == nil {
					return LaneCommand[*agentharness.OperationAdmission]{}, &session.SessionInvariantError{Message: "Validated summarized navigation is missing its preparation"}
				}
				writes = append(writes, session.SetValue(session.OperationPreparation(operationID, taskID), DurableBranchPreparation(*preparation)))
				operationState = session.OperationState{At: session.AtSummaryDeciding, OperationScope: operationState.OperationScope, Task: session.SummaryTask{TaskID: taskID, CustomInstructions: options.CustomInstructions, Boundary: session.ResultBoundary{Kind: session.BoundaryCommitNavigation, TargetID: *targetID, Label: options.Label}}}
			}
			writes = append(writes, session.SetValue(session.OperationMetaValue(operationID), meta), session.SetValue(session.OperationStateValue(operationID), operationState), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, &operationID, state.Inbox, state.LastOperationID)))
			next := state
			next.Operation = &session.Operation{Meta: meta, State: operationState}
			return LaneCommand[*agentharness.OperationAdmission]{Kind: CommandCommit, Writes: writes, Next: next,
				Materialize: func(session.CommitResult) *agentharness.OperationAdmission {
					return &agentharness.OperationAdmission{OperationID: operationID, Kind: agentharness.OperationNavigation, StartedAt: startedAt}
				},
				Events: func(session.CommitResult) []agentharness.HarnessEvent {
					return []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.NavigationStartPayload{RunID: operationID, TargetID: targetID, StartedAt: startedAt}}}
				}}, nil
		})
		if err != nil {
			return agentharness.OperationAdmission{}, err
		}
		if accepted != nil {
			return *accepted, nil
		}
	}
}
