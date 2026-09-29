package runtime

// Ports packages/agent/src/harness/runtime/drive/tool-placement.ts

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// ToolBatchSource binds planned source indexes to their immutable assistant tool calls.
type ToolBatchSource struct {
	Assistant agent.AssistantMessage
	Calls     map[int]ai.ToolCall
}

// ReadToolBatchSource validates the batch's source assistant and tool-call indexes.
func ReadToolBatchSource(ctx harness.Context, lane *Lane, drive *Drive, batch session.ToolBatch) (ToolBatchSource, error) {
	return Command(ctx, lane, func(_ LaneState, reader session.SessionReader) (LaneCommand[ToolBatchSource], error) {
		entries, err := reader.GetEntries(ctx, []string{batch.AssistantEntryID})
		if err != nil {
			return LaneCommand[ToolBatchSource]{}, err
		}
		entry, ok := entries[batch.AssistantEntryID]
		if !ok || entry.Type != session.EntryTypeMessage || entry.Message.Assistant == nil {
			return LaneCommand[ToolBatchSource]{}, &session.SessionInvariantError{Message: "Tool batch assistant entry is invalid"}
		}
		calls := make(map[int]ai.ToolCall, len(batch.Calls))
		for _, call := range batch.Calls {
			if call.SourceIndex < 0 || call.SourceIndex >= len(entry.Message.Assistant.Content) {
				return LaneCommand[ToolBatchSource]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d does not name a tool-call block", call.SourceIndex)}
			}
			block, ok := entry.Message.Assistant.Content[call.SourceIndex].(ai.ToolCall)
			if !ok {
				return LaneCommand[ToolBatchSource]{}, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d does not name a tool-call block", call.SourceIndex)}
			}
			calls[call.SourceIndex] = block
		}
		return LaneCommand[ToolBatchSource]{Kind: CommandReturn, Result: ToolBatchSource{Assistant: *entry.Message.Assistant, Calls: calls}}, nil
	})
}

// ToolCallFor resolves one source index in a validated batch.
func ToolCallFor(sources ToolBatchSource, call session.ToolCall) (ai.ToolCall, error) {
	value, ok := sources.Calls[call.SourceIndex]
	if !ok {
		return ai.ToolCall{}, &session.SessionInvariantError{Message: fmt.Sprintf("Tool call source index %d is invalid", call.SourceIndex)}
	}
	return value, nil
}

// WithToolBatch replaces the tools leaf while retaining only its uniform operation scope.
func WithToolBatch(run session.OperationState, batch session.ToolBatch) session.OperationState {
	return session.OperationState{OperationScope: run.OperationScope, At: session.AtTools, Batch: batch}
}

type placementItem struct {
	call    session.ToolCall
	message agent.ToolResultMessage
}
type placementRead struct {
	items       []placementItem
	turnResults []agent.ToolResultMessage
}

func readPlacement(ctx harness.Context, lane *Lane, sources ToolBatchSource) (*placementRead, error) {
	return Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[*placementRead], error) {
		none := LaneCommand[*placementRead]{Kind: CommandReturn}
		if state.Operation == nil || state.Operation.State.At != session.AtTools {
			return none, nil
		}
		current := state.Operation.State.Batch
		first := 0
		for first < len(current.Calls) && current.Calls[first].Status == session.ToolCallCompleted {
			first++
		}
		read := &placementRead{}
		for first < len(current.Calls) && current.Calls[first].Status == session.ToolCallOutcomeReady {
			call := current.Calls[first]
			first++
			stored, err := session.GetValue(ctx, reader, session.PendingEntryValue(call.ResultEntryID))
			if err != nil {
				return none, err
			}
			if stored == nil || stored.Value.Type != session.PendingEntryMessage || stored.Value.Message.ToolResult == nil {
				return none, &session.SessionInvariantError{Message: "Tool call " + call.ResultEntryID + " is missing its staged result"}
			}
			source, err := ToolCallFor(sources, call)
			if err != nil {
				return none, err
			}
			message := *stored.Value.Message.ToolResult
			if message.ToolCallID != source.ID || message.ToolName != source.Name {
				return none, &session.SessionInvariantError{Message: "Tool call " + call.ResultEntryID + " has a mismatched staged result"}
			}
			read.items = append(read.items, placementItem{call: call, message: message})
		}
		if len(read.items) == 0 {
			return none, nil
		}
		if first == len(current.Calls) {
			ids := []string{}
			for _, call := range current.Calls {
				if call.Status == session.ToolCallCompleted {
					ids = append(ids, call.ResultEntryID)
				}
			}
			placed, err := reader.GetEntries(ctx, ids)
			if err != nil {
				return none, err
			}
			staged := map[string]agent.ToolResultMessage{}
			for _, item := range read.items {
				staged[item.call.ResultEntryID] = item.message
			}
			read.turnResults = []agent.ToolResultMessage{}
			for _, call := range current.Calls {
				message, ok := staged[call.ResultEntryID]
				if !ok {
					entry, exists := placed[call.ResultEntryID]
					if !exists || entry.Type != session.EntryTypeMessage || entry.Message.ToolResult == nil {
						return none, &session.SessionInvariantError{Message: "Completed tool call " + call.ResultEntryID + " is missing its result entry"}
					}
					message = *entry.Message.ToolResult
				}
				read.turnResults = append(read.turnResults, message)
			}
		}
		return LaneCommand[*placementRead]{Kind: CommandReturn, Result: read}, nil
	})
}

func commitPlacement(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState, read *placementRead) (bool, error) {
	usageIDs := make([]string, len(read.items))
	for i, item := range read.items {
		if item.message.Usage != nil {
			usageIDs[i] = lane.Session.IdGenerator().Next(nil)
		}
	}
	return SettleOperation(ctx, lane, capability, func(state LaneState, run session.OperationState, _ session.OperationMeta, reader session.SessionReader) (OperationCommand[bool], error) {
		writes := []session.Write{}
		type eventEntry struct {
			entry    session.Entry
			seqIndex int
		}
		type eventUsage struct {
			row      session.UsageRow
			seqIndex int
		}
		entries := []eventEntry{}
		usages := []eventUsage{}
		parent := state.TipID
		for index, item := range read.items {
			entry := session.Entry{ID: item.call.ResultEntryID, ParentID: parent, Type: session.EntryTypeMessage, Message: agent.AgentMessage{ToolResult: new(item.message)}, Terminate: item.call.Terminate}
			entries = append(entries, eventEntry{entry: entry, seqIndex: len(writes)})
			writes = append(writes, session.InsertEntry(entry), session.DeleteValue(session.PendingEntryValue(item.call.ResultEntryID)))
			if usageIDs[index] != "" && item.message.Usage != nil {
				row := session.UsageRow{ID: usageIDs[index], Usage: *item.message.Usage, EntryID: new(item.call.ResultEntryID)}
				usages = append(usages, eventUsage{row: row, seqIndex: len(writes)})
				writes = append(writes, session.InsertUsage(row))
			}
			parent = new(item.call.ResultEntryID)
		}
		calls := append([]session.ToolCall{}, run.Batch.Calls...)
		complete, allTerminate := true, true
		for i, call := range calls {
			for _, item := range read.items {
				if item.call.SourceIndex == call.SourceIndex && item.call.ResultEntryID == call.ResultEntryID {
					calls[i] = session.ToolCall{Status: session.ToolCallCompleted, SourceIndex: call.SourceIndex, ResultEntryID: call.ResultEntryID, Terminate: item.call.Terminate}
					break
				}
			}
			complete = complete && calls[i].Status == session.ToolCallCompleted
			allTerminate = allTerminate && calls[i].Status == session.ToolCallCompleted && calls[i].Terminate
		}
		writes = append(writes, session.SetValue(session.BranchTip(lane.Name()), parent))
		batch := run.Batch
		batch.Calls = calls
		next := WithToolBatch(run, batch)
		if complete {
			continuation := session.Continuation{Kind: session.ContinuationNeedAssistant}
			if allTerminate {
				continuation.Kind = session.ContinuationMayFinish
			}
			next = session.OperationState{OperationScope: run.OperationScope, At: session.AtCheckpoint, Continuation: continuation, TriggerEntryID: *parent}
			args, err := session.ScanValues(ctx, reader, session.OperationToolArgsPrefix(drive.OperationID, &capability.Batch.TurnID))
			if err != nil {
				return OperationCommand[bool]{}, err
			}
			for _, arg := range args {
				writes = append(writes, session.DeleteValue(arg.Address))
			}
		}
		return OperationCommand[bool]{Kind: CommandCommit, Writes: writes, OperationState: next, Lane: &LanePatch{SetTipID: true, TipID: parent, SetConfiguration: true, Configuration: state.Configuration}, Materialize: func(session.CommitResult) bool { return complete }, Events: func(commit session.CommitResult) []agentharness.HarnessEvent {
			batch := []agentharness.HarnessEvent{}
			for _, item := range entries {
				entry := item.entry
				entry.Seq = commit.Seqs[item.seqIndex]
				entry.Timestamp = commit.Timestamp
				batch = append(batch, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.EntryAddedPayload{Entry: entry}})
				for _, usage := range usages {
					if usage.row.EntryID != nil && *usage.row.EntryID == entry.ID {
						row := usage.row
						row.Seq = commit.Seqs[usage.seqIndex]
						batch = append(batch, agentharness.HarnessEvent{Lane: lane.Name(), Payload: agentharness.UsagePayload{Row: row, Totals: commit.Stats.Usage}})
						break
					}
				}
			}
			return batch
		}}, nil
	})
}

// MaterializeReady publishes and places the contiguous source-order prefix of staged tool outcomes.
func MaterializeReady(ctx harness.Context, lane *Lane, drive *Drive, capability session.OperationState, sources ToolBatchSource, recovery bool) error {
	read, err := readPlacement(ctx, lane, sources)
	if err != nil || read == nil {
		return err
	}
	events := []agentharness.HarnessEvent{}
	for _, item := range read.items {
		message := agent.AgentMessage{ToolResult: new(item.message)}
		events = append(events,
			agentharness.HarnessEvent{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.MessageStartPayload{RunID: drive.OperationID, Message: message}},
			agentharness.HarnessEvent{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.MessageEndPayload{RunID: drive.OperationID, Message: message, EntryID: item.call.ResultEntryID}})
	}
	if err := lane.EmitBatch(ctx, events); err != nil {
		return err
	}
	complete, err := commitPlacement(ctx, lane, drive, capability, read)
	if err != nil {
		return err
	}
	if complete && read.turnResults != nil {
		return lane.EmitBatch(ctx, []agentharness.HarnessEvent{{Lane: lane.Name(), Recovery: recovery, Payload: agentharness.TurnEndPayload{RunID: drive.OperationID, TurnID: capability.Batch.TurnID, Message: sources.Assistant, ToolResults: read.turnResults}}})
	}
	return nil
}
