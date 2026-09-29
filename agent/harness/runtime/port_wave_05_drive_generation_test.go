package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

const generationOperationID = "01950000-0000-7000-8000-000000000001"

type generationFixture struct {
	*procedureFixture
	faux laneFaux
}

func newGenerationFixture(t *testing.T, backend session.Storage, options ...laneFauxOptions) *generationFixture {
	t.Helper()
	fauxOptions := laneFauxOptions{MinTokenSize: 1, MaxTokenSize: 1}
	if len(options) != 0 {
		fauxOptions.PendingFetches = options[0].PendingFetches
	}
	f := &generationFixture{procedureFixture: newProcedureFixture(t, backend), faux: newLaneFauxWithOptions(fauxOptions)}
	f.models, f.model = f.faux.models, f.faux.model
	f.provider = f.models.GetProvider("faux")
	configuration := session.LaneConfiguration{Model: session.ModelRef{Provider: f.model.ProviderMeta.ProviderID, ModelID: f.model.ID}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{}}
	f.installLane(t, "", []session.Write{session.SetValue(session.BranchTip("main"), (*string)(nil)), session.SetValue(session.LaneConfig("main"), configuration), session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{}})})
	_, err := f.lane.Accept(t.Context(), agentharness.OperationRequest{Kind: agentharness.RequestPrompt, OperationID: new(generationOperationID), PromptText: new("question")})
	require.NoError(t, err)
	f.drive = NewDrive(t.Context(), agentharness.DriveOptions{OperationID: generationOperationID})
	f.lane.ActiveDrive = f.drive
	f.storage.ClearCommitAttempts()
	return f
}

func (f *generationFixture) start(t *testing.T) ProcedureResult {
	t.Helper()
	run := f.currentRun(t)
	require.Equal(t, session.AtStarting, run.At)
	result, err := StartRun(t.Context(), f.lane, f.drive, run)
	require.NoError(t, err)
	return result
}
func (f *generationFixture) ready(t *testing.T) session.OperationState {
	t.Helper()
	f.start(t)
	checkpoint := f.currentRun(t)
	require.Equal(t, session.AtCheckpoint, checkpoint.At)
	_, err := RunCheckpoint(t.Context(), f.lane, f.drive, checkpoint)
	require.NoError(t, err)
	ready := f.currentRun(t)
	require.Equal(t, session.AtAssistantReady, ready.At)
	return ready
}
func (f *generationFixture) response(text string, timestamp int64) {
	f.faux.setResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop", Timestamp: &timestamp})})
}

type pendingFixtureEntry struct {
	id, kind string
	entry    session.PendingEntry
}

func (f *generationFixture) enqueue(t *testing.T, next *session.OperationState, pending ...pendingFixtureEntry) {
	t.Helper()
	_, err := f.lane.Command(t.Context(), func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
		inbox := append([]session.InboxItem{}, state.Inbox...)
		writes := []session.Write{}
		for _, value := range pending {
			inbox = append(inbox, session.InboxItem{EntryID: value.id, Kind: value.kind})
			writes = append(writes, session.SetValue(session.PendingEntryValue(value.id), value.entry))
		}
		if next != nil {
			operation := *state.Operation
			operation.State = *next
			state.Operation = &operation
			writes = append(writes, session.SetValue(session.OperationStateValue(generationOperationID), *next))
		}
		writes = append(writes, session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: new(generationOperationID), LastOperationID: state.LastOperationID, Inbox: inbox}))
		state.Inbox = inbox
		return LaneCommand[any]{Kind: CommandCommit, Writes: writes, Next: state, Materialize: func(session.CommitResult) any { return nil }}, nil
	})
	require.NoError(t, err)
}

func writesOperationState(writes []session.Write, at session.OperationAt) bool {
	for _, write := range writes {
		if value, ok := write.(session.ValueSetWrite); ok && value.Namespace == "pi.op.state" {
			if state, ok := value.Value.(session.OperationState); ok && state.At == at {
				return true
			}
		}
	}
	return false
}
func writeKinds(writes []session.Write, namespace bool) []string {
	kinds := make([]string, 0, len(writes))
	for _, write := range writes {
		var kind, ns string
		switch value := write.(type) {
		case session.EntryWrite:
			kind = "entry"
		case session.UsageWrite:
			kind = "usage"
		case session.ValueSetWrite:
			kind, ns = "value:set", value.Namespace
		case session.ValueDeleteWrite:
			kind, ns = "value:delete", value.Namespace
		case session.ListAppendWrite:
			kind, ns = "list:append", value.Namespace
		case session.ListDeleteWrite:
			kind, ns = "list:delete", value.Namespace
		}
		if namespace && ns != "" {
			kind += ":" + ns
		}
		kinds = append(kinds, kind)
	}
	return kinds
}
func requireNoEntryOrUsage(t *testing.T, f *procedureFixture) {
	t.Helper()
	for _, writes := range f.storage.GetCommitAttempts() {
		for _, write := range writes {
			switch write.(type) {
			case session.EntryWrite, session.UsageWrite:
				t.Fatalf("unexpected response reservation settlement write: %#v", write)
			}
		}
	}
}
func requireEmptyFrames(t *testing.T, f *procedureFixture, id string) {
	t.Helper()
	frames, err := session.ReadList(t.Context(), f.session, session.PendingAssistantFrames(f.drive.OperationID, id), nil)
	require.NoError(t, err)
	require.Empty(t, frames)
}

type frameBlockingMemoryStorage struct {
	*session.MemoryStorage
	blockNext atomic.Bool
	started   chan struct{}
	release   chan struct{}
}

func (storage *frameBlockingMemoryStorage) Commit(ctx context.Context, writes []session.Write) (session.CommitResult, error) {
	for _, write := range writes {
		if value, ok := write.(session.ListAppendWrite); ok && value.Namespace == "pi.pending.assistant_frame" && storage.blockNext.CompareAndSwap(true, false) {
			close(storage.started)
			<-storage.release
			break
		}
	}
	return storage.MemoryStorage.Commit(ctx, writes)
}

func TestPortWave05Generation(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:200
	t.Run("consumes before_run and snapshots a ready generation", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		_, err := f.hooks.OnBeforeRun(func(context.Context, agentharness.BeforeRunEvent) (*agentharness.BeforeRunResult, error) {
			return &agentharness.BeforeRunResult{Messages: []agent.AgentMessage{laneUser("injected", 2)}}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, f.start(t))
		run := f.currentRun(t)
		require.Equal(t, session.AtCheckpoint, run.At)
		require.Equal(t, *f.lane.SnapshotState().TipID, run.TriggerEntryID)
		require.Contains(t, f.eventTypes(), agentharness.EventEntryAdded)
		f.requireRestores(t)
		result, err := RunCheckpoint(t.Context(), f.lane, f.drive, run)
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		run = f.currentRun(t)
		require.Equal(t, session.AtAssistantReady, run.At)
		require.Equal(t, f.lane.SnapshotState().Configuration, run.GenerationContext.Configuration)
		require.Empty(t, jsonObject(t, run.GenerationContext.StreamOptions))
		require.Equal(t, session.NormalizedRetryPolicy{MaxAttempts: 4, BaseDelayMs: 1, MaxAgentDelayMs: 60000}, run.GenerationContext.RetryPolicy)
		require.False(t, run.GenerationContext.OverflowRecoveryUsed)
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:226
	t.Run("drains mixed pending writes while separating leaf and generation trigger", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		f.start(t)
		messageID, customID := "01950000-0000-7000-8000-000000000010", "01950000-0000-7000-8000-000000000011"
		f.enqueue(t, nil, pendingFixtureEntry{id: messageID, kind: "write", entry: session.PendingEntry{Type: "message", Message: laneUser("projecting", 3)}}, pendingFixtureEntry{id: customID, kind: "write", entry: session.PendingEntry{Type: "custom", CustomType: "display-only", CustomPayload: new(any(map[string]any{"value": true}))}})
		run := f.currentRun(t)
		require.Equal(t, session.AtCheckpoint, run.At)
		f.storage.ClearCommitAttempts()
		_, err := RunCheckpoint(t.Context(), f.lane, f.drive, run)
		require.NoError(t, err)
		require.Len(t, f.storage.GetCommitAttempts(), 1)
		ready := f.currentRun(t)
		require.Equal(t, session.AtAssistantReady, ready.At)
		require.Equal(t, customID, *f.lane.SnapshotState().TipID)
		require.Equal(t, messageID, ready.GenerationContext.TriggerEntryID)
		require.False(t, ready.GenerationContext.OverflowRecoveryUsed)
		require.Empty(t, f.lane.SnapshotState().Inbox)
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:279
	t.Run("preserves checkpoint routing when a custom-only pending write does not project", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		f.start(t)
		checkpoint := f.currentRun(t)
		require.Equal(t, session.AtCheckpoint, checkpoint.At)
		checkpoint.Continuation = session.Continuation{Kind: "may_finish", IncludeFinalAssistant: false}
		customID := "01950000-0000-7000-8000-000000000012"
		f.enqueue(t, &checkpoint, pendingFixtureEntry{id: customID, kind: "write", entry: session.PendingEntry{Type: "custom", CustomType: "display-only", CustomPayload: new(any(map[string]any{"value": true}))}})
		_, err := f.hooks.OnBeforeRunEnd(func(context.Context, agentharness.BeforeRunEndEvent) (*agentharness.BeforeRunEndResult, error) {
			return &agentharness.BeforeRunEndResult{FollowUp: new("continue after custom write")}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		result, err := RunCheckpoint(t.Context(), f.lane, f.drive, checkpoint)
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		ready := f.currentRun(t)
		require.Equal(t, session.AtAssistantReady, ready.At)
		followUpID := ready.GenerationContext.TriggerEntryID
		require.Equal(t, followUpID, *f.lane.SnapshotState().TipID)
		require.Equal(t, session.EntryTypeCustom, requireEntry(t, f.procedureFixture, customID).Type)
		entry := requireEntry(t, f.procedureFixture, followUpID)
		require.Equal(t, customID, *entry.ParentID)
		require.Equal(t, session.EntryTypeMessage, entry.Type)
		require.NotNil(t, entry.Message.User)
		require.Equal(t, "user", entry.Message.Role())
		require.Equal(t, ai.UserContentBlocks{ai.TextContent{Text: "continue after custom write"}}, entry.Message.User.Content)
		require.Empty(t, f.lane.SnapshotState().Inbox)
		requireNoValue(t, f.procedureFixture, session.PendingEntryValue(customID))
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:331
	t.Run("drops a stale finish-hook follow-up when input arrives during the hook", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		f.start(t)
		checkpoint := f.currentRun(t)
		checkpoint.Continuation = session.Continuation{Kind: "may_finish", IncludeFinalAssistant: false}
		f.replaceRun(t, checkpoint)
		started, release := laneTestBarrier(t)
		_, err := f.hooks.OnBeforeRunEnd(func(context.Context, agentharness.BeforeRunEndEvent) (*agentharness.BeforeRunEndResult, error) {
			close(started)
			<-release.done
			return &agentharness.BeforeRunEndResult{FollowUp: new("stale hook follow-up")}, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		var result ProcedureResult
		done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) {
			value, err := RunCheckpoint(t.Context(), f.lane, f.drive, checkpoint)
			result = value
			return value, err
		}, release.open)
		<-started
		f.enqueue(t, nil, pendingFixtureEntry{id: "steer-during-finish", kind: "steer", entry: session.PendingEntry{Type: "message", Message: laneUser("new input", 4)}})
		release.open()
		require.NoError(t, done())
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		ready := f.currentRun(t)
		require.Equal(t, session.AtAssistantReady, ready.At)
		require.Equal(t, "steer-during-finish", ready.GenerationContext.TriggerEntryID)
		require.Equal(t, "steer-during-finish", *f.lane.SnapshotState().TipID)
		require.Equal(t, session.EntryTypeMessage, requireEntry(t, f.procedureFixture, "steer-during-finish").Type)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:391
	t.Run("commits intent before provider admission, preserves queued inbox state, and settles reserved ids", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		ready := f.ready(t)
		started, release := laneTestBarrier(t)
		_, err := f.hooks.OnBeforeRequest(func(context.Context, agentharness.BeforeRequestEvent) (*agentharness.BeforeRequestResult, error) {
			close(started)
			<-release.done
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		var effectPending atomic.Bool
		f.faux.setResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.FauxResponse, error) {
			stored, err := session.GetValue(t.Context(), f.session, session.OperationStateValue(generationOperationID))
			effectPending.Store(err == nil && stored != nil && stored.Value.At == session.AtAssistantEffectPending)
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("answer")}, StopReason: "stop", Timestamp: new(int64(5))}, nil
		})})
		f.storage.ClearCommitAttempts()
		var result ProcedureResult
		done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) {
			value, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
			result = value
			return value, err
		}, release.open)
		<-started
		steerID := "01950000-0000-7000-8000-000000000020"
		f.enqueue(t, nil, pendingFixtureEntry{id: steerID, kind: "steer", entry: session.PendingEntry{Type: "message", Message: laneUser("late steer", 4)}})
		release.open()
		require.NoError(t, done())
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
		require.True(t, effectPending.Load())
		settled := f.currentRun(t)
		require.Equal(t, []session.InboxItem{{EntryID: steerID, Kind: "steer"}}, f.lane.SnapshotState().Inbox)
		require.Equal(t, session.AtCheckpoint, settled.At)
		require.NotNil(t, settled.LatestAssistantEntryID)
		responseID := *settled.LatestAssistantEntryID
		entry := requireEntry(t, f.procedureFixture, responseID)
		require.Equal(t, responseID, entry.ID)
		require.Equal(t, session.EntryTypeMessage, entry.Type)
		require.NotNil(t, entry.Message.Assistant)
		require.Equal(t, []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, entry.Message.Assistant.Content)
		require.Equal(t, int64(5), entry.Message.Assistant.Timestamp)
		requireEmptyFrames(t, f.procedureFixture, responseID)
		intent, settlement := false, []session.Write(nil)
		for _, writes := range f.storage.GetCommitAttempts() {
			intent = intent || writesOperationState(writes, session.AtAssistantEffectPending)
			for _, write := range writes {
				if _, ok := write.(session.EntryWrite); ok {
					settlement = writes
					break
				}
			}
		}
		require.True(t, intent)
		require.Equal(t, []string{"entry", "usage", "value:set:pi.branch.tip", "list:delete:pi.pending.assistant_frame", "value:set:pi.op.state"}, writeKinds(settlement, true))
		for _, kind := range []agentharness.HarnessEventType{agentharness.EventTurnStart, agentharness.EventMessageStart, agentharness.EventMessageUpdate, agentharness.EventMessageEnd, agentharness.EventEntryAdded, agentharness.EventUsage, agentharness.EventTurnEnd} {
			require.Contains(t, f.eventTypes(), kind)
		}
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:482
	t.Run("does not await frame storage inside the provider event loop and persists frames in order", func(t *testing.T) {
		backend := &frameBlockingMemoryStorage{MemoryStorage: session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 100 }}), started: make(chan struct{}), release: make(chan struct{})}
		release := sync.OnceFunc(func() { close(backend.release) })
		t.Cleanup(release)
		f := newGenerationFixture(t, backend)
		ready := f.ready(t)
		provider := *f.provider
		provider.StreamSimple = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			stream := ai.NewAssistantMessageEventStream()
			partial := &ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonPending, Timestamp: 5}
			events := []ai.AssistantMessageEvent{ai.StartEvent{Partial: partial}, ai.TextStartEvent{ContentIndex: 0, Partial: partial}, ai.TextDeltaEvent{ContentIndex: 0, Delta: "ab", Partial: partial}, ai.TextEndEvent{ContentIndex: 0, Content: "ab", Partial: partial}}
			partial.Content = append(partial.Content, ai.TextContent{Text: "ab"})
			for _, event := range events {
				if err := stream.Push(event); err != nil {
					return nil, err
				}
			}
			final := *partial
			final.StopReason = ai.StopReasonStop
			if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: &final}); err != nil {
				return nil, err
			}
			stream.End(&final)
			return stream, nil
		}
		f.models.SetProvider(&provider)
		backend.blockNext.Store(true)
		f.storage.ClearCommitAttempts()
		updates := make(chan struct{})
		var count atomic.Int32
		f.onEmit = func(events []agentharness.HarnessEvent) error {
			for _, event := range events {
				if event.Type() == agentharness.EventMessageUpdate && count.Add(1) == 2 {
					close(updates)
				}
			}
			return nil
		}
		done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) { return RunGeneration(t.Context(), f.lane, f.drive, ready) }, release)
		<-backend.started
		<-updates
		require.Greater(t, count.Load(), int32(1))
		release()
		require.NoError(t, done())
		frames := []ai.AssistantMessageFrame{}
		for _, writes := range f.storage.GetCommitAttempts() {
			for _, write := range writes {
				if value, ok := write.(session.ListAppendWrite); ok && value.Namespace == "pi.pending.assistant_frame" {
					frames = append(frames, value.Value.(ai.AssistantMessageFrame))
				}
			}
		}
		types := make([]ai.AssistantMessageFrameType, len(frames))
		for i, frame := range frames {
			types[i] = frame.FrameType()
		}
		require.Equal(t, []ai.AssistantMessageFrameType{ai.FrameTypeStart, ai.FrameTypeTextStart, ai.FrameTypeTextEnd}, types)
		require.Empty(t, frames[0].(ai.StartFrame).Partial.Content)
		require.Equal(t, "ab", frames[1].(ai.TextStartFrame).Content.Text)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:542
	t.Run("enters configuration failure without reserving response ids or calling the provider", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		require.NoError(t, f.lane.SetActiveTools(t.Context(), []string{"missing"}))
		ready := f.ready(t)
		queuedID, err := f.lane.AppendCustomEntry(t.Context(), "after-failure", new(any(map[string]any{"retained": true})))
		require.NoError(t, err)
		f.storage.ClearCommitAttempts()
		result, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, result.Kind)
		require.Equal(t, generationOperationID, result.Record.OperationID)
		require.Equal(t, "run", result.Record.Kind)
		require.Equal(t, "failed", result.Record.Status)
		require.NotNil(t, result.Record.Error)
		require.Equal(t, "configured_tools_unavailable", result.Record.Error.Code)
		require.Equal(t, map[string]any{"tools": []string{"missing"}}, *result.Record.Error.Details)
		require.Nil(t, f.lane.SnapshotState().Operation)
		require.Equal(t, []session.InboxItem{{EntryID: queuedID, Kind: "write"}}, f.lane.SnapshotState().Inbox)
		pending, err := session.GetValue(t.Context(), f.session, session.PendingEntryValue(queuedID))
		require.NoError(t, err)
		require.NotNil(t, pending)
		require.Zero(t, f.faux.callCount())
		requireNoEntryOrUsage(t, f.procedureFixture)
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:571
	t.Run("fails a missing captured model before reserving ids", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		ready := f.ready(t)
		f.models.DeleteProvider(f.provider.ID)
		f.storage.ClearCommitAttempts()
		result, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, result.Kind)
		require.Equal(t, generationOperationID, result.Record.OperationID)
		require.Equal(t, "run", result.Record.Kind)
		require.Equal(t, "failed", result.Record.Status)
		require.NotNil(t, result.Record.Error)
		require.Equal(t, "model_unavailable", result.Record.Error.Code)
		require.Nil(t, f.lane.SnapshotState().Operation)
		require.Zero(t, f.faux.callCount())
		requireNoEntryOrUsage(t, f.procedureFixture)
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:592
	t.Run("declines intent when cancellation wins preparation", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		ready := f.ready(t)
		started, release := laneTestBarrier(t)
		_, err := f.hooks.OnBeforeRequest(func(context.Context, agentharness.BeforeRequestEvent) (*agentharness.BeforeRequestResult, error) {
			close(started)
			<-release.done
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		f.storage.ClearCommitAttempts()
		var result ProcedureResult
		done := runProcedureAsync(t, f.procedureFixture, func() (ProcedureResult, error) {
			value, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
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
		require.Zero(t, f.faux.callCount())
		for _, writes := range f.storage.GetCommitAttempts() {
			require.False(t, writesOperationState(writes, session.AtAssistantEffectPending))
		}
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:627
	t.Run("recovers an orphan with no frames into a zero-usage retry wait", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		ready := f.ready(t)
		responseID, usageID := "01950000-0000-7000-8000-000000000028", "01950000-0000-7000-8000-000000000029"
		pending := session.OperationState{OperationScope: ready.OperationScope, At: session.AtAssistantEffectPending, GenerationContext: ready.GenerationContext, Attempt: 1, ResponseEntryID: responseID, UsageID: usageID, IntendedOutputLimit: 100, ContextWindow: 1000}
		pending.GenerationContext.RetryPolicy = session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 1, MaxAgentDelayMs: 30000}
		f.replaceRun(t, pending)
		_, err := RecoverAssistantGeneration(t.Context(), f.lane, f.drive, pending)
		require.NoError(t, err)
		message := requireEntry(t, f.procedureFixture, responseID).Message.Assistant
		require.NotNil(t, message)
		require.Equal(t, ai.API("unknown"), message.API)
		require.Equal(t, ready.GenerationContext.Configuration.Model.Provider, message.Provider)
		require.Equal(t, ready.GenerationContext.Configuration.Model.ModelID, message.ModelID)
		require.Empty(t, message.Content)
		require.Equal(t, ai.StopReasonError, message.StopReason)
		require.NotNil(t, message.Usage)
		require.Zero(t, message.Usage.TotalTokens)
		run := f.currentRun(t)
		require.Equal(t, session.AtAssistantRetryWait, run.At)
		require.Equal(t, 2, run.NextAttempt)
		require.Zero(t, f.faux.callCount())
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:675
	t.Run("recovers an orphan from committed frames without a provider or response hook", func(t *testing.T) {
		f := newGenerationFixture(t, nil)
		ready := f.ready(t)
		responseID, usageID := "01950000-0000-7000-8000-000000000030", "01950000-0000-7000-8000-000000000031"
		pending := session.OperationState{OperationScope: ready.OperationScope, At: session.AtAssistantEffectPending, GenerationContext: ready.GenerationContext, Attempt: 1, ResponseEntryID: responseID, UsageID: usageID, IntendedOutputLimit: 100, ContextWindow: 1000}
		pending.GenerationContext.RetryPolicy = session.NormalizedRetryPolicy{MaxAttempts: 1, BaseDelayMs: 1, MaxAgentDelayMs: 30000}
		address := session.PendingAssistantFrames(generationOperationID, responseID)
		frames := []ai.AssistantMessageFrame{ai.StartFrame{Partial: ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonPending, Timestamp: 6}}, ai.TextStartFrame{ContentIndex: 0, Content: ai.TextContent{Text: ""}}, ai.TextDeltaFrame{ContentIndex: 0, Delta: "draft"}, ai.TextEndFrame{ContentIndex: 0, Content: "corrected"}}
		writes := []session.Write{session.SetValue(session.OperationStateValue(generationOperationID), pending)}
		for _, frame := range frames {
			writes = append(writes, session.AppendList(address, frame))
		}
		_, err := f.lane.Command(t.Context(), func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
			operation := *state.Operation
			operation.State = pending
			state.Operation = &operation
			return LaneCommand[any]{Kind: CommandCommit, Writes: writes, Next: state, Materialize: func(session.CommitResult) any { return nil }}, nil
		})
		require.NoError(t, err)
		var afterCalls atomic.Int32
		_, err = f.hooks.OnAfterResponse(func(context.Context, agentharness.AfterResponseEvent) (*agentharness.AfterResponseResult, error) {
			afterCalls.Add(1)
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		result, err := RecoverAssistantGeneration(t.Context(), f.lane, f.drive, pending)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, result.Kind)
		require.Equal(t, generationOperationID, result.Record.OperationID)
		require.Equal(t, "run", result.Record.Kind)
		require.Equal(t, "failed", result.Record.Status)
		require.Equal(t, responseID, *result.Record.TipID)
		require.Zero(t, f.faux.callCount())
		require.Zero(t, afterCalls.Load())
		message := requireEntry(t, f.procedureFixture, responseID).Message.Assistant
		require.NotNil(t, message)
		require.Equal(t, "assistant", message.Role)
		require.Equal(t, []ai.AssistantContentBlock{ai.TextContent{Text: "corrected"}}, message.Content)
		require.Equal(t, ai.StopReasonError, message.StopReason)
		require.NotNil(t, message.Usage)
		require.Zero(t, message.Usage.Input)
		require.Zero(t, message.Usage.Output)
		require.Nil(t, f.lane.SnapshotState().Operation)
		requireEmptyFrames(t, f.procedureFixture, responseID)
		types := []agentharness.HarnessEventType{}
		for _, event := range f.eventSnapshot() {
			if event.Recovery {
				types = append(types, event.Type())
			}
		}
		require.Equal(t, []agentharness.HarnessEventType{agentharness.EventMessageStart, agentharness.EventMessageEnd, agentharness.EventEntryAdded}, types)
		f.requireRestores(t)
	})
	// upstream: packages/agent/test/harness/runtime/drive-generation.test.ts:762
	t.Run("finishes the no-tool run with terminal cleanup and an immutable result record", func(t *testing.T) {
		backend := &countingEntryStorage{MemoryStorage: session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 100 }})}
		f := newGenerationFixture(t, backend)
		ready := f.ready(t)
		f.response("done", 7)
		_, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
		require.NoError(t, err)
		run := f.currentRun(t)
		require.Equal(t, session.AtCheckpoint, run.At)
		result, err := RunCheckpoint(t.Context(), f.lane, f.drive, run)
		require.NoError(t, err)
		require.Equal(t, ProcedureSettled, result.Kind)
		require.Equal(t, generationOperationID, result.Record.OperationID)
		require.Equal(t, "run", result.Record.Kind)
		require.Equal(t, "completed", result.Record.Status)
		require.Equal(t, f.lane.SnapshotState().TipID, result.Record.TipID)
		require.Nil(t, f.lane.SnapshotState().Operation)
		require.Equal(t, generationOperationID, *f.lane.SnapshotState().LastOperationID)
		backend.reads.Store(0)
		record, err := f.lane.GetResult(t.Context(), generationOperationID)
		require.NoError(t, err)
		require.NotNil(t, record)
		require.Equal(t, generationOperationID, record.OperationID)
		require.Equal(t, "run", record.Kind)
		require.Equal(t, "completed", record.Status)
		require.Equal(t, f.lane.SnapshotState().TipID, record.TipID)
		unknown, err := f.lane.GetResult(t.Context(), "unknown")
		require.NoError(t, err)
		require.Nil(t, unknown)
		require.Zero(t, backend.reads.Load())
		requireNoValue(t, f.procedureFixture, session.OperationMetaValue(generationOperationID))
		requireNoValue(t, f.procedureFixture, session.OperationStateValue(generationOperationID))
		require.Len(t, f.eventsOf(agentharness.EventRunEnd), 1)
		f.requireRestores(t)
	})
}

// upstream: packages/agent/src/harness/runtime/drive/generation.ts:81-129
func TestPortWave05GenerationRetainsProviderConverterSnapshot(t *testing.T) {
	f := newGenerationFixture(t, nil)
	var initialCalls, replacementCalls atomic.Int32
	f.config.ToProviderMessages = func(_ context.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
		initialCalls.Add(1)
		return harness.ConvertToLlm(messages), nil
	}
	generating := false
	f.config.EntryProjectors["change-config"] = func(context.Context, session.Entry) ([]agent.AgentMessage, error) {
		if generating {
			f.config.ToProviderMessages = func(context.Context, []agent.AgentMessage) ([]ai.Message, error) {
				replacementCalls.Add(1)
				return nil, errors.New("replacement converter must not enter the captured request")
			}
		}
		return nil, nil
	}
	_, err := f.lane.AppendCustomEntry(t.Context(), "change-config", nil)
	require.NoError(t, err)
	ready := f.ready(t)
	f.response("answer", 5)
	generating = true
	result, err := RunGeneration(t.Context(), f.lane, f.drive, ready)
	require.NoError(t, err)
	require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
	require.Equal(t, int32(1), initialCalls.Load())
	require.Zero(t, replacementCalls.Load())
}

type countingEntryStorage struct {
	*session.MemoryStorage
	reads atomic.Int32
}

func (storage *countingEntryStorage) GetEntries(ctx context.Context, ids []string) (map[string]session.Entry, error) {
	storage.reads.Add(1)
	return storage.MemoryStorage.GetEntries(ctx, ids)
}
