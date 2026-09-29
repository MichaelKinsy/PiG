package runtime

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/ai"
)

type retryDeferredFixture struct {
	*generationFixture
	backend *session.MemoryStorage
	gating  *sessiontesting.GatingStorage
}

func newRetryDeferredFixture(t *testing.T, gated bool, pendingFetches int, deferredSubmission bool) *retryDeferredFixture {
	t.Helper()
	backend := session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 100 }})
	var storage session.Storage = backend
	var gating *sessiontesting.GatingStorage
	if gated {
		gating = sessiontesting.NewGatingStorage(backend)
		storage = gating
	}
	f := &retryDeferredFixture{generationFixture: newGenerationFixture(t, storage, laneFauxOptions{PendingFetches: pendingFetches}), backend: backend, gating: gating}
	f.config.StreamOptions.Deferred = &harness.AgentHarnessDeferredOption{Enabled: deferredSubmission}
	f.config.RetryPolicy.BaseDelayMs = 10
	if gating != nil {
		t.Cleanup(gating.Discard)
	}
	return f
}
func (f *retryDeferredFixture) submit(t *testing.T, response ai.FauxResponse) session.OperationState {
	t.Helper()
	f.faux.setResponses([]ai.FauxResponseStep{ai.FauxStaticStep(response)})
	ready := f.ready(t)
	_, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
	require.NoError(t, err)
	suspended := f.currentRun(t)
	require.Equal(t, session.AtDeferredSuspended, suspended.At)
	return suspended
}
func deferredAnswer() ai.FauxResponse {
	return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop", Timestamp: new(int64(20))}
}
func (f *retryDeferredFixture) installDrive(wait, poll bool) *Drive {
	drive := NewDrive(f.drive.Context, agentharness.DriveOptions{OperationID: generationOperationID, WaitForRetry: wait, PollDeferred: poll})
	f.drive = drive
	f.lane.ActiveDrive = drive
	return drive
}
func (f *retryDeferredFixture) unknownPoll(t *testing.T, suspended session.OperationState) session.OperationState {
	t.Helper()
	pending := session.OperationState{OperationScope: f.currentRun(t).OperationScope, At: session.AtDeferredEffectPending, StepID: suspended.StepID, SourceEntryID: suspended.SourceEntryID, Poll: suspended.Poll + 1, ResponseEntryID: f.session.IdGenerator().Next(nil), UsageID: f.session.IdGenerator().Next(nil), Configuration: suspended.Configuration, StreamOptions: suspended.StreamOptions}
	f.replaceRun(t, pending, session.AppendList(session.PendingAssistantFrames(generationOperationID, pending.ResponseEntryID), ai.AssistantMessageFrame(ai.TextDeltaFrame{ContentIndex: 0, Delta: "old"})))
	return pending
}
func withRuntimeClock(t *testing.T, milliseconds int64) *atomic.Int32 {
	t.Helper()
	originalNow, originalTimer := runtimeNow, runtimeNewTimer
	start := time.Now()
	calls := &atomic.Int32{}
	runtimeNow = func() int64 { return milliseconds + time.Since(start).Milliseconds() }
	runtimeNewTimer = func(delay time.Duration) *time.Timer { calls.Add(1); return originalTimer(delay) }
	t.Cleanup(func() { runtimeNow, runtimeNewTimer = originalNow, originalTimer })
	return calls
}
func retryWaitState(ready session.OperationState, deadline int64) session.OperationState {
	return session.OperationState{OperationScope: ready.OperationScope, At: session.AtAssistantRetryWait, GenerationContext: ready.GenerationContext, NextAttempt: 2, NotBefore: deadline, ErrorMessage: "retry"}
}

func TestPortWave05RetryDeferred(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:237
	t.Run("classifies a live retryable provider error into durable retry wait", func(t *testing.T) {
		f := newRetryDeferredFixture(t, false, 0, false)
		ready := f.ready(t)
		f.faux.setResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{}, StopReason: "error", ErrorMessage: "503 service unavailable", Timestamp: new(int64(10))})})
		result, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		state := f.currentRun(t)
		require.Equal(t, session.AtAssistantRetryWait, state.At)
		require.Equal(t, 2, state.NextAttempt)
		require.Equal(t, "503 service unavailable", state.ErrorMessage)
		events := f.eventSnapshot()
		require.NotEmpty(t, events)
		require.Equal(t, agentharness.EventRetryScheduled, events[len(events)-1].Type())
		require.Equal(t, 2, events[len(events)-1].Payload.(agentharness.RetryScheduledPayload).Attempt)
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:256
	t.Run("returns a durable waiting outcome without a timer or write when local waiting is disabled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			timers := withRuntimeClock(t, 1000)
			f := newRetryDeferredFixture(t, false, 0, false)
			ready := f.ready(t)
			retry := retryWaitState(ready, 1100)
			f.replaceRun(t, retry)
			f.storage.ClearCommitAttempts()
			timers.Store(0)
			result, err := RunGeneration(t.Context(), f.lane, f.drive, retry)
			require.NoError(t, err)
			require.Equal(t, ProcedureResult{Kind: ProcedureWaiting, Outcome: agentharness.DriveOutcome{Kind: agentharness.DriveWaiting, OperationID: generationOperationID, Reason: agentharness.DriveWaitRetry, NotBefore: 1100}}, result)
			require.Zero(t, timers.Load())
			require.Empty(t, f.storage.GetCommitAttempts())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:281
	t.Run("commits ready at the deadline and emits retry lifecycle around the next attempt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			withRuntimeClock(t, 2000)
			f := newRetryDeferredFixture(t, false, 0, false)
			ready := f.ready(t)
			retry := retryWaitState(ready, 2000)
			f.replaceRun(t, retry)
			f.storage.ClearCommitAttempts()
			result, err := RunGeneration(t.Context(), f.lane, f.drive, retry)
			require.NoError(t, err)
			require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
			next := f.currentRun(t)
			require.Equal(t, session.AtAssistantReady, next.At)
			events := f.eventSnapshot()
			require.NotEmpty(t, events)
			require.Equal(t, agentharness.EventRetryStart, events[len(events)-1].Type())
			require.Equal(t, 2, events[len(events)-1].Payload.(agentharness.RetryStartPayload).Attempt)
			f.response("retried", 30)
			_, err = RunGeneration(t.Context(), f.lane, f.drive, next)
			require.NoError(t, err)
			matched := false
			for _, event := range f.eventsOf(agentharness.EventRetryEnd) {
				payload := event.Payload.(agentharness.RetryEndPayload)
				matched = matched || payload.Attempt == 2 && payload.Success
			}
			require.True(t, matched, "missing successful retry_end for attempt 2")
			f.requireRestores(t)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:311
	t.Run("admits an abort-aware timer only for local waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			withRuntimeClock(t, 3000)
			f := newRetryDeferredFixture(t, false, 0, false)
			ready := f.ready(t)
			retry := retryWaitState(ready, 3100)
			f.replaceRun(t, retry)
			drive := f.installDrive(true, false)
			f.storage.ClearCommitAttempts()
			var result ProcedureResult
			done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) {
				value, err := RunGeneration(t.Context(), f.lane, drive, retry)
				result = value
				return value, err
			})
			synctest.Wait()
			// Advance only the virtual clock, preserving upstream's separate 99ms and 1ms boundaries.
			<-time.After(99 * time.Millisecond)
			require.Empty(t, f.storage.GetCommitAttempts())
			<-time.After(time.Millisecond)
			require.NoError(t, done())
			require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
			state := f.currentRun(t)
			require.Equal(t, session.AtAssistantReady, state.At)
			require.Equal(t, 2, state.NextAttempt)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:341
	t.Run("waits without a permit, then performs at most one pending poll from captured options", func(t *testing.T) {
		f := newRetryDeferredFixture(t, false, 1, true)
		suspended := f.submit(t, deferredAnswer())
		f.storage.ClearCommitAttempts()
		result, err := RunDeferredSuspended(t.Context(), f.lane, f.drive, suspended)
		require.NoError(t, err)
		require.Equal(t, ProcedureWaiting, result.Kind)
		require.Equal(t, agentharness.DriveWaiting, result.Outcome.Kind)
		require.Equal(t, generationOperationID, result.Outcome.OperationID)
		require.Equal(t, agentharness.DriveWaitDeferred, result.Outcome.Reason)
		require.NotNil(t, result.Outcome.Deferred)
		require.NotEmpty(t, result.Outcome.Deferred.ID)
		require.Zero(t, f.faux.deferredFetchCount())
		require.Empty(t, f.storage.GetCommitAttempts())
		var hookOptions *harness.AgentHarnessStreamOptions
		_, err = f.hooks.OnBeforeRequest(func(_ context.Context, event agentharness.BeforeRequestEvent) (*agentharness.BeforeRequestResult, error) {
			if event.Step == "deferred" {
				hookOptions = new(event.StreamOptions)
			}
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		var fetchOptions *ai.DeferredFetchOptions
		provider := *f.provider
		fetch := provider.FetchDeferred
		require.NotNil(t, fetch)
		provider.FetchDeferred = func(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
			fetchOptions = &options
			return fetch(ctx, model, handle, options)
		}
		f.models.SetProvider(&provider)
		options := agentharness.DriveOptions{OperationID: generationOperationID, PollDeferred: true}
		drive := NewDrive(t.Context(), options)
		f.drive = drive
		f.lane.ActiveDrive = drive
		result, err = RunDeferred(t.Context(), f.lane, drive, f.currentRun(t))
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		require.Zero(t, drive.DeferredPermits)
		require.Equal(t, agentharness.DriveOptions{OperationID: generationOperationID, PollDeferred: true}, options)
		require.NotNil(t, hookOptions)
		require.Equal(t, &harness.AgentHarnessDeferredOption{Enabled: false}, hookOptions.Deferred)
		require.NotNil(t, fetchOptions)
		require.Equal(t, new(float64(0)), fetchOptions.Wait)
		require.True(t, fetchOptions.Signal == drive.Gate.Signal(), "fetch signal must be the drive gate signal")
		current := f.currentRun(t)
		require.Equal(t, session.AtDeferredSuspended, current.At)
		require.Equal(t, 1, current.Poll)
		require.Equal(t, 1, f.faux.deferredFetchCount())
		types := f.eventTypes()
		require.Equal(t, []agentharness.HarnessEventType{agentharness.EventTurnEnd, agentharness.EventRunSuspend}, types[len(types)-2:])
		result, err = RunDeferred(t.Context(), f.lane, drive, f.currentRun(t))
		require.NoError(t, err)
		require.Equal(t, ProcedureWaiting, result.Kind)
		require.Equal(t, agentharness.DriveWaitDeferred, result.Outcome.Reason)
		require.Equal(t, 1, f.faux.deferredFetchCount())
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:399
	t.Run("declines poll intent when cancellation wins preparation", func(t *testing.T) {
		f := newRetryDeferredFixture(t, false, 0, true)
		suspended := f.submit(t, deferredAnswer())
		drive := f.installDrive(false, true)
		started, release := laneTestBarrier(t)
		_, err := f.hooks.OnBeforeRequest(func(_ context.Context, event agentharness.BeforeRequestEvent) (*agentharness.BeforeRequestResult, error) {
			if event.Step == "deferred" {
				close(started)
				<-release.done
			}
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		var result ProcedureResult
		done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) {
			value, err := RunDeferredSuspended(t.Context(), f.lane, drive, suspended)
			result = value
			return value, err
		}, release.open)
		<-started
		next := f.currentRun(t)
		next.Control = session.Control{Status: "cancel_requested", RequestedAt: 10}
		f.replaceRun(t, next)
		release.open()
		require.NoError(t, done())
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		require.Zero(t, f.faux.deferredFetchCount())
		state := f.currentRun(t)
		require.Equal(t, session.AtDeferredSuspended, state.At)
		require.Equal(t, session.ControlCancelRequested, state.Control.Status)
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:438
	t.Run("plans ready deferred tool calls with the poll turn identity and follower result ids", func(t *testing.T) {
		f := newRetryDeferredFixture(t, false, 0, true)
		suspended := f.submit(t, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("lookup", map[string]any{"query": "value"}, "")}, StopReason: "toolUse", Timestamp: new(int64(20))})
		drive := f.installDrive(false, true)
		result, err := RunDeferredSuspended(t.Context(), f.lane, drive, suspended)
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		run := f.currentRun(t)
		require.Equal(t, session.AtTools, run.At)
		require.Equal(t, suspended.StepID+":poll:1", run.Batch.TurnID)
		require.Len(t, run.Batch.Calls, 1)
		require.Equal(t, "planned", run.Batch.Calls[0].Status)
		require.Zero(t, run.Batch.Calls[0].SourceIndex)
		require.NotNil(t, run.LatestAssistantEntryID)
		require.Equal(t, (*run.LatestAssistantEntryID)[:13], run.Batch.Calls[0].ResultEntryID[:13])
		types := f.eventTypes()
		require.Equal(t, agentharness.EventUsage, types[len(types)-1])
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:463
	t.Run("consumes its permit only after the fresh intent commit lands", func(t *testing.T) {
		f := newRetryDeferredFixture(t, true, 0, true)
		suspended := f.submit(t, ai.FauxResponse{Content: []ai.FauxContentBlock{}, StopReason: "error", ErrorMessage: "failed", Timestamp: new(int64(20))})
		drive := f.installDrive(false, true)
		resumed, fetched := make(chan struct{}), make(chan struct{})
		f.onEmit = func(events []agentharness.HarnessEvent) error {
			for _, event := range events {
				if event.Type() == agentharness.EventRunResume {
					close(resumed)
				}
			}
			return nil
		}
		provider := *f.provider
		fetch := provider.FetchDeferred
		provider.FetchDeferred = func(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
			stream, err := fetch(ctx, model, handle, options)
			close(fetched)
			return stream, err
		}
		f.models.SetProvider(&provider)
		f.gating.Arm()
		var result ProcedureResult
		done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) {
			value, err := RunDeferredSuspended(t.Context(), f.lane, drive, suspended)
			result = value
			return value, err
		}, f.gating.Discard)
		require.NoError(t, f.gating.WaitPending(1))
		require.Equal(t, 1, drive.DeferredPermits)
		require.Zero(t, f.faux.deferredFetchCount())
		require.NoError(t, f.gating.Next(1))
		<-resumed
		require.Zero(t, drive.DeferredPermits)
		<-fetched
		require.Equal(t, 1, f.faux.deferredFetchCount())
		// Upstream releases exactly two more commits: a start frame, then settlement. Assert the first kind before release so a missing frame fails instead of parking the test forever on a nonexistent second commit.
		require.NoError(t, f.gating.WaitPending(1))
		attempts := f.storage.GetCommitAttempts()
		require.Contains(t, writeKinds(attempts[len(attempts)-1], true), "list:append:pi.pending.assistant_frame")
		require.NoError(t, f.gating.Next(2))
		require.NoError(t, done())
		require.Equal(t, ProcedureSettled, result.Kind)
		require.Equal(t, generationOperationID, result.Record.OperationID)
		require.Equal(t, "run", result.Record.Kind)
		require.Equal(t, "failed", result.Record.Status)
		require.Nil(t, f.lane.SnapshotState().Operation)
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:489
	t.Run("leaves suspended state unchanged when the fresh intent is discarded", func(t *testing.T) {
		f := newRetryDeferredFixture(t, true, 0, true)
		suspended := f.submit(t, deferredAnswer())
		drive := f.installDrive(false, true)
		f.gating.Arm()
		done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) { return RunDeferredSuspended(t.Context(), f.lane, drive, suspended) }, f.gating.Discard)
		require.NoError(t, f.gating.WaitPending(1))
		f.gating.Discard()
		require.ErrorContains(t, done(), "commit discarded")
		require.Equal(t, 1, drive.DeferredPermits)
		require.Zero(t, f.faux.deferredFetchCount())
		durable, err := session.GetValue(t.Context(), f.backend, session.OperationStateValue(generationOperationID))
		require.NoError(t, err)
		require.NotNil(t, durable)
		require.Equal(t, session.AtDeferredSuspended, durable.Value.At)
		require.Zero(t, durable.Value.Poll)
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:508
	t.Run("replaces an unknown poll under fresh ids at the same poll number and deletes old frames", func(t *testing.T) {
		f := newRetryDeferredFixture(t, false, 0, true)
		suspended := f.submit(t, deferredAnswer())
		unknown := f.unknownPoll(t, suspended)
		f.storage.ClearCommitAttempts()
		noPermit := f.installDrive(false, false)
		result, err := RecoverDeferredPoll(t.Context(), f.lane, noPermit, unknown)
		require.NoError(t, err)
		require.Equal(t, ProcedureWaiting, result.Kind)
		require.Equal(t, agentharness.DriveWaitDeferred, result.Outcome.Reason)
		frames, err := session.ReadList(t.Context(), f.session, session.PendingAssistantFrames(generationOperationID, unknown.ResponseEntryID), nil)
		require.NoError(t, err)
		require.Len(t, frames, 1)
		require.Zero(t, f.faux.deferredFetchCount())
		replacement := f.installDrive(false, true)
		result, err = RecoverDeferredPoll(t.Context(), f.lane, replacement, unknown)
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		var intent []session.Write
		for _, writes := range f.storage.GetCommitAttempts() {
			for _, write := range writes {
				if value, ok := write.(session.ListDeleteWrite); ok && value.Namespace == "pi.pending.assistant_frame" && strings.HasSuffix(value.Key, unknown.ResponseEntryID) {
					intent = writes
					break
				}
			}
			if intent != nil {
				break
			}
		}
		require.Equal(t, []string{"list:delete", "value:set"}, writeKinds(intent, false))
		var state *session.OperationState
		for _, write := range intent {
			if value, ok := write.(session.ValueSetWrite); ok && value.Namespace == "pi.op.state" {
				state = new(value.Value.(session.OperationState))
			}
		}
		require.NotNil(t, state)
		require.Equal(t, session.AtDeferredEffectPending, state.At)
		require.Equal(t, unknown.Poll, state.Poll)
		require.NotContains(t, state.ResponseEntryID, unknown.ResponseEntryID)
		require.NotContains(t, state.UsageID, unknown.UsageID)
		entry, err := f.session.GetEntry(t.Context(), unknown.ResponseEntryID)
		require.NoError(t, err)
		require.Nil(t, entry)
		usage, err := f.storage.ScanUsage(t.Context(), session.UsageScan{})
		require.NoError(t, err)
		for _, row := range usage {
			require.NotEqual(t, unknown.UsageID, row.ID)
		}
		requireEmptyFrames(t, f.procedureFixture, unknown.ResponseEntryID)
		require.Equal(t, session.AtCheckpoint, f.currentRun(t).At)
		recovered := false
		for _, event := range f.eventSnapshot() {
			recovered = recovered || event.Recovery
		}
		require.True(t, recovered)
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-retry-deferred.test.ts:574
	t.Run("abandons unknown ids and frames into configuration failure when the captured model is unavailable", func(t *testing.T) {
		f := newRetryDeferredFixture(t, false, 0, true)
		suspended := f.submit(t, deferredAnswer())
		unknown := f.unknownPoll(t, suspended)
		f.models.DeleteProvider(f.provider.ID)
		f.storage.ClearCommitAttempts()
		drive := f.installDrive(false, true)
		result, err := RecoverDeferredPoll(t.Context(), f.lane, drive, unknown)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, result.Kind)
		require.Equal(t, generationOperationID, result.Record.OperationID)
		require.Equal(t, "run", result.Record.Kind)
		require.Equal(t, "failed", result.Record.Status)
		require.NotNil(t, result.Record.Error)
		require.Equal(t, "model_unavailable", result.Record.Error.Code)
		require.Equal(t, 1, drive.DeferredPermits)
		require.Zero(t, f.faux.deferredFetchCount())
		require.Nil(t, f.lane.SnapshotState().Operation)
		attempts := f.storage.GetCommitAttempts()
		require.NotEmpty(t, attempts)
		require.Equal(t, []string{"value:delete:pi.op.meta", "value:delete:pi.op.state", "list:delete:pi.pending.assistant_frame", "value:set:pi.result", "value:set:pi.lane.state"}, writeKinds(attempts[len(attempts)-1], true))
		requireNoEntryOrUsage(t, f.procedureFixture)
		f.requireRestores(t)
	})
}
