package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func terminalScope() session.OperationScope {
	return session.OperationScope{Control: session.Control{Status: session.ControlRunning}, Settings: session.RunSettings{
		Compaction:   harness.CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 2000},
		SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: session.ToolExecutionParallel,
	}}
}

func writeAddress(write session.Write) string {
	switch write := write.(type) {
	case session.EntryWrite:
		return "entry:" + write.Entry.ID
	case session.UsageWrite:
		return "usage:" + write.Row.ID
	case session.ValueSetWrite:
		return "value:set:" + write.Namespace + ":" + write.Key
	case session.ValueDeleteWrite:
		return "value:delete:" + write.Namespace + ":" + write.Key
	case session.ListAppendWrite:
		return "list:append:" + write.Namespace + ":" + write.Key
	case session.ListDeleteWrite:
		return "list:delete:" + write.Namespace + ":" + write.Key
	default:
		panic(fmt.Sprintf("unexpected write %T", write))
	}
}

func writeAddresses(writes []session.Write) []string {
	addresses := make([]string, len(writes))
	for index, write := range writes {
		addresses[index] = writeAddress(write)
	}
	return addresses
}

func seedOperationLeftovers(t *testing.T, opened session.Session, operationID string, state session.OperationState) {
	t.Helper()
	intent := session.OperationIntent{Kind: session.OperationKindRun, PromptEntryIDs: []string{}}
	switch state.At {
	case session.AtSummaryDeciding:
		intent = session.OperationIntent{Kind: session.OperationKindCompaction}
	case session.AtNavigationReadyToCommit:
		intent = session.OperationIntent{Kind: session.OperationKindNavigation, TargetID: state.TargetID, Summarize: false}
	}
	harnessCommit(t, opened, []session.Write{
		session.SetValue(session.OperationMetaValue(operationID), session.OperationMeta{OperationID: operationID, Lane: "main", StartedAt: 1, Intent: intent}),
		session.SetValue(session.OperationStateValue(operationID), state),
		session.SetValue(session.OperationToolArgs(operationID, "step", 0), map[string]session.JsonValue{"value": true}),
		session.SetValue(session.OperationToolMemo(operationID, "invocation", "memo"), session.JsonValue(map[string]any{"value": true})),
		session.SetValue(session.OperationPreparation(operationID, "task"), session.DurableStructuralPreparation{Kind: session.PreparationBranchSummary, Messages: []agent.AgentMessage{}, FileOps: session.DurableFileOperations{Read: []string{}, Written: []string{}, Edited: []string{}}, TotalTokens: 0}),
		session.SetValue(session.PendingToolOutput(operationID, "invocation"), harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{}}),
	}...)

}

func TestPortWave04RuntimeTerminalCleanup(t *testing.T) {
	t.Parallel()
	// packages/agent/test/harness/runtime/drive-terminal.test.ts:97-173
	t.Run("deletes every operation-owned family and exact live frame list but preserves the lane inbox", func(t *testing.T) {
		opened := harnessSession(t, t.Name(), nil)
		state := session.OperationState{OperationScope: terminalScope(), At: session.AtAssistantEffectPending,
			GenerationContext: session.GenerationContext{StepID: "step", TriggerEntryID: "trigger", Configuration: laneTestConfiguration(), RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 1, MaxAgentDelayMs: 30000}},
			Attempt:           1, ResponseEntryID: "response", UsageID: "usage", IntendedOutputLimit: 100, ContextWindow: 1000,
		}
		state.Control = session.Control{Status: session.ControlCancelRequested, RequestedAt: 2}
		seedOperationLeftovers(t, opened, "run", state)
		writes := []session.Write{}
		for _, id := range []string{"steer", "follow", "write", "next"} {
			payload := session.JsonValue(map[string]any{"id": id})
			writes = append(writes, session.SetValue(session.PendingEntryValue(id), session.PendingEntry{Type: session.PendingEntryCustom, CustomType: "test", CustomPayload: &payload}))
		}
		inbox := []session.InboxItem{{EntryID: "steer", Kind: "steer"}, {EntryID: "follow", Kind: "followUp"}, {EntryID: "write", Kind: "write"}, {EntryID: "next", Kind: "nextRun"}}
		writes = append(writes,
			session.AppendList(session.PendingAssistantFrames("run", "response"), ai.AssistantMessageFrame(ai.TextDeltaFrame{ContentIndex: 0, Delta: "partial"})),
			session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: new("run"), Inbox: inbox}),
		)
		harnessCommit(t, opened, writes...)
		cleanup, err := OperationCleanupWrites(context.Background(), opened, "run", state)
		require.NoError(t, err)
		require.Equal(t, []string{"value:delete:pi.op.meta:run", "value:delete:pi.op.state:run", "value:delete:pi.op.tool_args:run:step:0", "value:delete:pi.op.tool_memo:run:invocation:memo", "value:delete:pi.op.preparation:run:task", "value:delete:pi.pending.tool_output:run:invocation", "list:delete:pi.pending.assistant_frame:run:response"}, writeAddresses(cleanup))
		harnessCommit(t, opened, cleanup...)
		for _, id := range []string{"steer", "follow", "write", "next"} {
			stored, err := session.GetValue(context.Background(), opened, session.PendingEntryValue(id))
			require.NoError(t, err)
			require.NotNil(t, stored)
		}
		lane, err := session.GetValue(context.Background(), opened, session.LaneStateValue("main"))
		require.NoError(t, err)
		require.NotNil(t, lane)
		require.Equal(t, inbox, lane.Value.Inbox)
		frames, err := session.ReadList(context.Background(), opened, session.PendingAssistantFrames("run", "response"), nil)
		require.NoError(t, err)
		require.Equal(t, []session.ListElement[ai.AssistantMessageFrame]{}, frames)
	})
	// packages/agent/test/harness/runtime/drive-terminal.test.ts:175-210
	t.Run("deletes staged tool outcomes and leaves completed results alone", func(t *testing.T) {
		opened := harnessSession(t, t.Name(), nil)
		state := session.OperationState{OperationScope: terminalScope(), At: session.AtTools, Batch: session.ToolBatch{AssistantEntryID: "assistant", Configuration: laneTestConfiguration(), TurnID: "step", Calls: []session.ToolCall{
			{Status: session.ToolCallOutcomeReady, SourceIndex: 0, ResultEntryID: "staged", Terminate: false},
			{Status: session.ToolCallCompleted, SourceIndex: 1, ResultEntryID: "placed", Terminate: false},
		}}}
		seedOperationLeftovers(t, opened, "tools", state)
		harnessCommit(t, opened, []session.Write{session.SetValue(session.PendingEntryValue("staged"), session.PendingEntry{Type: session.PendingEntryMessage, Message: agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call", ToolName: "tool", Content: []ai.ToolResultMessageContent{}, IsError: false, Timestamp: 1}}})}...)
		writes, err := OperationCleanupWrites(context.Background(), opened, "tools", state)
		require.NoError(t, err)
		require.Contains(t, writeAddresses(writes), "value:delete:pi.pending.entry:staged")
		require.NotContains(t, writeAddresses(writes), "value:delete:pi.pending.entry:placed")
	})
	// packages/agent/test/harness/runtime/drive-terminal.test.ts:212-243 (both table rows)
	for _, tc := range []struct {
		name  string
		state session.OperationState
	}{
		{"compaction", session.OperationState{OperationScope: terminalScope(), At: session.AtSummaryDeciding, Task: session.SummaryTask{TaskID: "task", Reason: "manual", Boundary: session.ResultBoundary{Kind: session.BoundaryFinish}}}},
		{"navigation", session.OperationState{OperationScope: terminalScope(), At: session.AtNavigationReadyToCommit, TargetID: nil}},
	} {
		t.Run(fmt.Sprintf("defensively deletes leftover %s operation families", tc.name), func(t *testing.T) {
			opened := harnessSession(t, t.Name(), nil)
			seedOperationLeftovers(t, opened, tc.name, tc.state)
			writes, err := OperationCleanupWrites(context.Background(), opened, tc.name, tc.state)
			require.NoError(t, err)
			require.Equal(t, []string{"value:delete:pi.op.meta:" + tc.name, "value:delete:pi.op.state:" + tc.name, "value:delete:pi.op.tool_args:" + tc.name + ":step:0", "value:delete:pi.op.tool_memo:" + tc.name + ":invocation:memo", "value:delete:pi.op.preparation:" + tc.name + ":task", "value:delete:pi.pending.tool_output:" + tc.name + ":invocation"}, writeAddresses(writes))
		})
	}
}

func TestPortWave04RuntimeOperationResultRecords(t *testing.T) {
	// packages/agent/test/harness/runtime/drive-terminal.test.ts:247-272
	t.Run("constructs one flat immutable observation from terminal metadata", func(t *testing.T) {
		original := runtimeNow
		runtimeNow = func() int64 { return 20 }
		t.Cleanup(func() { runtimeNow = original })
		failure := &session.OperationError{Code: "provider", Message: "failed"}
		record, err := OperationResultRecord(session.OperationMeta{OperationID: "run", Lane: "main", SourceTipID: new("source"), StartedAt: 10, Intent: session.OperationIntent{Kind: session.OperationKindRun, PromptEntryIDs: []string{"prompt"}}}, session.TerminalFailed, new("tip"), failure)
		require.NoError(t, err)
		require.Equal(t, session.OperationResultRecord{OperationID: "run", Kind: "run", Status: "failed", Error: failure, FromTipID: new("source"), TipID: new("tip"), StartedAt: 10, EndedAt: 20}, record)
	})
	// packages/agent/test/harness/runtime/drive-terminal.test.ts:274-289
	t.Run("rejects errors on non-failed records and missing errors on failed records", func(t *testing.T) {
		meta := session.OperationMeta{OperationID: "run", Lane: "main", SourceTipID: nil, StartedAt: 1, Intent: session.OperationIntent{Kind: session.OperationKindRun, PromptEntryIDs: []string{}}}
		_, err := OperationResultRecord(meta, session.TerminalCompleted, new("tip"), &session.OperationError{Code: "x", Message: "x"})
		require.EqualError(t, err, "Only a failed operation result may carry an error")
		_, err = OperationResultRecord(meta, session.TerminalFailed, new("tip"), nil)
		require.EqualError(t, err, "Only a failed operation result may carry an error")
	})
}
