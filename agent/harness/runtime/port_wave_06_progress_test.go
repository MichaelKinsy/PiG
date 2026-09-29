package runtime

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave06RuntimeActiveDriveOwnership(t *testing.T) {
	t.Parallel()
	// upstream: packages/agent/test/harness/runtime/progress.test.ts:161
	t.Run("settles or rejects the shared completion without exposing owner controls through the gate", func(t *testing.T) {
		ctx := harness.BackgroundContext()
		settled := NewDrive(ctx, agentharness.DriveOptions{OperationID: "operation"})
		outcome := agentharness.DriveOutcome{
			Kind:        agentharness.DriveWaiting,
			OperationID: "operation",
			Reason:      agentharness.DriveWaitRetry,
			NotBefore:   10,
		}
		settled.Settle(outcome)
		settled.Fail(errors.New("late failure"))
		got, err := settled.Completion(ctx)
		if err != nil || got != outcome {
			t.Fatalf("settled completion = (%+v, %v), want (%+v, nil)", got, err, outcome)
		}

		failed := NewDrive(ctx, agentharness.DriveOptions{OperationID: "operation"})
		failure := errors.New("drive failed")
		failed.Fail(failure)
		if _, err := failed.Completion(ctx); err != failure { //nolint:errorlint // Upstream rejects with the same Error instance, not a wrapped error.
			t.Fatalf("failed completion error = %v, want the original %v", err, failure)
		}
		// Check property absence regardless of an accidentally exposed control's signature.
		gateType := reflect.TypeOf(failed.Gate)
		if _, exposed := gateType.MethodByName("BeginAbort"); exposed {
			t.Fatal("procedure-facing gate exposes the owner's BeginAbort method")
		}
		if _, exposed := gateType.Elem().FieldByName("BeginAbort"); exposed {
			t.Fatal("procedure-facing gate exposes the owner's BeginAbort field")
		}
	})

	// upstream: packages/agent/test/harness/runtime/progress.test.ts:175
	t.Run("strips invocation cancellation from pass context and owns policy and gate control", func(t *testing.T) {
		controller, cancel := harness.WithCancel(harness.BackgroundContext())
		t.Cleanup(func() { cancel(nil) })
		caller := harness.WithAbortSignal(harness.BackgroundContext(), controller)
		drive := NewDrive(caller, agentharness.DriveOptions{
			OperationID:  "operation",
			WaitForRetry: true,
			PollDeferred: true,
		})

		if drive.OperationID != "operation" {
			t.Fatalf("operation id = %q, want operation", drive.OperationID)
		}
		if drive.Context.Done() != nil {
			t.Fatal("drive context retains the invocation abort signal")
		}
		if !drive.WaitForRetry {
			t.Fatal("drive did not retain waitForRetry")
		}
		if drive.DeferredPermits != 1 {
			t.Fatalf("deferred permits = %d, want 1", drive.DeferredPermits)
		}

		closed := errors.New("closed")
		drive.CloseGate(closed)
		if drive.Gate.Signal().Err() == nil {
			t.Fatal("closed gate signal is not aborted")
		}
		if err := drive.Gate.Admit(func() error { return nil }); err == nil || err.Error() != closed.Error() {
			t.Fatalf("closed gate admission error = %v, want %v", err, closed)
		}
	})
}

func progressAssistantState(responseID string) session.OperationState {
	return session.OperationState{
		OperationScope: restoreScope(), At: session.AtAssistantEffectPending,
		GenerationContext: session.GenerationContext{StepID: "step", TriggerEntryID: "trigger", Configuration: laneTestConfiguration(), StreamOptions: harness.AgentHarnessStreamOptions{}, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 1, MaxAgentDelayMs: 30000}},
		Attempt:           1, ResponseEntryID: responseID, UsageID: "usage", IntendedOutputLimit: 100, ContextWindow: 1000,
	}
}

type progressStorage struct {
	session.Storage
	mu               sync.Mutex
	failure          error
	beforeNextCommit func()
	reads            []session.StoredAddressBase
}

func (storage *progressStorage) Commit(ctx context.Context, writes []session.Write) (session.CommitResult, error) {
	storage.mu.Lock()
	before := storage.beforeNextCommit
	storage.beforeNextCommit = nil
	storage.mu.Unlock()
	if before != nil {
		before()
	}
	storage.mu.Lock()
	failure := storage.failure
	storage.failure = nil
	storage.mu.Unlock()
	if failure != nil {
		return session.CommitResult{}, failure
	}
	return storage.Storage.Commit(ctx, writes)
}

func (storage *progressStorage) GetValue(ctx context.Context, address session.StoredAddressBase) (*session.StoredValue[any], error) {
	storage.mu.Lock()
	storage.reads = append(storage.reads, address)
	storage.mu.Unlock()
	return storage.Storage.GetValue(ctx, address)
}

func progressFixture(t *testing.T, state session.OperationState) (*Lane, *Drive, *progressStorage) {
	t.Helper()
	isolateHarnessTest(t)
	ctx := context.Background()
	storage := &progressStorage{Storage: session.NewMemoryStorage(nil)}
	opened := session.NewStorageBackedSession(session.SessionMetadata{ID: t.Name(), CreatedAt: 1, StorageVersion: 1}, storage, nil)
	t.Cleanup(func() { requireHarnessOK(t, opened.Close(ctx)) })
	meta := session.OperationMeta{OperationID: "operation", Lane: "main", StartedAt: 1, Intent: session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}}
	projection := LaneState{Configuration: laneTestConfiguration(), Inbox: []session.InboxItem{}, Operation: &session.Operation{Meta: meta, State: state}}
	lane := NewLane("main", opened, ai.CreateModels(), agentharness.NewHookRegistry(func(harness.Context, error, agentharness.HookName, string) error { return nil }), projection,
		func(_ harness.Context, err error) error { return err },
		func(harness.Context, []agentharness.HarnessEvent) (func() error, error) { return nil, nil },
		func(harness.Context, agentharness.LaneSnapshot, func(agentharness.HarnessEvent) bool, agentharness.ResnapshotCapture[agentharness.LaneSnapshot]) (agentharness.WatchHandle[agentharness.LaneSnapshot], error) {
			return nil, errors.New("watch is not used by progress tests")
		},
		func() Config {
			return Config{Tools: []harness.AgentHarnessTool{}, Resources: agentharness.Resources{}, StreamOptions: harness.AgentHarnessStreamOptions{}, RetryPolicy: ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 1000}, Compaction: compaction.DefaultCompactionSettings, SteeringMode: "all", FollowUpMode: "all", ToolExecution: "parallel", ToProviderMessages: func(harness.Context, []agent.AgentMessage) ([]ai.Message, error) { return []ai.Message{}, nil }, EntryProjectors: map[string]session.EntryProjector{}}
		},
	)
	harnessCommit(t, opened,
		session.SetValue(session.BranchTip("main"), (*string)(nil)),
		session.SetValue(session.LaneConfig("main"), laneTestConfiguration()),
		session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: new("operation"), Inbox: []session.InboxItem{}}),
		session.SetValue(session.OperationMetaValue("operation"), meta),
		session.SetValue(session.OperationStateValue("operation"), state),
	)
	drive := NewDrive(ctx, agentharness.DriveOptions{OperationID: "operation"})
	lane.ActiveDrive = drive
	return lane, drive, storage
}

func TestPortWave06RuntimeProgressChannels(t *testing.T) {
	ctx := context.Background()
	// upstream: packages/agent/test/harness/runtime/progress.test.ts:195
	t.Run("enqueues assistant frames in order, seals admission, and exposes the settlement delete", func(t *testing.T) {
		lane, drive, _ := progressFixture(t, progressAssistantState("response"))
		progress := OpenFrameProgress(lane, drive, "response")
		frames := []ai.AssistantMessageFrame{ai.TextDeltaFrame{ContentIndex: 0, Delta: "a"}, ai.TextDeltaFrame{ContentIndex: 0, Delta: "b"}}
		for _, frame := range frames {
			progress.Write(frame)
		}
		progress.Seal()
		progress.Write(ai.TextDeltaFrame{ContentIndex: 0, Delta: "late"})
		requireHarnessOK(t, progress.Drain())
		stored, err := session.ReadList(ctx, lane.Session, session.PendingAssistantFrames("operation", "response"), nil)
		requireHarnessOK(t, err)
		got := []ai.AssistantMessageFrame{}
		for _, element := range stored {
			got = append(got, element.Value)
		}
		requireHarnessEqual(t, got, frames)
	})
	// upstream: packages/agent/test/harness/runtime/progress.test.ts:221,264
	for _, tc := range []struct {
		title    string
		terminal bool
	}{
		{"declines a queued frame after the authoritative projection leaves its phase without control reads", false},
		{"declines a queued frame after terminal projection publication", true},
	} {
		t.Run(tc.title, func(t *testing.T) {
			lane, drive, storage := progressFixture(t, progressAssistantState("response"))
			progress := OpenFrameProgress(lane, drive, "response")
			started, release := make(chan struct{}), make(chan struct{})
			releaseCommit := sync.OnceFunc(func() { close(release) })
			t.Cleanup(releaseCommit)
			storage.beforeNextCommit = func() { close(started); <-release }
			moving, err := lane.CommandAsync(ctx, func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
				next := state
				var writes []session.Write
				if tc.terminal {
					next.Operation = nil
					writes = []session.Write{session.DeleteValue(session.OperationMetaValue("operation")), session.DeleteValue(session.OperationStateValue("operation")), session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: state.Inbox})}
				} else {
					if state.Operation == nil {
						return LaneCommand[any]{}, errors.New("missing operation")
					}
					nextState := session.OperationState{OperationScope: session.OperationScopeOf(state.Operation.State), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: "may_finish", IncludeFinalAssistant: true}, TriggerEntryID: "response"}
					next.Operation = &session.Operation{Meta: state.Operation.Meta, State: nextState}
					writes = []session.Write{session.SetValue(session.OperationStateValue("operation"), nextState)}
				}
				return LaneCommand[any]{Kind: CommandCommit, Writes: writes, Next: next, Materialize: func(session.CommitResult) any { return nil }}, nil
			})
			requireHarnessOK(t, err)
			<-started
			progress.Write(ai.TextDeltaFrame{ContentIndex: 0, Delta: "late"})
			releaseCommit()
			_, err = moving.Wait()
			requireHarnessOK(t, err)
			requireHarnessOK(t, progress.Drain())
			stored, err := session.ReadList(ctx, lane.Session, session.PendingAssistantFrames("operation", "response"), nil)
			requireHarnessOK(t, err)
			requireHarnessEqual(t, stored, []session.ListElement[ai.AssistantMessageFrame]{})
			requireHarnessEqual(t, len(storage.reads), 0)
		})
	}
	// upstream: packages/agent/test/harness/runtime/progress.test.ts:308
	t.Run("replaces tool checkpoints in invocation order", func(t *testing.T) {
		state := session.OperationState{OperationScope: restoreScope(), At: session.AtTools, Batch: session.ToolBatch{AssistantEntryID: "assistant", Configuration: laneTestConfiguration(), TurnID: "turn", Calls: []session.ToolCall{{Status: "effect_pending", SourceIndex: 0, ResultEntryID: "result", Replay: "safe"}}}}
		lane, drive, _ := progressFixture(t, state)
		progress := OpenToolProgress(lane, drive, "turn", 0, "result")
		progress.Write(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "first"}}, Details: map[string]any{}})
		progress.Write(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "second"}}, Details: map[string]any{}})
		requireHarnessOK(t, progress.Drain())
		stored, err := session.GetValue(ctx, lane.Session, session.PendingToolOutput("operation", "result"))
		requireHarnessOK(t, err)
		if stored == nil {
			t.Fatal("missing tool checkpoint")
		}
		requireHarnessEqual(t, stored.Value.Content, []ai.ToolResultMessageContent{ai.TextContent{Text: "second"}})
	})
	// upstream: packages/agent/test/harness/runtime/progress.test.ts:330
	t.Run("retains the rejecting write promise so drain propagates commit failure", func(t *testing.T) {
		lane, drive, storage := progressFixture(t, progressAssistantState("response"))
		failure := errors.New("frame commit failed")
		storage.failure = failure
		progress := OpenFrameProgress(lane, drive, "response")
		progress.Write(ai.TextDeltaFrame{ContentIndex: 0, Delta: "lost"})
		if err := progress.Drain(); err != failure { //nolint:errorlint // Upstream rejects with the identical storage error.
			t.Fatalf("drain error = %v, want the original %v", err, failure)
		}
	})
}
