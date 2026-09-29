package runtime

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// Ports packages/agent/src/harness/runtime/transcript.ts.

// ChainEntries places each entry after the previous entry, starting at parentID.
func ChainEntries(parentID *string, entries []session.Entry) []session.Entry {
	placed := make([]session.Entry, len(entries))
	for index, entry := range entries {
		entry.ParentID = parentID
		placed[index] = entry
		parentID = new(entry.ID)
	}
	return placed
}

// EntryLifecycleEvents projects one committed entry into ordered message and entry events. Empty runID omits run identity.
func EntryLifecycleEvents(entry session.Entry, lane, runID string) []agentharness.HarnessEvent {
	events := make([]agentharness.HarnessEvent, 0, 3)
	if entry.Type == session.EntryTypeMessage {
		events = append(events,
			agentharness.HarnessEvent{Lane: lane, Payload: agentharness.MessageStartPayload{RunID: runID, Message: entry.Message}},
			agentharness.HarnessEvent{Lane: lane, Payload: agentharness.MessageEndPayload{RunID: runID, Message: entry.Message, EntryID: entry.ID}})
	}
	return append(events, agentharness.HarnessEvent{Lane: lane, Payload: agentharness.EntryAddedPayload{Entry: entry}})
}

// CommittedEntryEvents uses storage-assigned sequence numbers and timestamps for the inserted entries.
func CommittedEntryEvents(entries []session.Entry, commit session.CommitResult, lane, runID string, firstWriteIndex int) []agentharness.HarnessEvent {
	events := make([]agentharness.HarnessEvent, 0, len(entries))
	for index, entry := range entries {
		placed := session.MaterializeCommittedEntry(entry, commit.Seqs[firstWriteIndex+index], commit.Timestamp)
		events = append(events, EntryLifecycleEvents(placed, lane, runID)...)
	}
	return events
}

// ReadBoundedEntries reads the current context segment on the lane's mutation line unless cancellation already won.
func ReadBoundedEntries(ctx context.Context, lane *Lane, drive *Drive, capability session.OperationState) (ContinueOperationResult[[]session.Entry], error) {
	return ContinueOperation(ctx, lane, capability, func(state LaneState, _ session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[[]session.Entry], error) {
		if state.TipID == nil {
			return OperationCommand[[]session.Entry]{}, &session.SessionInvariantError{Message: "Run operation has no Branch tip"}
		}
		entries, err := reader.ScanBranch(drive.Context, session.StorageBranchScan{Start: *state.TipID, StopAtType: session.EntryTypeCompaction, Order: session.OrderNewestFirst})
		if err != nil {
			return OperationCommand[[]session.Entry]{}, err
		}
		slices.Reverse(entries)
		return OperationCommand[[]session.Entry]{Kind: CommandReturn, Result: entries}, nil
	})
}

// ReadBoundedContext projects bounded entries outside the mutation line through the configured entry projectors.
func ReadBoundedContext(ctx context.Context, lane *Lane, drive *Drive, capability session.OperationState) (ContinueOperationResult[[]agent.AgentMessage], error) {
	entries, err := ReadBoundedEntries(ctx, lane, drive, capability)
	if err != nil || entries.CancelRequested {
		return ContinueOperationResult[[]agent.AgentMessage]{CancelRequested: entries.CancelRequested}, err
	}
	messages, err := session.BuildSessionContext(drive.Context, entries.Value, &session.SessionContextBuildOptions{EntryProjectors: lane.ReadConfig().EntryProjectors})
	return ContinueOperationResult[[]agent.AgentMessage]{Value: messages}, err
}

// ReadLaneQueues resolves ordered inbox payloads with owned, joined reads.
func ReadLaneQueues(ctx context.Context, reader session.SessionReader, inbox []session.InboxItem) ([]agentharness.LaneQueuedItem, error) {
	queues := make([]agentharness.LaneQueuedItem, len(inbox))
	err := parallelReads(len(inbox), func(index int) error {
		item := inbox[index]
		stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(item.EntryID))
		if err != nil {
			return err
		}
		if stored == nil {
			return &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is missing its payload", item.Kind, item.EntryID)}
		}
		if stored.Value.Type == session.PendingEntryMessage {
			queues[index] = agentharness.LaneQueuedItem{EntryID: item.EntryID, Kind: item.Kind, Type: "message", Message: stored.Value.Message}
			return nil
		}
		if item.Kind != session.InboxWrite {
			return &session.SessionInvariantError{Message: fmt.Sprintf("Pending %s entry %s is not a message", item.Kind, item.EntryID)}
		}
		queued := agentharness.LaneQueuedItem{EntryID: item.EntryID, Kind: "write", Type: "custom", CustomType: stored.Value.CustomType}
		if stored.Value.CustomPayload != nil {
			queued.HasData, queued.Data = true, *stored.Value.CustomPayload
		}
		queues[index] = queued
		return nil
	})
	return queues, err
}

// PendingMessage is a resolved pending entry and its message payload.
type PendingMessage struct {
	EntryID string
	Message agent.AgentMessage
}

// ReadPendingMessages resolves message payloads in the requested ID order.
func ReadPendingMessages(ctx context.Context, reader session.SessionReader, ids []string, description string) ([]PendingMessage, error) {
	messages := make([]PendingMessage, len(ids))
	err := parallelReads(len(ids), func(index int) error {
		entryID := ids[index]
		stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(entryID))
		if err != nil {
			return err
		}
		if stored == nil || stored.Value.Type != session.PendingEntryMessage {
			return &session.SessionInvariantError{Message: fmt.Sprintf("%s %s is missing its message payload", description, entryID)}
		}
		messages[index] = PendingMessage{EntryID: entryID, Message: stored.Value.Message}
		return nil
	})
	return messages, err
}

func parallelReads(count int, read func(int) error) error {
	var group sync.WaitGroup
	var failure sync.Once
	var firstError error
	for index := range count {
		group.Go(func() {
			if err := read(index); err != nil {
				failure.Do(func() { firstError = err })
			}
		})
	}
	group.Wait()
	return firstError
}
