package runtime

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave04RuntimeStructuralDrive(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:289
	t.Run("routes a declined threshold directly to assistant generation", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		checkpoint := session.OperationState{OperationScope: driveTestScope(harness.CompactionSettings{Enabled: true, ReserveTokens: f.model.Capabilities.ContextWindow, KeepRecentTokens: 1}), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "assistant"}
		f.install(t, checkpoint, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"user"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("user", nil, "question"), {ID: "assistant", ParentID: new("user"), Type: session.EntryTypeMessage, Message: driveTestAssistant("answer")}}})
		r, err := RunCheckpoint(context.Background(), f.lane, f.drive, checkpoint)
		driveTestContinue(t, r, err)
		deciding := f.state(t)
		require.Equal(t, session.AtSummaryDeciding, deciding.At)
		require.Equal(t, session.BoundaryResumeCheckpoint, deciding.Task.Boundary.Kind)
		require.NotNil(t, driveTestValue(t, f, session.OperationPreparation(driveProcedureOperationID, deciding.Task.TaskID)))
		_, err = f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			return &agentharness.BeforeCompactionResult{Decline: true}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err = RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestContinue(t, r, err)
		ready := f.state(t)
		require.Equal(t, session.AtAssistantReady, ready.At)
		require.Equal(t, "assistant", ready.GenerationContext.TriggerEntryID)
		require.False(t, ready.GenerationContext.OverflowRecoveryUsed)
		require.Len(t, driveTestEventsOf(f, agentharness.EventCompactionStart), 1)
		require.Len(t, driveTestEventsOf(f, agentharness.EventCompactionEnd), 1)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:338
	t.Run("uses a newer compaction entry as the durable threshold guard", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		checkpoint := session.OperationState{OperationScope: driveTestScope(harness.CompactionSettings{Enabled: true, ReserveTokens: f.model.Capabilities.ContextWindow, KeepRecentTokens: 1}), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "trigger"}
		f.install(t, checkpoint, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"trigger"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("trigger", nil, "history"), {ID: "compacted", ParentID: new("trigger"), Type: session.EntryTypeCompaction, Summary: "already compacted", RetainedTail: []agent.AgentMessage{}, TokensBefore: f.model.Capabilities.ContextWindow, FromHook: false}}})
		r, err := RunCheckpoint(context.Background(), f.lane, f.drive, checkpoint)
		driveTestContinue(t, r, err)
		ready := f.state(t)
		require.Equal(t, session.AtAssistantReady, ready.At)
		require.Equal(t, "trigger", ready.GenerationContext.TriggerEntryID)
		require.NotContains(t, f.eventTypes(), agentharness.EventCompactionStart)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:374
	t.Run("rejects a missing threshold trigger when no newer compaction guards it", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		checkpoint := session.OperationState{OperationScope: driveTestScope(harness.CompactionSettings{Enabled: true, ReserveTokens: f.model.Capabilities.ContextWindow, KeepRecentTokens: 1}), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "missing-trigger"}
		f.install(t, checkpoint, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}})
		_, err := RunCheckpoint(context.Background(), f.lane, f.drive, checkpoint)
		require.EqualError(t, err, "Checkpoint trigger missing-trigger is missing from its Branch")
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:397
	t.Run("finishes a may-finish run directly after threshold decline", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestRunTask("threshold", session.Continuation{Kind: session.ContinuationMayFinish, IncludeFinalAssistant: false}, "tip")}
		prep := driveTestCompactionPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		finishHooks := 0
		_, err := f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			return &agentharness.BeforeCompactionResult{Decline: true}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		_, err = f.hooks.OnBeforeRunEnd(func(harness.Context, agentharness.BeforeRunEndEvent) (*agentharness.BeforeRunEndResult, error) {
			finishHooks++
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestSettled(t, r, err, "run", "completed")
		require.Equal(t, new("tip"), r.Record.TipID)
		require.Len(t, f.storage.GetCommitAttempts(), 1)
		require.Equal(t, 1, finishHooks)
		require.Nil(t, f.lane.SnapshotState().Operation)
		events := f.events()
		require.GreaterOrEqual(t, len(events), 2)
		end, ok := events[len(events)-2].Payload.(agentharness.CompactionEndPayload)
		require.True(t, ok)
		require.Equal(t, "threshold", end.Reason)
		require.Equal(t, "declined", end.Status)
		runEnd, ok := events[len(events)-1].Payload.(agentharness.RunEndPayload)
		require.True(t, ok)
		require.Equal(t, "completed", runEnd.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:436
	t.Run("routes queued follow-up before before_run_end after threshold decline", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestRunTask("threshold", session.Continuation{Kind: session.ContinuationMayFinish}, "tip")}
		prep := driveTestCompactionPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		f.queue(t, driveTestQueuedMessage("follow-up", "followUp", "continue"))
		f.storage.ClearCommitAttempts()
		finishHooks := 0
		_, err := f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			return &agentharness.BeforeCompactionResult{Decline: true}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		_, err = f.hooks.OnBeforeRunEnd(func(harness.Context, agentharness.BeforeRunEndEvent) (*agentharness.BeforeRunEndResult, error) {
			finishHooks++
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestContinue(t, r, err)
		require.Len(t, f.storage.GetCommitAttempts(), 1)
		require.Zero(t, finishHooks)
		ready := f.state(t)
		require.Equal(t, session.AtAssistantReady, ready.At)
		require.Equal(t, "follow-up", ready.GenerationContext.TriggerEntryID)
		require.Contains(t, f.eventTypes(), agentharness.EventCompactionEnd)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:491 (both rows)
	for _, queue := range []string{"steer", "followUp"} {
		t.Run("continues to an assistant turn when "+queue+" arrives during in-run compaction", func(t *testing.T) {
			f := newDriveProcedureFixture(t, true)
			deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestRunTask("threshold", session.Continuation{Kind: session.ContinuationMayFinish, IncludeFinalAssistant: true}, "tip")}
			prep := driveTestCompactionPreparation()
			f.install(t, deciding, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
			started := make(chan struct{})
			release := make(chan struct{})
			releaseHook := sync.OnceFunc(func() { close(release) })
			_, err := f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
				close(started)
				<-release
				return &agentharness.BeforeCompactionResult{Compaction: &compaction.CompactResult{Summary: "hook summary", TokensBefore: 1000, RetainedTail: []agent.AgentMessage{laneUser("tail", 1)}}}, nil
			}, agentharness.HookOptions{})
			require.NoError(t, err)
			running := driveTestAsync(t, releaseHook, func() (ProcedureResult, error) {
				return RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
			})
			<-started
			f.queue(t, driveTestQueuedMessage("queued", queue, queue+" during compaction"))
			releaseHook()
			result := <-running
			driveTestContinue(t, result.result, result.err)
			routed := f.state(t)
			if queue == "steer" {
				require.Equal(t, session.AtAssistantReady, routed.At)
				require.Equal(t, "queued", routed.GenerationContext.TriggerEntryID)
				require.False(t, routed.GenerationContext.OverflowRecoveryUsed)
				require.Equal(t, []session.InboxItem{}, f.lane.SnapshotState().Inbox)
				require.Nil(t, driveTestValue(t, f, session.PendingEntryValue("queued")))
				entry := driveTestEntryAt(t, f, "queued")
				require.NotNil(t, entry.ParentID)
				require.Equal(t, session.EntryTypeMessage, entry.Type)
				require.Equal(t, session.EntryTypeCompaction, driveTestEntryAt(t, f, *entry.ParentID).Type)
			} else {
				require.Equal(t, session.AtCheckpoint, routed.At)
				require.Equal(t, []session.InboxItem{{EntryID: "queued", Kind: "followUp"}}, f.lane.SnapshotState().Inbox)
				r, err := RunCheckpoint(context.Background(), f.lane, f.drive, routed)
				driveTestContinue(t, r, err)
				ready := f.state(t)
				require.Equal(t, session.AtAssistantReady, ready.At)
				require.Equal(t, "queued", ready.GenerationContext.TriggerEntryID)
				require.False(t, ready.GenerationContext.OverflowRecoveryUsed)
				require.Equal(t, []session.InboxItem{}, f.lane.SnapshotState().Inbox)
			}
		})
	}
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:580
	t.Run("publishes structural output and mixed write/steer input in one admission-ordered commit", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		scope := driveTestScope()
		scope.Settings.SteeringMode = agent.QueueModeOneAtATime
		deciding := session.OperationState{OperationScope: scope, At: session.AtSummaryDeciding, Task: driveTestRunTask("threshold", session.Continuation{Kind: session.ContinuationNeedAssistant}, "tip")}
		prep := driveTestCompactionPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		one := session.JsonValue(map[string]any{"order": 1})
		two := session.JsonValue(map[string]any{"order": 2})
		f.queue(t, driveTestQueuedEntry{id: "write-1", kind: "write", pending: session.PendingEntry{Type: session.PendingEntryCustom, CustomType: "note", CustomPayload: &one}}, driveTestQueuedMessage("steer", "steer", "steer"), driveTestQueuedEntry{id: "write-2", kind: "write", pending: session.PendingEntry{Type: session.PendingEntryCustom, CustomType: "note", CustomPayload: &two}}, driveTestQueuedMessage("steer-2", "steer", "next steer"))
		f.storage.ClearCommitAttempts()
		_, err := f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			return &agentharness.BeforeCompactionResult{Compaction: &compaction.CompactResult{Summary: "summary", TokensBefore: 1000, RetainedTail: []agent.AgentMessage{laneUser("tail", 1)}}}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestContinue(t, r, err)
		require.Len(t, f.storage.GetCommitAttempts(), 1)
		require.Equal(t, []session.InboxItem{{EntryID: "steer-2", Kind: "steer"}}, f.lane.SnapshotState().Inbox)
		require.NotNil(t, driveTestValue(t, f, session.PendingEntryValue("steer-2")))
		ready := f.state(t)
		require.Equal(t, session.AtAssistantReady, ready.At)
		require.Equal(t, "steer", ready.GenerationContext.TriggerEntryID)
		require.NotNil(t, driveTestEntryAt(t, f, "write-1").ParentID)
		require.Equal(t, new("write-1"), driveTestEntryAt(t, f, "steer").ParentID)
		require.Equal(t, new("steer"), driveTestEntryAt(t, f, "write-2").ParentID)
		require.Equal(t, new("write-2"), f.lane.SnapshotState().TipID)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:659
	t.Run("queues writes during standalone structural work without changing the operation", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestCompactionTask()}
		prep := driveTestCompactionPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		payload := session.JsonValue(map[string]any{"pending": true})
		id, err := f.lane.AppendCustomEntry(context.Background(), "note", &payload)
		require.NoError(t, err)
		require.Equal(t, deciding, f.state(t))
		require.Equal(t, new("tip"), f.lane.SnapshotState().TipID)
		inbox := []session.InboxItem{{EntryID: id, Kind: "write"}}
		require.Equal(t, inbox, f.lane.SnapshotState().Inbox)
		stored := driveTestValue(t, f, session.LaneStateValue("main"))
		require.NotNil(t, stored)
		require.Equal(t, session.LaneState{CurrentOperationID: new(driveProcedureOperationID), LastOperationID: nil, Inbox: inbox}, stored.Value)
		require.NotNil(t, driveTestValue(t, f, session.PendingEntryValue(id)))
		events := f.events()
		require.NotEmpty(t, events)
		event, ok := events[len(events)-1].Payload.(agentharness.QueueUpdatePayload)
		require.True(t, ok)
		require.Len(t, event.Queues, 1)
		require.Equal(t, id, event.Queues[0].EntryID)
		require.Equal(t, "write", event.Queues[0].Kind)
		require.Equal(t, "custom", event.Queues[0].Type)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:693
	t.Run("publishes overflow preparation with the normalized response settlement", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		ready := session.OperationState{OperationScope: driveTestScope(harness.CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 1}), At: session.AtAssistantReady, GenerationContext: session.GenerationContext{StepID: "step", TriggerEntryID: "tip", Configuration: f.configuration, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 10, MaxAgentDelayMs: 30000}}, NextAttempt: 1}
		f.install(t, ready, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "large prompt")}})
		f.setResponses([]ai.FauxResponseStep{driveTestError("prompt exceeds the context window")})
		r, err := RunGeneration(context.Background(), f.lane, f.drive, ready)
		driveTestContinue(t, r, err)
		deciding := f.state(t)
		require.Equal(t, session.AtSummaryDeciding, deciding.At)
		require.Equal(t, "overflow", deciding.Task.Reason)
		require.Equal(t, session.BoundaryResumeCheckpoint, deciding.Task.Boundary.Kind)
		require.Equal(t, &session.CheckpointData{Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant, OverflowRecoveryUsed: true}, TriggerEntryID: "tip"}, deciding.Task.Boundary.ResumeAfter)
		require.NotNil(t, driveTestValue(t, f, session.OperationPreparation(driveProcedureOperationID, deciding.Task.TaskID)))
		tip := f.lane.SnapshotState().TipID
		require.NotNil(t, tip)
		response := driveTestEntryAt(t, f, *tip)
		require.Equal(t, session.EntryTypeMessage, response.Type)
		require.NotNil(t, response.Message.Assistant)
		require.Equal(t, ai.StopReasonError, response.Message.Assistant.StopReason)
		require.Contains(t, f.eventTypes(), agentharness.EventCompactionStart)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:743
	t.Run("publishes a hook compaction and terminal cleanup atomically without assistant lifecycle", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		task := driveTestCompactionTask()
		task.Reason = ""
		task.CustomInstructions = new("focus")
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: task}
		prep := driveTestCompactionPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "compaction", CustomInstructions: new("focus")}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		payload := session.JsonValue(map[string]any{"after": "compaction"})
		queued, err := f.lane.AppendCustomEntry(context.Background(), "retained", &payload)
		require.NoError(t, err)
		_, err = f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			return &agentharness.BeforeCompactionResult{Compaction: &compaction.CompactResult{Summary: "hook summary", TokensBefore: 1000, RetainedTail: []agent.AgentMessage{laneUser("tail", 1)}, Details: map[string]any{"source": "hook"}}}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestSettled(t, r, err, "compaction", "completed")
		require.Nil(t, f.lane.SnapshotState().Operation)
		require.Equal(t, []session.InboxItem{{EntryID: queued, Kind: "write"}}, f.lane.SnapshotState().Inbox)
		require.NotNil(t, driveTestValue(t, f, session.PendingEntryValue(queued)))
		tip := f.lane.SnapshotState().TipID
		require.NotNil(t, tip)
		entry := driveTestEntryAt(t, f, *tip)
		require.Equal(t, session.EntryTypeCompaction, entry.Type)
		require.Equal(t, "hook summary", entry.Summary)
		require.True(t, entry.FromHook)
		require.Greater(t, entry.Seq, int64(0))
		require.Equal(t, int64(100), entry.Timestamp)
		require.Nil(t, driveTestValue(t, f, session.OperationMetaValue(driveProcedureOperationID)))
		require.Nil(t, driveTestValue(t, f, session.OperationPreparation(driveProcedureOperationID, "task")))
		require.NotContains(t, f.eventTypes(), agentharness.EventMessageStart)
		require.NotContains(t, f.eventTypes(), agentharness.EventMessageEnd)
		require.Contains(t, f.eventTypes(), agentharness.EventEntryAdded)
		events := f.events()
		last, ok := events[len(events)-1].Payload.(agentharness.CompactionEndPayload)
		require.True(t, ok)
		require.Equal(t, "manual", last.Reason)
		require.Equal(t, "completed", last.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:798
	t.Run("terminal-declines standalone compaction without publishing an entry", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestCompactionTask()}
		prep := driveTestCompactionPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		_, err := f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			return &agentharness.BeforeCompactionResult{Decline: true}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestSettled(t, r, err, "compaction", "declined")
		require.Equal(t, new("tip"), r.Record.TipID)
		require.Nil(t, f.lane.SnapshotState().Operation)
		require.Equal(t, new("tip"), f.lane.SnapshotState().TipID)
		events := f.events()
		end, ok := events[len(events)-1].Payload.(agentharness.CompactionEndPayload)
		require.True(t, ok)
		require.Equal(t, "declined", end.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:821
	t.Run("terminal-fails overflow decline while preserving lane-owned input", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestRunTask("overflow", session.Continuation{Kind: session.ContinuationNeedAssistant, OverflowRecoveryUsed: true}, "tip")}
		prep := driveTestCompactionPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		payload := session.JsonValue(map[string]any{"value": true})
		queued, err := f.lane.AppendCustomEntry(context.Background(), "retained", &payload)
		require.NoError(t, err)
		_, err = f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			return &agentharness.BeforeCompactionResult{Decline: true}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestSettled(t, r, err, "run", "failed")
		require.NotNil(t, r.Record.Error)
		require.Equal(t, "compaction_declined", r.Record.Error.Code)
		require.Equal(t, []session.InboxItem{{EntryID: queued, Kind: "write"}}, f.lane.SnapshotState().Inbox)
		require.NotNil(t, driveTestValue(t, f, session.PendingEntryValue(queued)))
		events := f.events()
		require.GreaterOrEqual(t, len(events), 2)
		end, ok := events[len(events)-2].Payload.(agentharness.CompactionEndPayload)
		require.True(t, ok)
		require.Equal(t, "declined", end.Status)
		runEnd, ok := events[len(events)-1].Payload.(agentharness.RunEndPayload)
		require.True(t, ok)
		require.Equal(t, "failed", runEnd.Status)
	})
}

func TestPortWave04RuntimeStructuralGeneration(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:855
	t.Run("gives each split-turn provider request its own durable intent and usage row", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		ready := driveTestSummaryReady(driveTestScope(), driveTestCompactionTask(), f.configuration)
		prep := driveTestCompactionPreparation()
		prep.MessagesToSummarize = []agent.AgentMessage{laneUser("old history", 1)}
		prep.TurnPrefixMessages = []agent.AgentMessage{laneUser("large turn", 1)}
		prep.IsSplitTurn = true
		f.install(t, ready, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		f.setResponses([]ai.FauxResponseStep{driveTestResponse("history summary"), driveTestResponse("turn prefix summary")})
		r, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		driveTestSettled(t, r, err, "compaction", "completed")
		indices := []int{}
		usageWrites := 0
		frames := false
		for _, batch := range f.storage.GetCommitAttempts() {
			for _, write := range batch {
				switch write := write.(type) {
				case session.ValueSetWrite:
					if write.Namespace == "pi.op.state" {
						state, ok := write.Value.(session.OperationState)
						require.True(t, ok)
						if state.Request != nil {
							indices = append(indices, state.Request.Index)
						}
					}
				case session.UsageWrite:
					usageWrites++
				case session.ListAppendWrite:
					frames = frames || write.Namespace == "pi.pending.assistant_frame"
				case session.ListDeleteWrite:
					frames = frames || write.Namespace == "pi.pending.assistant_frame"
				}
			}
		}
		require.Equal(t, []int{0, 1}, indices)
		require.Equal(t, 2, usageWrites)
		require.False(t, frames)
		for _, kind := range []agentharness.HarnessEventType{agentharness.EventMessageStart, agentharness.EventMessageUpdate, agentharness.EventMessageEnd} {
			require.NotContains(t, f.eventTypes(), kind)
		}
		require.Len(t, driveTestEventsOf(f, agentharness.EventUsage), 2)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:909
	t.Run("preserves the overflow recovery bound when compaction resumes generation", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		resumeAfter := session.OperationState{OperationScope: driveTestScope(), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "tip"}
		ready := driveTestSummaryReady(driveTestScope(), driveTestRunTask("overflow", session.Continuation{Kind: session.ContinuationNeedAssistant, OverflowRecoveryUsed: true}, resumeAfter.TriggerEntryID), f.configuration)
		prep := driveTestCompactionPreparation()
		f.install(t, ready, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		f.setResponses([]ai.FauxResponseStep{driveTestResponse("generated summary")})
		r, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		driveTestContinue(t, r, err)
		routed := f.state(t)
		require.Equal(t, session.AtAssistantReady, routed.At)
		require.Equal(t, "tip", routed.GenerationContext.TriggerEntryID)
		require.True(t, routed.GenerationContext.OverflowRecoveryUsed)
		require.Equal(t, new("summary-entry"), f.lane.SnapshotState().TipID)
		entry := driveTestEntryAt(t, f, "summary-entry")
		require.Equal(t, session.EntryTypeCompaction, entry.Type)
		require.Equal(t, "generated summary", entry.Summary)
		require.False(t, entry.FromHook)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:948
	t.Run("settles structural usage without faulting when durable cancellation aborts the request", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		ready := driveTestSummaryReady(driveTestScope(), driveTestCompactionTask(), f.configuration)
		prep := driveTestCompactionPreparation()
		f.install(t, ready, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		started := make(chan struct{})
		release := make(chan struct{})
		releaseRequest := sync.OnceFunc(func() { close(release) })
		f.setResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
			close(started)
			<-release
			response := ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("")}, StopReason: "stop"}
			if options.Signal != nil && options.Signal.Err() != nil {
				response.StopReason = "aborted"
				response.ErrorMessage = "cancelled"
			}
			return response, nil
		})})
		running := driveTestAsync(t, releaseRequest, func() (ProcedureResult, error) {
			return RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		})
		<-started
		cancelled := make(chan struct{})
		f.drive.BeginAbort(func() error { <-cancelled; return nil })
		f.cancel(t)
		close(cancelled)
		f.drive.SignalAbort()
		releaseRequest()
		result := <-running
		driveTestContinue(t, result.result, result.err)
		state := f.state(t)
		require.Equal(t, session.AtSummaryEffectPending, state.At)
		require.Equal(t, session.ControlCancelRequested, state.Control.Status)
		require.Nil(t, state.Request)
		require.Len(t, state.UsageIDs, 1)
		usage := 0
		for _, batch := range f.storage.GetCommitAttempts() {
			for _, write := range batch {
				if _, ok := write.(session.UsageWrite); ok {
					usage++
				}
			}
		}
		require.Equal(t, 1, usage)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:998 (both in-run and standalone fixtures)
	t.Run("fails missing in-run structural models with configuration provenance", func(t *testing.T) {
		for _, kind := range []string{"run", "compaction"} {
			f := newDriveProcedureFixture(t, true)
			configuration := f.configuration
			configuration.Model = session.ModelRef{Provider: "missing", ModelID: "missing"}
			task := driveTestCompactionTask()
			tip := "standalone-tip"
			intent := session.OperationIntent{Kind: "compaction"}
			if kind == "run" {
				task = driveTestRunTask("threshold", session.Continuation{Kind: session.ContinuationNeedAssistant}, "tip")
				tip = "tip"
				intent = session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}
			}
			ready := driveTestSummaryReady(driveTestScope(), task, configuration)
			prep := driveTestCompactionPreparation()
			f.install(t, ready, intent, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry(tip, nil, "history")}, Preparation: &prep})
			r, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
			driveTestSettled(t, r, err, kind, "failed")
			require.NotNil(t, r.Record.Error)
			require.Equal(t, "model_unavailable", r.Record.Error.Code)
			require.Nil(t, f.lane.SnapshotState().Operation)
			if kind == "run" {
				for _, batch := range f.storage.GetCommitAttempts() {
					for _, write := range batch {
						_, usage := write.(session.UsageWrite)
						require.False(t, usage)
					}
				}
			}
		}
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1051
	t.Run("durably brackets a delayed structural retry through success", func(t *testing.T) {
		now := driveTestFreezeTime(t, 1000)
		f := newDriveProcedureFixture(t, true)
		ready := driveTestSummaryReady(driveTestScope(), driveTestCompactionTask(), f.configuration)
		prep := driveTestCompactionPreparation()
		f.install(t, ready, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		f.setResponses([]ai.FauxResponseStep{driveTestError("rate limit exceeded")})
		r, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		driveTestContinue(t, r, err)
		retry := f.state(t)
		require.Equal(t, session.AtSummaryRetryWait, retry.At)
		require.Equal(t, 2, retry.NextAttempt)
		r, err = RunStructuralRetryWait(context.Background(), f.lane, f.drive, retry)
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureWaiting, Outcome: agentharness.DriveOutcome{Kind: agentharness.DriveWaiting, OperationID: driveProcedureOperationID, Reason: agentharness.DriveWaitRetry, NotBefore: retry.NotBefore}}, r)
		*now = retry.NotBefore
		r, err = RunStructuralRetryWait(context.Background(), f.lane, f.drive, retry)
		driveTestContinue(t, r, err)
		second := f.state(t)
		require.Equal(t, session.AtSummaryReady, second.At)
		f.setResponses([]ai.FauxResponseStep{driveTestResponse("summary")})
		r, err = RunStructuralGeneration(context.Background(), f.lane, f.drive, second)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, r.Kind)
		require.Equal(t, "completed", r.Record.Status)
		types := []agentharness.HarnessEventType{}
		var success []bool
		for _, event := range f.events() {
			switch event.Type() {
			case agentharness.EventRetryScheduled, agentharness.EventRetryStart, agentharness.EventRetryEnd:
				types = append(types, event.Type())
				if end, ok := event.Payload.(agentharness.RetryEndPayload); ok {
					success = append(success, end.Success)
				}
			}
		}
		require.Equal(t, []agentharness.HarnessEventType{agentharness.EventRetryScheduled, agentharness.EventRetryStart, agentharness.EventRetryEnd}, types)
		require.Equal(t, []bool{true}, success)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1102
	t.Run("closes an exhausted structural retry with its final error", func(t *testing.T) {
		now := driveTestFreezeTime(t, 2000)
		f := newDriveProcedureFixture(t, true)
		ready := driveTestSummaryReady(driveTestScope(), driveTestCompactionTask(), f.configuration)
		prep := driveTestCompactionPreparation()
		f.install(t, ready, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		f.setResponses([]ai.FauxResponseStep{driveTestError("rate limit exceeded")})
		_, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		require.NoError(t, err)
		retry := f.state(t)
		require.Equal(t, session.AtSummaryRetryWait, retry.At)
		*now = retry.NotBefore
		_, err = RunStructuralRetryWait(context.Background(), f.lane, f.drive, retry)
		require.NoError(t, err)
		second := f.state(t)
		require.Equal(t, session.AtSummaryReady, second.At)
		f.setResponses([]ai.FauxResponseStep{driveTestError("rate limit exceeded")})
		r, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, second)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, r.Kind)
		require.Equal(t, "failed", r.Record.Status)
		events := driveTestEventsOf(f, agentharness.EventRetryEnd)
		require.Len(t, events, 1)
		end, ok := events[0].Payload.(agentharness.RetryEndPayload)
		require.True(t, ok)
		require.Equal(t, 2, end.Attempt)
		require.False(t, end.Success)
		require.NotNil(t, end.FinalError)
		require.Contains(t, *end.FinalError, "rate limit exceeded")
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1143
	t.Run("finishes a standalone structural failure at the retry cap", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		ready := driveTestSummaryReady(driveTestScope(), driveTestCompactionTask(), f.configuration)
		ready.SummaryContext.RetryPolicy.MaxAttempts = 1
		prep := driveTestCompactionPreparation()
		f.install(t, ready, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		f.setResponses([]ai.FauxResponseStep{driveTestError("rate limit exceeded")})
		r, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		driveTestSettled(t, r, err, "compaction", "failed")
		require.NotNil(t, r.Record.Error)
		require.Equal(t, "summarization_failed", r.Record.Error.Code)
		require.Nil(t, f.lane.SnapshotState().Operation)
		require.NotContains(t, f.eventTypes(), agentharness.EventRetryScheduled)
	})
}

func TestPortWave04RuntimeStructuralNavigationAndRecovery(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1172 (all three dynamic cases)
	for _, invalid := range []string{"missing", "source", "root_label"} {
		t.Run("rejects invalid unsummarized navigation state without committing/"+invalid, func(t *testing.T) {
			f := newDriveProcedureFixture(t, true)
			target := new(invalid)
			var label *string
			if invalid == "root_label" {
				target = nil
				label = new("invalid")
			}
			navigation := session.OperationState{OperationScope: driveTestScope(), At: session.AtNavigationReadyToCommit, TargetID: target, Label: label}
			f.install(t, navigation, session.OperationIntent{Kind: "navigation", TargetID: target, Summarize: false, Label: label}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("source", nil, "source")}, TipID: new("source")})
			_, err := CommitNavigation(context.Background(), f.lane, f.drive, navigation)
			require.Error(t, err)
			require.Empty(t, f.storage.GetCommitAttempts())
			require.Equal(t, new("source"), f.lane.SnapshotState().TipID)
		})
	}
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1203
	t.Run("moves an unsummarized navigation and cleans up in one terminal transaction", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		navigation := session.OperationState{OperationScope: driveTestScope(), At: session.AtNavigationReadyToCommit, TargetID: new("target"), Label: new("chosen")}
		f.install(t, navigation, session.OperationIntent{Kind: "navigation", TargetID: new("target"), Summarize: false, Label: new("chosen")}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("root", nil, "root"), driveTestEntry("source", new("root"), "source"), driveTestEntry("target", new("root"), "target")}, TipID: new("source")})
		r, err := CommitNavigation(context.Background(), f.lane, f.drive, navigation)
		driveTestSettled(t, r, err, "navigation", "completed")
		require.Equal(t, new("source"), r.Record.FromTipID)
		require.Equal(t, new("target"), r.Record.TipID)
		require.Equal(t, new("target"), f.lane.SnapshotState().TipID)
		label, err := f.session.GetLabel(context.Background(), "target")
		require.NoError(t, err)
		require.Equal(t, new("chosen"), label)
		attempts := f.storage.GetCommitAttempts()
		require.NotEmpty(t, attempts)
		writes := attempts[len(attempts)-1]
		require.True(t, slices.ContainsFunc(writes, func(write session.Write) bool {
			value, ok := write.(session.ValueDeleteWrite)
			return ok && value.Namespace == "pi.op.state"
		}))
		for _, namespace := range []string{"pi.result", "pi.lane.state"} {
			require.True(t, slices.ContainsFunc(writes, func(write session.Write) bool {
				value, ok := write.(session.ValueSetWrite)
				return ok && value.Namespace == namespace
			}))
		}
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1245
	t.Run("publishes a hook navigation summary with the target parent and source identity", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestNavigationTask("target")}
		prep := driveTestBranchPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "navigation", TargetID: new("target"), Summarize: true}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("root", nil, "root"), driveTestEntry("source", new("root"), "source"), driveTestEntry("target", new("root"), "target")}, TipID: new("source"), Preparation: &prep})
		_, err := f.hooks.OnBeforeNavigation(func(harness.Context, agentharness.BeforeNavigationEvent) (*agentharness.BeforeNavigationResult, error) {
			return &agentharness.BeforeNavigationResult{Summary: &compaction.BranchSummaryResult{Summary: "branch summary", ReadFiles: []string{"read.ts"}, ModifiedFiles: []string{"edit.ts"}}}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestSettled(t, r, err, "navigation", "completed")
		tip := f.lane.SnapshotState().TipID
		require.NotNil(t, tip)
		entry := driveTestEntryAt(t, f, *tip)
		require.Equal(t, session.EntryTypeBranchSummary, entry.Type)
		require.Equal(t, new("target"), entry.ParentID)
		require.Equal(t, new("source"), entry.FromID)
		require.Equal(t, "branch summary", entry.Summary)
		require.True(t, entry.FromHook)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1285
	t.Run("terminal-declines summarized navigation without moving the tip", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestNavigationTask("target")}
		prep := driveTestBranchPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "navigation", TargetID: new("target"), Summarize: true}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("root", nil, "root"), driveTestEntry("source", new("root"), "source"), driveTestEntry("target", new("root"), "target")}, TipID: new("source"), Preparation: &prep})
		_, err := f.hooks.OnBeforeNavigation(func(harness.Context, agentharness.BeforeNavigationEvent) (*agentharness.BeforeNavigationResult, error) {
			return &agentharness.BeforeNavigationResult{Decline: true}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		r, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		driveTestSettled(t, r, err, "navigation", "declined")
		require.Equal(t, new("source"), r.Record.TipID)
		require.Equal(t, new("source"), f.lane.SnapshotState().TipID)
		events := f.events()
		end, ok := events[len(events)-1].Payload.(agentharness.NavigationEndPayload)
		require.True(t, ok)
		require.Equal(t, "declined", end.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1312
	t.Run("generates and atomically publishes a navigation summary", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		ready := driveTestSummaryReady(driveTestScope(), driveTestNavigationTask("target"), f.configuration)
		prep := driveTestBranchPreparation()
		f.install(t, ready, session.OperationIntent{Kind: "navigation", TargetID: new("target"), Summarize: true}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("root", nil, "root"), driveTestEntry("source", new("root"), "source"), driveTestEntry("target", new("root"), "target")}, TipID: new("source"), Preparation: &prep})
		f.setResponses([]ai.FauxResponseStep{driveTestResponse("generated branch summary")})
		r, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		driveTestSettled(t, r, err, "navigation", "completed")
		require.Equal(t, new("source"), r.Record.FromTipID)
		require.Equal(t, new("summary-entry"), r.Record.TipID)
		entry := driveTestEntryAt(t, f, "summary-entry")
		require.Equal(t, session.EntryTypeBranchSummary, entry.Type)
		require.Equal(t, new("target"), entry.ParentID)
		require.Equal(t, new("source"), entry.FromID)
		require.False(t, entry.FromHook)
		require.Len(t, driveTestEventsOf(f, agentharness.EventUsage), 1)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1352
	t.Run("consumes an orphaned structural attempt and never resumes its nested request", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		effect := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryEffectPending, Task: driveTestCompactionTask(), SummaryContext: session.SummaryContext{ResultEntryID: "summary-entry", Configuration: f.configuration, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 10, MaxAgentDelayMs: 30000}}, Attempt: 1, Request: &session.SummaryRequest{Index: 1, UsageID: "abandoned-usage"}, UsageIDs: []string{"settled-usage"}}
		prep := driveTestCompactionPreparation()
		f.install(t, effect, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		r, err := RecoverStructuralGeneration(context.Background(), f.lane, f.drive, effect)
		driveTestContinue(t, r, err)
		retry := f.state(t)
		require.Equal(t, session.AtSummaryRetryWait, retry.At)
		require.Equal(t, 2, retry.NextAttempt)
		require.Nil(t, retry.Request)
		encoded, err := json.Marshal(retry)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &fields))
		require.NotContains(t, fields, "request")
		for _, batch := range f.storage.GetCommitAttempts() {
			for _, write := range batch {
				_, usage := write.(session.UsageWrite)
				require.False(t, usage)
			}
		}
		events := f.events()
		last := events[len(events)-1]
		scheduled, ok := last.Payload.(agentharness.RetryScheduledPayload)
		require.True(t, ok)
		require.Equal(t, 2, scheduled.Attempt)
		require.True(t, last.Recovery)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1396
	t.Run("terminal-fails an orphaned structural attempt at the retry cap", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		effect := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryEffectPending, Task: driveTestCompactionTask(), SummaryContext: session.SummaryContext{ResultEntryID: "summary-entry", Configuration: f.configuration, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 1, BaseDelayMs: 10, MaxAgentDelayMs: 30000}}, Attempt: 1, Request: &session.SummaryRequest{Index: 0, UsageID: "abandoned-usage"}, UsageIDs: []string{}}
		prep := driveTestCompactionPreparation()
		f.install(t, effect, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		r, err := RecoverStructuralGeneration(context.Background(), f.lane, f.drive, effect)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, r.Kind)
		require.Equal(t, "failed", r.Record.Status)
		require.NotNil(t, r.Record.Error)
		require.Equal(t, "structural_interrupted", r.Record.Error.Code)
		require.Nil(t, f.lane.SnapshotState().Operation)
		require.NotContains(t, f.eventTypes(), agentharness.EventRetryScheduled)
	})
	// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:1433
	t.Run("rejects a preparation whose durable kind contradicts the structural state", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestCompactionTask()}
		prep := driveTestBranchPreparation()
		f.install(t, deciding, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		_, err := RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		require.EqualError(t, err, "Structural task task is missing its compaction preparation")
	})
}

// These guards exercise the Go representation and error boundaries needed by the upstream cases above.
func TestPortWave04StructuralPortBoundaries(t *testing.T) {
	// upstream: packages/agent/src/harness/runtime/drive/structural.ts:67-137.
	t.Run("durable preparations preserve insertion order and an empty present previous summary", func(t *testing.T) {
		preparation := driveTestCompactionPreparation()
		preparation.FileOps = session.DurableFileOperations{Read: []string{"z", "a"}, Written: []string{"second", "first"}, Edited: []string{"edit-b", "edit-a"}}
		preparation.PreviousSummary = new("")
		require.Equal(t, preparation, DurableCompactionPreparation(compactionPreparation(preparation)))
		branch := driveTestBranchPreparation()
		branch.FileOps = preparation.FileOps
		require.Equal(t, branch, DurableBranchPreparation(branchPreparation(branch)))
	})
	// upstream: packages/agent/src/harness/runtime/drive/structural.ts:821-836,876-928.
	t.Run("a request rejection remains a rejection even when it uses a summary error type", func(t *testing.T) {
		f := newDriveProcedureFixture(t, true)
		ready := driveTestSummaryReady(driveTestScope(), driveTestCompactionTask(), f.configuration)
		preparation := driveTestCompactionPreparation()
		f.install(t, ready, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &preparation})
		rejection := &harness.CompactionError{Code: harness.CompactionErrorSummarizationFailed, Message: "request rejected"}
		f.drive.CloseGate(rejection)
		_, err := RunStructuralGeneration(context.Background(), f.lane, f.drive, ready)
		require.Same(t, rejection, err)
		require.Equal(t, session.AtSummaryEffectPending, f.state(t).At)
		require.Nil(t, driveTestValue(t, f, session.OperationResult(driveProcedureOperationID)))
		require.NotContains(t, f.eventTypes(), agentharness.EventCompactionEnd)
	})
}
