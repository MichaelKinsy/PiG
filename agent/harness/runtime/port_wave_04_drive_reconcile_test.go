package runtime

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

type reconciliationCase struct {
	state          session.OperationState
	intent         session.OperationIntent
	options        driveTestInstallOptions
	terminalEvents []agentharness.HarnessEventType
}

func cancelledDriveScope() session.OperationScope {
	scope := driveTestScope()
	scope.Control = session.Control{Status: session.ControlCancelRequested, RequestedAt: 10}
	return scope
}

// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:206-243.
func reconciliationDeferred(f *driveProcedureFixture, effectPending bool) (session.OperationState, session.Entry, ai.DeferredHandle) {
	handle := ai.DeferredHandle{Provider: f.model.ProviderMeta.ProviderID, ModelID: f.model.ID, API: f.model.ProviderMeta.API, ID: "deferred-job"}
	message := driveTestAssistant("")
	message.Assistant.Content = []ai.AssistantContentBlock{}
	message.Assistant.StopReason = ai.StopReasonDeferred
	message.Assistant.Deferred = &handle
	entry := session.Entry{ID: "deferred-source", ParentID: nil, Type: session.EntryTypeMessage, Message: message}
	state := session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtDeferredSuspended, StepID: "step", SourceEntryID: entry.ID, Poll: 0, Configuration: f.configuration}
	if effectPending {
		state.At = session.AtDeferredEffectPending
		state.Poll = 1
		state.ResponseEntryID = "deferred-response"
		state.UsageID = "deferred-usage"
	}
	return state, entry, handle
}

// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:245-401 (all thirteen durable leaves).
func reconciliationCases(f *driveProcedureFixture) []reconciliationCase {
	generation := session.GenerationContext{StepID: "step", TriggerEntryID: "tip", Configuration: f.configuration, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 10, MaxAgentDelayMs: 30000}}
	runTask := session.SummaryTask{TaskID: "run-summary", Reason: "threshold", Boundary: session.ResultBoundary{Kind: session.BoundaryResumeCheckpoint, ResumeAfter: &session.CheckpointData{Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "tip"}}}
	compactionTask := session.SummaryTask{TaskID: "compaction", Reason: "manual", Boundary: session.ResultBoundary{Kind: session.BoundaryFinish}}
	navigationTask := session.SummaryTask{TaskID: "navigation", Boundary: session.ResultBoundary{Kind: session.BoundaryCommitNavigation, TargetID: "target"}}
	summary := session.SummaryContext{ResultEntryID: "summary-entry", Configuration: f.configuration, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 10, MaxAgentDelayMs: 30000}}
	suspended, suspendedEntry, _ := reconciliationDeferred(f, false)
	effect, effectEntry, _ := reconciliationDeferred(f, true)
	toolMessage := driveTestAssistant("")
	toolMessage.Assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "tool-call", Name: "tool", Arguments: map[string]any{}}}
	toolMessage.Assistant.StopReason = ai.StopReasonToolUse
	run := session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}
	emptyRun := session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}
	runEnd := []agentharness.HarnessEventType{agentharness.EventRunEnd}
	compactRunEnd := []agentharness.HarnessEventType{agentharness.EventCompactionEnd, agentharness.EventRunEnd}
	cases := []reconciliationCase{
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtStarting}, intent: run, terminalEvents: runEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "tip"}, intent: run, terminalEvents: runEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtAssistantReady, GenerationContext: generation, NextAttempt: 1}, intent: run, terminalEvents: runEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtAssistantEffectPending, GenerationContext: generation, Attempt: 1, ResponseEntryID: "assistant-response", UsageID: "assistant-usage", IntendedOutputLimit: 100, ContextWindow: 1000}, intent: run, options: driveTestInstallOptions{Writes: []session.Write{session.AppendList(session.PendingAssistantFrames(driveProcedureOperationID, "assistant-response"), ai.AssistantMessageFrame(ai.TextDeltaFrame{ContentIndex: 0, Delta: "partial"}))}}, terminalEvents: runEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtAssistantRetryWait, GenerationContext: generation, NextAttempt: 2, NotBefore: runtimeNow() + 100000, ErrorMessage: "retry"}, intent: run, terminalEvents: runEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtTools, Batch: session.ToolBatch{AssistantEntryID: "assistant", Configuration: f.configuration, TurnID: "turn", Calls: []session.ToolCall{{Status: session.ToolCallPlanned, SourceIndex: 0, ResultEntryID: "tool-result"}}}}, intent: emptyRun, options: driveTestInstallOptions{Entries: []session.Entry{{ID: "assistant", ParentID: nil, Type: session.EntryTypeMessage, Message: toolMessage}}}, terminalEvents: runEnd},
		{state: suspended, intent: emptyRun, options: driveTestInstallOptions{Entries: []session.Entry{suspendedEntry}}, terminalEvents: runEnd},
		{state: effect, intent: emptyRun, options: driveTestInstallOptions{Entries: []session.Entry{effectEntry}}, terminalEvents: runEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtSummaryDeciding, Task: runTask}, intent: run, terminalEvents: compactRunEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtSummaryReady, Task: compactionTask, SummaryContext: summary, NextAttempt: 1}, intent: session.OperationIntent{Kind: "compaction"}, terminalEvents: []agentharness.HarnessEventType{agentharness.EventCompactionEnd}},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtSummaryEffectPending, Task: navigationTask, SummaryContext: summary, Attempt: 1, Request: &session.SummaryRequest{Index: 0, UsageID: "usage"}, UsageIDs: []string{}}, intent: session.OperationIntent{Kind: "navigation", TargetID: new("target"), Summarize: true}, terminalEvents: []agentharness.HarnessEventType{agentharness.EventNavigationEnd}},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtSummaryRetryWait, Task: runTask, SummaryContext: summary, NextAttempt: 2, NotBefore: runtimeNow() + 100000, ErrorMessage: "retry"}, intent: run, terminalEvents: compactRunEnd},
		{state: session.OperationState{OperationScope: cancelledDriveScope(), At: session.AtNavigationReadyToCommit, TargetID: new("target")}, intent: session.OperationIntent{Kind: "navigation", TargetID: new("target"), Summarize: false}, terminalEvents: []agentharness.HarnessEventType{agentharness.EventNavigationEnd}},
	}
	for i := range cases {
		if cases[i].options.Entries == nil {
			cases[i].options.Entries = []session.Entry{driveTestEntry("tip", nil, "history")}
		}
	}
	return cases
}

func TestPortWave04RuntimeTotalDrive(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:411
	t.Run("drives an ordinary run through all direct procedures with one before_drive hook", func(t *testing.T) {
		f := newDriveProcedureFixture(t, false)
		f.install(t, session.OperationState{OperationScope: driveTestScope(), At: session.AtStarting}, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}})
		beforeDrive := 0
		_, err := f.hooks.OnBeforeDrive(func(harness.Context, agentharness.BeforeDriveEvent) error { beforeDrive++; return nil }, agentharness.HookOptions{})
		require.NoError(t, err)
		f.setResponses([]ai.FauxResponseStep{driveTestResponse("answer")})
		outcome, err := DriveOperation(context.Background(), f.lane, f.drive)
		require.NoError(t, err)
		require.Equal(t, agentharness.DriveSettled, outcome.Kind)
		require.NotNil(t, outcome.Outcome)
		require.Equal(t, driveProcedureOperationID, outcome.Outcome.OperationID)
		require.Equal(t, "run", outcome.Outcome.Kind)
		require.Equal(t, "completed", outcome.Outcome.Status)
		require.Equal(t, 1, beforeDrive)
		require.Equal(t, 1, f.callCount())
		require.Nil(t, f.lane.SnapshotState().Operation)
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:431
	t.Run("leaves durable state unchanged when before_drive fails closed", func(t *testing.T) {
		f := newDriveProcedureFixture(t, false)
		starting := session.OperationState{OperationScope: driveTestScope(), At: session.AtStarting}
		f.install(t, starting, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}})
		_, err := f.hooks.OnBeforeDrive(func(harness.Context, agentharness.BeforeDriveEvent) error { return errors.New("blocked drive") }, agentharness.HookOptions{})
		require.NoError(t, err)
		_, err = DriveOperation(context.Background(), f.lane, f.drive)
		require.EqualError(t, err, "blocked drive")
		require.Equal(t, starting, f.state(t))
		require.Empty(t, f.storage.GetCommitAttempts())
	})
}

func TestPortWave04RuntimeCancellationReconciliation(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:450 (all thirteen dynamic rows)
	t.Run("reconciles every durable leaf without ordinary hook admission", func(t *testing.T) {
		for index := range 13 {
			t.Run(string([]session.OperationAt{session.AtStarting, session.AtCheckpoint, session.AtAssistantReady, session.AtAssistantEffectPending, session.AtAssistantRetryWait, session.AtTools, session.AtDeferredSuspended, session.AtDeferredEffectPending, session.AtSummaryDeciding, session.AtSummaryReady, session.AtSummaryEffectPending, session.AtSummaryRetryWait, session.AtNavigationReadyToCommit}[index]), func(t *testing.T) {
				f := newDriveProcedureFixture(t, false)
				installed := reconciliationCases(f)[index]
				f.install(t, installed.state, installed.intent, installed.options)
				beforeDrive := 0
				_, err := f.hooks.OnBeforeDrive(func(harness.Context, agentharness.BeforeDriveEvent) error { beforeDrive++; return nil }, agentharness.HookOptions{})
				require.NoError(t, err)
				outcome, err := DriveOperation(context.Background(), f.lane, f.drive)
				require.NoError(t, err)
				require.Equal(t, agentharness.DriveSettled, outcome.Kind)
				require.NotNil(t, outcome.Outcome)
				require.Equal(t, driveProcedureOperationID, outcome.Outcome.OperationID)
				require.Equal(t, "aborted", outcome.Outcome.Status)
				require.Zero(t, beforeDrive)
				require.Nil(t, f.lane.SnapshotState().Operation)
				record, err := f.lane.GetResult(context.Background(), driveProcedureOperationID)
				require.NoError(t, err)
				require.NotNil(t, record)
				require.Equal(t, "aborted", record.Status)
				require.Nil(t, driveTestValue(t, f, session.OperationMetaValue(driveProcedureOperationID)))
				require.Nil(t, driveTestValue(t, f, session.OperationStateValue(driveProcedureOperationID)))
				args, err := session.ScanValues(context.Background(), f.session, session.OperationToolArgsPrefix(driveProcedureOperationID, nil))
				require.NoError(t, err)
				require.Empty(t, args)
				memos, err := session.ScanValues(context.Background(), f.session, session.OperationToolMemoPrefix(driveProcedureOperationID, nil))
				require.NoError(t, err)
				require.Empty(t, memos)
				preparations, err := session.ScanValues(context.Background(), f.session, session.OperationPreparationPrefix(driveProcedureOperationID))
				require.NoError(t, err)
				require.Empty(t, preparations)
				if installed.state.At == session.AtDeferredSuspended || installed.state.At == session.AtDeferredEffectPending {
					require.Len(t, f.cancelledDeferred(), 1)
				}
				types := []agentharness.HarnessEventType{}
				for _, event := range f.events() {
					if slices.Contains(installed.terminalEvents, event.Type()) {
						types = append(types, event.Type())
					}
				}
				require.GreaterOrEqual(t, len(types), len(installed.terminalEvents))
				require.Equal(t, installed.terminalEvents, types[len(types)-len(installed.terminalEvents):])
			})
		}
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:494
	t.Run("durably drains abortable input once and preserves lane-owned input through terminal cleanup", func(t *testing.T) {
		f := newDriveProcedureFixture(t, false)
		state := session.OperationState{OperationScope: driveTestScope(), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: "tip"}
		f.install(t, state, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}})
		f.queue(t, driveTestQueuedMessage("steer", "steer", "steer"), driveTestQueuedMessage("write", "write", "write"), driveTestQueuedMessage("follow", "followUp", "follow"), driveTestQueuedMessage("next", "nextRun", "next"))
		f.storage.ClearCommitAttempts()
		requested, err := f.lane.RequestOperationAbort(context.Background(), driveProcedureOperationID)
		require.NoError(t, err)
		require.Equal(t, driveProcedureOperationID, requested.OperationID)
		require.True(t, requested.NewlyRequested)
		require.Equal(t, []agent.AgentMessage{laneUser("steer", 1)}, requested.Steer)
		require.Equal(t, []agent.AgentMessage{laneUser("follow", 1)}, requested.FollowUp)
		require.Error(t, f.drive.Gate.Signal().Err())
		require.NoError(t, f.drive.CloseSignal.Err())
		remaining := []session.InboxItem{{EntryID: "write", Kind: "write"}, {EntryID: "next", Kind: "nextRun"}}
		require.Equal(t, remaining, f.lane.SnapshotState().Inbox)
		require.Nil(t, driveTestValue(t, f, session.PendingEntryValue("steer")))
		require.Nil(t, driveTestValue(t, f, session.PendingEntryValue("follow")))
		events := driveTestEventsOf(f, agentharness.EventOperationAbort)
		require.Len(t, events, 1)
		abort, ok := events[0].Payload.(agentharness.OperationAbortPayload)
		require.True(t, ok)
		require.Equal(t, driveProcedureOperationID, abort.OperationID)
		require.Equal(t, []agent.AgentMessage{laneUser("steer", 1)}, abort.Steer)
		require.Equal(t, []agent.AgentMessage{laneUser("follow", 1)}, abort.FollowUp)
		commitCount := len(f.storage.GetCommitAttempts())
		eventCount := len(f.events())
		requested, err = f.lane.RequestOperationAbort(context.Background(), driveProcedureOperationID)
		require.NoError(t, err)
		require.Equal(t, agentharness.AbortRequest{OperationID: driveProcedureOperationID, NewlyRequested: false, Steer: []agent.AgentMessage{}, FollowUp: []agent.AgentMessage{}}, requested)
		require.Len(t, f.storage.GetCommitAttempts(), commitCount)
		require.Len(t, f.events(), eventCount)
		outcome, err := DriveOperation(context.Background(), f.lane, f.drive)
		require.NoError(t, err)
		require.Equal(t, agentharness.DriveSettled, outcome.Kind)
		require.NotNil(t, outcome.Outcome)
		require.Equal(t, "aborted", outcome.Outcome.Status)
		require.Equal(t, remaining, f.lane.SnapshotState().Inbox)
		require.NotNil(t, driveTestValue(t, f, session.PendingEntryValue("write")))
		require.NotNil(t, driveTestValue(t, f, session.PendingEntryValue("next")))
		f.storage.ClearCommitAttempts()
		_, err = f.lane.RequestOperationAbort(context.Background(), driveProcedureOperationID)
		require.IsType(t, &harness.OperationMismatch{}, err)
		require.Empty(t, f.storage.GetCommitAttempts())
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:583
	t.Run("marks cancellation without installing a Drive", func(t *testing.T) {
		f := newDriveProcedureFixture(t, false)
		f.install(t, session.OperationState{OperationScope: driveTestScope(), At: session.AtStarting}, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}})
		f.lane.ActiveDrive = nil
		requested, err := f.lane.RequestOperationAbort(context.Background(), driveProcedureOperationID)
		require.NoError(t, err)
		require.True(t, requested.NewlyRequested)
		require.Nil(t, f.lane.CurrentDrive())
		require.Equal(t, session.ControlCancelRequested, f.state(t).Control.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:600
	t.Run("cancels an admitted retry timer only after the abort marker commits", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			origin := time.Now()
			originalNow := runtimeNow
			runtimeNow = func() int64 { return 1000 + time.Since(origin).Milliseconds() }
			t.Cleanup(func() { runtimeNow = originalNow })
			f := newDriveProcedureFixture(t, false)
			state := session.OperationState{OperationScope: driveTestScope(), At: session.AtAssistantRetryWait, GenerationContext: session.GenerationContext{StepID: "step", TriggerEntryID: "tip", Configuration: f.configuration, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 10, MaxAgentDelayMs: 30000}}, NextAttempt: 2, NotBefore: 2000, ErrorMessage: "retry"}
			f.install(t, state, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{"tip"}}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}})
			drive := NewDrive(context.Background(), agentharness.DriveOptions{OperationID: driveProcedureOperationID, WaitForRetry: true})
			f.lane.ActiveDrive = drive
			admitted := make(chan struct{})
			signalAdmitted := sync.OnceFunc(func() { close(admitted) })
			originalTimer := runtimeNewTimer
			runtimeNewTimer = func(delay time.Duration) *time.Timer { timer := originalTimer(delay); signalAdmitted(); return timer }
			t.Cleanup(func() { runtimeNewTimer = originalTimer })
			running := driveTestAsync(t, func() { drive.CloseGate(errors.New("fixture closed")) }, func() (agentharness.DriveOutcome, error) { return DriveOperation(context.Background(), f.lane, drive) })
			<-admitted
			requested, err := f.lane.RequestOperationAbort(context.Background(), driveProcedureOperationID)
			require.NoError(t, err)
			require.True(t, requested.NewlyRequested)
			outcome := <-running
			require.NoError(t, outcome.err)
			require.Equal(t, agentharness.DriveSettled, outcome.result.Kind)
			require.NotNil(t, outcome.result.Outcome)
			require.Equal(t, "aborted", outcome.result.Outcome.Status)
			require.NotContains(t, f.eventTypes(), agentharness.EventRetryStart)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:638
	t.Run("drops a stale structural hook result when cancellation commits first", func(t *testing.T) {
		f := newDriveProcedureFixture(t, false)
		deciding := session.OperationState{OperationScope: driveTestScope(), At: session.AtSummaryDeciding, Task: driveTestCompactionTask()}
		prep := driveTestCompactionPreparation()
		prep.RetainedTail = []agent.AgentMessage{}
		prep.TokensBefore = 100
		prep.Settings = compaction.DefaultCompactionSettings
		f.install(t, deciding, session.OperationIntent{Kind: "compaction"}, driveTestInstallOptions{Entries: []session.Entry{driveTestEntry("tip", nil, "history")}, Preparation: &prep})
		started := make(chan struct{})
		release := make(chan struct{})
		releaseHook := sync.OnceFunc(func() { close(release) })
		_, err := f.hooks.OnBeforeCompaction(func(harness.Context, agentharness.BeforeCompactionEvent) (*agentharness.BeforeCompactionResult, error) {
			close(started)
			<-release
			return &agentharness.BeforeCompactionResult{Compaction: &compaction.CompactResult{Summary: "stale", TokensBefore: 100, RetainedTail: []agent.AgentMessage{}}}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		running := driveTestAsync(t, releaseHook, func() (ProcedureResult, error) {
			return RunStructuralDecision(context.Background(), f.lane, f.drive, deciding)
		})
		<-started
		_, err = f.lane.RequestOperationAbort(context.Background(), driveProcedureOperationID)
		require.NoError(t, err)
		releaseHook()
		result := <-running
		driveTestContinue(t, result.result, result.err)
		entries, err := f.session.FindEntries(context.Background(), &session.EntryQuery{Type: session.EntryTypeCompaction})
		require.NoError(t, err)
		require.Empty(t, entries)
		outcome, err := DriveOperation(context.Background(), f.lane, f.drive)
		require.NoError(t, err)
		require.Equal(t, agentharness.DriveSettled, outcome.Kind)
		require.NotNil(t, outcome.Outcome)
		require.Equal(t, "aborted", outcome.Outcome.Status)
		events := f.events()
		end, ok := events[len(events)-1].Payload.(agentharness.CompactionEndPayload)
		require.True(t, ok)
		require.Equal(t, "aborted", end.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:685-695
	t.Run("keeps the deferred cleanup signal separate from operation abort", func(t *testing.T) {
		drive := NewDrive(harness.BackgroundContext(), agentharness.DriveOptions{OperationID: "01950000-0000-7000-8000-000000000001"})
		drive.BeginAbort(func() error { return nil })
		drive.SignalAbort()
		require.ErrorIs(t, drive.Gate.Signal().Err(), context.Canceled)
		require.NoError(t, drive.CloseSignal.Err())
		closed := errors.New("closed")
		drive.CloseGate(closed)
		require.ErrorIs(t, drive.CloseSignal.Err(), context.Canceled)
		require.Same(t, closed, context.Cause(drive.CloseSignal))
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:697
	t.Run("can crash between cancelled response settlement and terminal cleanup", func(t *testing.T) {
		f := newDriveProcedureFixture(t, false)
		installed := reconciliationCases(f)[3]
		f.install(t, installed.state, installed.intent, installed.options)
		r, err := ReconcileOperation(context.Background(), f.lane, f.drive)
		driveTestContinue(t, r, err)
		require.Equal(t, session.AtCheckpoint, f.state(t).At)
		entry := driveTestEntryAt(t, f, "assistant-response")
		require.Equal(t, session.EntryTypeMessage, entry.Type)
		require.NotNil(t, entry.Message.Assistant)
		require.Equal(t, ai.StopReasonAborted, entry.Message.Assistant.StopReason)
		record, err := f.lane.GetResult(context.Background(), driveProcedureOperationID)
		require.NoError(t, err)
		require.Nil(t, record)
		frames, err := session.ReadList(context.Background(), f.session, session.PendingAssistantFrames(driveProcedureOperationID, "assistant-response"), nil)
		require.NoError(t, err)
		require.Empty(t, frames)
		resumed := NewDrive(context.Background(), agentharness.DriveOptions{OperationID: driveProcedureOperationID})
		f.lane.ActiveDrive = resumed
		t.Cleanup(func() { resumed.CloseGate(errors.New("fixture closed")) })
		outcome, err := DriveOperation(context.Background(), f.lane, resumed)
		require.NoError(t, err)
		require.Equal(t, agentharness.DriveSettled, outcome.Kind)
		require.NotNil(t, outcome.Outcome)
		require.Equal(t, "aborted", outcome.Outcome.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-reconcile.test.ts:725
	t.Run("ignores deferred-provider cancellation failure", func(t *testing.T) {
		f := newDriveProcedureFixture(t, false)
		state, entry, _ := reconciliationDeferred(f, false)
		provider := *f.provider
		provider.CancelDeferred = func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredCancelOptions) error {
			return errors.New("remote cancellation failed")
		}
		f.models.SetProvider(&provider)
		f.install(t, state, session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}, driveTestInstallOptions{Entries: []session.Entry{entry}})
		outcome, err := DriveOperation(context.Background(), f.lane, f.drive)
		require.NoError(t, err)
		require.Equal(t, agentharness.DriveSettled, outcome.Kind)
		require.NotNil(t, outcome.Outcome)
		require.Equal(t, "aborted", outcome.Outcome.Status)
	})
}
