package runtime

// Ports packages/agent/src/harness/runtime/lane.ts.

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func pendingEntryWrite(id string, pending session.PendingEntry) session.Entry {
	entry := session.Entry{ID: id}
	if pending.Type == session.PendingEntryMessage {
		entry.Type = session.EntryTypeMessage
		entry.Message = pending.Message
	} else {
		entry.Type = session.EntryTypeCustom
		entry.CustomType = pending.CustomType
		entry.Data = pending.CustomPayload
	}
	return entry
}
func queuedPending(id, kind string, pending session.PendingEntry) agentharness.LaneQueuedItem {
	queued := agentharness.LaneQueuedItem{EntryID: id, Kind: kind, Type: pending.Type, Message: pending.Message, CustomType: pending.CustomType}
	if pending.CustomPayload != nil {
		queued.HasData = true
		queued.Data = *pending.CustomPayload
	}
	return queued
}
func (lane *Lane) AppendMessage(ctx harness.Context, message agent.AgentMessage) (string, error) {
	return lane.append(ctx, session.PendingEntry{Type: session.PendingEntryMessage, Message: message})
}
func (lane *Lane) AppendCustomEntry(ctx harness.Context, customType string, data *session.JsonValue) (string, error) {
	return lane.append(ctx, session.PendingEntry{Type: session.PendingEntryCustom, CustomType: customType, CustomPayload: data})
}
func (lane *Lane) append(ctx harness.Context, pending session.PendingEntry) (string, error) {
	if err := lane.AssertOpen(); err != nil {
		return "", err
	}
	if pending.Type == session.PendingEntryMessage && session.IsPendingAssistant(pending.Message) {
		return "", &session.SessionPendingAssistantMessageError{}
	}
	id := lane.Session.IdGenerator().Next(nil)
	return Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[string], error) {
		next := state
		if state.Operation == nil {
			entries := []session.Entry{}
			writes := []session.Write{}
			inbox := []session.InboxItem{}
			hadQueued := false
			for _, item := range state.Inbox {
				if item.Kind != session.InboxWrite {
					inbox = append(inbox, item)
					continue
				}
				stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(item.EntryID))
				if err != nil {
					return LaneCommand[string]{}, err
				}
				if stored == nil {
					return LaneCommand[string]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Pending write %s is missing its payload", item.EntryID)}
				}
				entries = append(entries, pendingEntryWrite(item.EntryID, stored.Value))
				writes = append(writes, session.DeleteValue(session.PendingEntryValue(item.EntryID)))
				hadQueued = true
			}
			entries = ChainEntries(state.TipID, append(entries, pendingEntryWrite(id, pending)))
			entryWrites := make([]session.Write, 0, len(entries)+len(writes)+2)
			for _, entry := range entries {
				entryWrites = append(entryWrites, session.InsertEntry(entry))
			}
			writes = append(entryWrites, writes...)
			writes = append(writes, session.SetValue(session.BranchTip(lane.name), &id), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, nil, inbox, state.LastOperationID)))
			var queues []agentharness.LaneQueuedItem
			if hadQueued {
				var err error
				queues, err = ReadLaneQueues(ctx, reader, inbox)
				if err != nil {
					return LaneCommand[string]{}, err
				}
			}
			next.TipID = &id
			next.Inbox = inbox
			return LaneCommand[string]{Kind: CommandCommit, Writes: writes, Next: next, Materialize: func(session.CommitResult) string { return id }, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
				events := CommittedEntryEvents(entries, commit, lane.name, "", 0)
				if hadQueued {
					events = append(events, agentharness.HarnessEvent{Lane: lane.name, Payload: agentharness.QueueUpdatePayload{Queues: queues}})
				}
				return events
			}}, nil
		}
		inbox := append(slices.Clone(state.Inbox), session.InboxItem{EntryID: id, Kind: session.InboxWrite})
		queues, err := ReadLaneQueues(ctx, reader, state.Inbox)
		if err != nil {
			return LaneCommand[string]{}, err
		}
		queues = append(queues, queuedPending(id, session.InboxWrite, pending))
		next.Inbox = inbox
		return LaneCommand[string]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.PendingEntryValue(id), pending), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, &state.Operation.Meta.OperationID, inbox, state.LastOperationID))}, Next: next, Materialize: func(session.CommitResult) string { return id }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.QueueUpdatePayload{Queues: queues}}}
		}}, nil
	})
}
func (lane *Lane) Steer(ctx harness.Context, message agent.AgentMessage) (string, error) {
	return lane.enqueue(ctx, session.InboxSteer, nil, message, nil)
}
func (lane *Lane) FollowUp(ctx harness.Context, message agent.AgentMessage) (string, error) {
	return lane.enqueue(ctx, session.InboxFollowUp, nil, message, nil)
}
func (lane *Lane) NextRun(ctx harness.Context, message agent.AgentMessage) (string, error) {
	return lane.enqueue(ctx, session.InboxNextRun, nil, message, nil)
}
func (lane *Lane) SteerWithImages(ctx harness.Context, message agent.AgentMessage, images []ai.ImageContent) (string, error) {
	return lane.enqueue(ctx, session.InboxSteer, nil, message, images)
}
func (lane *Lane) FollowUpWithImages(ctx harness.Context, message agent.AgentMessage, images []ai.ImageContent) (string, error) {
	return lane.enqueue(ctx, session.InboxFollowUp, nil, message, images)
}
func (lane *Lane) NextRunWithImages(ctx harness.Context, message agent.AgentMessage, images []ai.ImageContent) (string, error) {
	return lane.enqueue(ctx, session.InboxNextRun, nil, message, images)
}
func (lane *Lane) SteerText(ctx harness.Context, text string, images []ai.ImageContent) (string, error) {
	return lane.enqueue(ctx, session.InboxSteer, &text, agent.AgentMessage{}, images)
}
func (lane *Lane) FollowUpText(ctx harness.Context, text string, images []ai.ImageContent) (string, error) {
	return lane.enqueue(ctx, session.InboxFollowUp, &text, agent.AgentMessage{}, images)
}
func (lane *Lane) NextRunText(ctx harness.Context, text string, images []ai.ImageContent) (string, error) {
	return lane.enqueue(ctx, session.InboxNextRun, &text, agent.AgentMessage{}, images)
}

func (lane *Lane) enqueue(ctx harness.Context, kind string, text *string, input agent.AgentMessage, images []ai.ImageContent) (string, error) {
	if err := lane.expectedOpen(); err != nil {
		return "", err
	}
	at := runtimeNow()
	var message agent.AgentMessage
	if text != nil {
		if *text == "" && len(images) == 0 {
			return "", &harness.InvalidMessage{Lane: lane.name, Reason: "empty", Message: "Queued input must contain text or an image"}
		}
		content := ai.UserContentBlocks{}
		if *text != "" {
			content = append(content, ai.TextContent{Text: *text})
		}
		for _, image := range images {
			content = append(content, image)
		}
		message = agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: content, Timestamp: at}}
	} else {
		if session.IsPendingAssistant(input) {
			return "", &harness.InvalidMessage{Lane: lane.name, Reason: "pending_assistant", Message: "Cannot queue a pending assistant message"}
		}
		if len(images) > 0 && input.User == nil {
			return "", &harness.InvalidMessage{Lane: lane.name, Reason: "images_with_non_user", Message: "Images can be added only to queued user messages"}
		}
		message = input.Clone()
		if message.User != nil && len(images) > 0 {
			var blocks ai.UserContentBlocks
			switch content := message.User.Content.(type) {
			case ai.UserText:
				if content != "" {
					blocks = append(blocks, ai.TextContent{Text: string(content)})
				}
			case ai.UserContentBlocks:
				blocks = content
			}
			for _, image := range images {
				blocks = append(blocks, image)
			}
			message.User.Content = blocks
		}
	}
	id := lane.Session.IdGenerator().Next(&at)
	return Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[string], error) {
		inbox := append(slices.Clone(state.Inbox), session.InboxItem{EntryID: id, Kind: kind})
		queues, err := ReadLaneQueues(ctx, reader, state.Inbox)
		if err != nil {
			return LaneCommand[string]{}, err
		}
		queues = append(queues, agentharness.LaneQueuedItem{EntryID: id, Kind: kind, Type: session.PendingEntryMessage, Message: message})
		next := state
		next.Inbox = inbox
		return LaneCommand[string]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.PendingEntryValue(id), session.PendingEntry{Type: session.PendingEntryMessage, Message: message}), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, currentOperationID(state), inbox, state.LastOperationID))}, Next: next, Materialize: func(session.CommitResult) string { return id }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.QueueUpdatePayload{Queues: queues}}}
		}}, nil
	})
}

func (lane *Lane) CancelQueued(ctx harness.Context, id string) (agentharness.CancelQueuedKind, error) {
	if err := lane.expectedOpen(); err != nil {
		return "", err
	}
	return Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[agentharness.CancelQueuedKind], error) {
		index := slices.IndexFunc(state.Inbox, func(item session.InboxItem) bool { return item.EntryID == id })
		if index < 0 {
			entries, err := reader.GetEntries(ctx, []string{id})
			if err != nil {
				return LaneCommand[agentharness.CancelQueuedKind]{}, err
			}
			result := agentharness.CancelQueuedNotFound
			if _, found := entries[id]; found {
				result = agentharness.CancelQueuedAlreadyConsumed
			}
			return LaneCommand[agentharness.CancelQueuedKind]{Kind: CommandReturn, Result: result}, nil
		}
		stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(id))
		if err != nil {
			return LaneCommand[agentharness.CancelQueuedKind]{}, err
		}
		if stored == nil {
			return LaneCommand[agentharness.CancelQueuedKind]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Queued %s entry %s is missing its payload", state.Inbox[index].Kind, id)}
		}
		inbox := slices.Delete(slices.Clone(state.Inbox), index, index+1)
		queues, err := ReadLaneQueues(ctx, reader, inbox)
		if err != nil {
			return LaneCommand[agentharness.CancelQueuedKind]{}, err
		}
		next := state
		next.Inbox = inbox
		return LaneCommand[agentharness.CancelQueuedKind]{Kind: CommandCommit, Writes: []session.Write{session.DeleteValue(session.PendingEntryValue(id)), session.SetValue(session.LaneStateValue(lane.name), durableLaneState(state, currentOperationID(state), inbox, state.LastOperationID))}, Next: next, Materialize: func(session.CommitResult) agentharness.CancelQueuedKind { return agentharness.CancelQueuedCancelled }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
			return []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.QueueUpdatePayload{Queues: queues}}}
		}}, nil
	})
}

func (lane *Lane) RecordUsage(ctx harness.Context, usage ai.Usage, options *agentharness.RecordUsageOptions) (string, error) {
	if err := lane.expectedOpen(); err != nil {
		return "", err
	}
	return Command(ctx, lane, func(state LaneState, _ session.SessionReader) (LaneCommand[string], error) {
		id := lane.Session.IdGenerator().Next(nil)
		row := session.UsageRow{ID: id, Usage: usage, Adjustment: true}
		if options != nil {
			row.EntryID = options.EntryID
			row.Details = options.Details
		}
		return LaneCommand[string]{Kind: CommandCommit, Writes: []session.Write{session.InsertUsage(row)}, Next: state, Materialize: func(session.CommitResult) string { return id }, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
			row.Seq = commit.Seqs[0]
			return []agentharness.HarnessEvent{{Lane: lane.name, Payload: agentharness.UsagePayload{Row: row, Totals: commit.Stats.Usage}}}
		}}, nil
	})
}
