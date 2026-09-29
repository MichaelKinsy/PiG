package runtime

// Ports packages/agent/src/harness/runtime/reducer.ts.

import (
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// LaneSnapshotReduction is empty for an applied or ignored event, or "rebase" when the caller must capture a fresh snapshot.
type LaneSnapshotReduction string

func upsertTool(operation *agentharness.LaneSnapshotOperation, tool agentharness.LaneSnapshotTool) {
	index := slices.IndexFunc(operation.RunningTools, func(candidate agentharness.LaneSnapshotTool) bool { return candidate.ToolCallID == tool.ToolCallID })
	if index == -1 {
		operation.RunningTools = append(operation.RunningTools, tool)
	} else {
		operation.RunningTools[index] = tool
	}
}

func matchingOperation(snapshot *agentharness.LaneSnapshot, operationID string) *agentharness.LaneSnapshotOperation {
	if snapshot.Operation != nil && snapshot.Operation.ID == operationID {
		return snapshot.Operation
	}
	return nil
}

func startOperation(snapshot *agentharness.LaneSnapshot, id string, kind agentharness.OperationKind, startedAt int64) {
	snapshot.Operation = &agentharness.LaneSnapshotOperation{
		ID: id, Kind: kind, StartedAt: startedAt, FromTipID: copyTipID(snapshot.TipID),
		Status: agentharness.OperationStatusOpen, RunningTools: []agentharness.LaneSnapshotTool{},
	}
}

// ReduceLaneSnapshot applies one harness event to a mutable lane snapshot. Navigation completion returns "rebase" without changing the snapshot. Nullable string values are copied; object payload references are retained. Event delivery and snapshot capture own isolation from authoritative state.
func ReduceLaneSnapshot(snapshot *agentharness.LaneSnapshot, event agentharness.HarnessEvent) LaneSnapshotReduction {
	if event.Lane != "" && event.Lane != snapshot.Lane && event.Type() != agentharness.EventUsage {
		return ""
	}
	switch payload := event.Payload.(type) {
	case agentharness.RunStartPayload:
		startOperation(snapshot, payload.RunID, agentharness.OperationRun, payload.StartedAt)
	case agentharness.CompactionStartPayload:
		if snapshot.Operation == nil {
			startOperation(snapshot, payload.RunID, agentharness.OperationCompaction, payload.StartedAt)
		}
	case agentharness.NavigationStartPayload:
		startOperation(snapshot, payload.RunID, agentharness.OperationNavigation, payload.StartedAt)
	case agentharness.OperationAbortPayload:
		if operation := matchingOperation(snapshot, payload.OperationID); operation != nil {
			operation.Status = agentharness.OperationStatusAborting
		}
	case agentharness.RunResumePayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			operation.Deferred = nil
		}
	case agentharness.RunSuspendPayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			operation.StreamingMessage = nil
			operation.Deferred = &agentharness.LaneSnapshotDeferred{Handle: payload.Deferred, Poll: payload.Poll}
		}
	case agentharness.RetryScheduledPayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			operation.Retry = &agentharness.LaneSnapshotRetry{Attempt: payload.Attempt, MaxAttempts: payload.MaxAttempts, NextAttemptAt: payload.NotBefore}
		}
	case agentharness.RetryStartPayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			operation.Retry = nil
		}
	case agentharness.RetryEndPayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			operation.Retry = nil
		}
	case agentharness.MessageStartPayload:
		if payload.RunID == "" || payload.Message.Role() != agent.RoleAssistant || payload.Message.Assistant.StopReason != ai.StopReasonPending {
			return ""
		}
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			operation.StreamingMessage = payload.Message.Assistant
		}
	case agentharness.MessageUpdatePayload:
		if payload.Message.Role() != agent.RoleAssistant {
			return ""
		}
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			operation.StreamingMessage = payload.Message.Assistant
		}
	case agentharness.MessageEndPayload:
		if payload.RunID != "" {
			if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
				operation.StreamingMessage = nil
			}
		}
	case agentharness.ToolStartPayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			upsertTool(operation, agentharness.LaneSnapshotTool{Status: "running", ToolCallID: payload.ToolCallID, ToolName: payload.ToolName, Args: payload.Args})
		}
	case agentharness.ToolUpdatePayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			for index := range operation.RunningTools {
				tool := &operation.RunningTools[index]
				if tool.ToolCallID == payload.ToolCallID {
					if tool.Status == "running" {
						tool.Result = &payload.PartialResult
					}
					break
				}
			}
		}
	case agentharness.ToolEndPayload:
		if operation := matchingOperation(snapshot, payload.RunID); operation != nil {
			for index, current := range operation.RunningTools {
				if current.ToolCallID == payload.ToolCallID {
					operation.RunningTools[index] = agentharness.LaneSnapshotTool{Status: "settled", ToolCallID: payload.ToolCallID, ToolName: payload.ToolName, Args: current.Args, Result: &payload.Result, IsError: payload.IsError}
					break
				}
			}
		}
	case agentharness.EntryAddedPayload:
		if payload.Entry.Type == session.EntryTypeMessage && payload.Entry.Message.Role() == agent.RoleToolResult && snapshot.Operation != nil {
			tools := snapshot.Operation.RunningTools
			index := slices.IndexFunc(tools, func(tool agentharness.LaneSnapshotTool) bool {
				return tool.ToolCallID == payload.Entry.Message.ToolResult.ToolCallID
			})
			if index != -1 {
				snapshot.Operation.RunningTools = slices.Delete(tools, index, index+1)
			}
		}
		if payload.Entry.Type == session.EntryTypeCompaction {
			clear(snapshot.Transcript)
			snapshot.Transcript = append(snapshot.Transcript[:0], payload.Entry)
		} else {
			snapshot.Transcript = append(snapshot.Transcript, payload.Entry)
		}
		snapshot.TipID = &payload.Entry.ID
		if payload.Entry.Type == session.EntryTypeMessage {
			snapshot.Stats.MessageCount++
		}
	case agentharness.QueueUpdatePayload:
		snapshot.Queues = payload.Queues
	case agentharness.UsagePayload:
		snapshot.Stats.Usage = payload.Totals
	case agentharness.ConfigUpdatePayload:
		if event.Lane != snapshot.Lane {
			return ""
		}
		switch payload.Property {
		case agentharness.ConfigModel:
			snapshot.Configuration.Model = payload.Value.(session.ModelRef)
		case agentharness.ConfigThinkingLevel:
			snapshot.Configuration.ThinkingLevel = payload.Value.(ai.ThinkingLevel)
		case agentharness.ConfigActiveTools:
			snapshot.Configuration.ActiveToolNames = payload.Value.([]string)
		}
	case agentharness.RunEndPayload:
		operation := matchingOperation(snapshot, payload.RunID)
		if operation == nil || operation.Kind != agentharness.OperationRun {
			return ""
		}
		record := &session.OperationResultRecord{
			OperationID: payload.RunID, Kind: "run", Status: payload.Status,
			FromTipID: copyTipID(payload.FromTipID), TipID: copyTipID(payload.TipID), StartedAt: operation.StartedAt, EndedAt: payload.EndedAt,
		}
		if payload.Status == session.TerminalFailed {
			record.Error = payload.Error
		}
		snapshot.LastResult, snapshot.Operation, snapshot.TipID = record, nil, copyTipID(payload.TipID)
	case agentharness.CompactionEndPayload:
		operation := matchingOperation(snapshot, payload.RunID)
		if operation == nil || operation.Kind != agentharness.OperationCompaction {
			return ""
		}
		record := &session.OperationResultRecord{
			OperationID: payload.RunID, Kind: "compaction", Status: payload.Status,
			FromTipID: copyTipID(operation.FromTipID), TipID: copyTipID(snapshot.TipID), StartedAt: operation.StartedAt, EndedAt: payload.EndedAt,
		}
		if payload.Status == session.TerminalFailed {
			record.Error = payload.Error
		}
		snapshot.LastResult, snapshot.Operation = record, nil
	case agentharness.NavigationEndPayload:
		return "rebase"
	case agentharness.FaultPayload:
		snapshot.Faulted = true
	case agentharness.HandlerErrorPayload, agentharness.TurnStartPayload, agentharness.TurnEndPayload, agentharness.ValueUpdatePayload, agentharness.LaneCreatedPayload:
	}
	return ""
}
