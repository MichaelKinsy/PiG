package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/ai"
)

const driveProcedureOperationID = "01950000-0000-7000-8000-000000000001"

type driveProcedureFixture struct {
	lane          *Lane
	drive         *Drive
	session       session.Session
	storage       *sessiontesting.InstrumentedStorage
	models        *ai.Models
	provider      *ai.ModelsProvider
	model         *ai.Model
	configuration session.LaneConfiguration
	config        Config
	hooks         *agentharness.HookRegistry
	setResponses  func([]ai.FauxResponseStep)
	callCount     func() int
	mu            sync.Mutex
	published     []agentharness.HarnessEvent
	cancelled     []ai.DeferredHandle
}

// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:139-208; drive-reconcile.test.ts:98-166.
func newDriveProcedureFixture(t *testing.T, structural bool) *driveProcedureFixture {
	t.Helper()
	isolateHarnessTest(t)
	storage := sessiontesting.NewInstrumentedStorage(session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 100 }}))
	opened := session.NewStorageBackedSession(session.SessionMetadata{ID: t.Name(), CreatedAt: 1, StorageVersion: 1}, storage, nil)
	t.Cleanup(func() { require.NoError(t, opened.Close(context.Background())) })
	options := ai.FauxConfig{}
	if structural {
		options.MinTokenSize = 1
		options.MaxTokenSize = 1
	}
	faux := ai.NewFauxProvider(options)
	t.Cleanup(func() { require.NoError(t, faux.Close()) })
	model := faux.GetModel()
	fixture := &driveProcedureFixture{session: opened, storage: storage, model: model, setResponses: faux.SetResponses, callCount: faux.CallCount}
	// The upstream faux provider replaces network I/O. Bind the existing Go faux stream to the real Models collection; cancellation records the same external mock call.
	stream := func(ctx context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return faux.Stream(ctx, transcript, options)
	}
	fixture.provider = ai.CreateProvider(ai.CreateProviderOptions{ID: "faux", Models: []ai.AnyModel{model}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Faux", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}}, API: &ai.ProviderStreams{Stream: stream, StreamSimple: stream, CancelDeferred: func(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ai.DeferredCancelOptions) error {
		fixture.mu.Lock()
		fixture.cancelled = append(fixture.cancelled, handle)
		fixture.mu.Unlock()
		if options.OnResponse != nil {
			return options.OnResponse(ctx, ai.ProviderResponse{Status: 200, Headers: map[string]string{}}, model)
		}
		return nil
	}}})
	fixture.models = ai.CreateModels()
	fixture.models.SetProvider(fixture.provider)
	fixture.configuration = session.LaneConfiguration{Model: session.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelID: model.ID}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{}}
	harnessCommit(t, opened, []session.Write{session.SetValue(session.BranchTip("main"), (*string)(nil)), session.SetValue(session.LaneConfig("main"), fixture.configuration), session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{}})}...)
	fixture.hooks = agentharness.NewHookRegistry(func(harness.Context, error, agentharness.HookName, string) error { return nil })
	fixture.config = Config{Tools: []harness.AgentHarnessTool{}, Resources: agentharness.Resources{}, StreamOptions: harness.AgentHarnessStreamOptions{}, RetryPolicy: ai.RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 10}, Compaction: compaction.DefaultCompactionSettings, SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: agent.ToolModeParallel, EntryProjectors: map[string]session.EntryProjector{}, ToProviderMessages: func(_ harness.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
		filtered := []agent.AgentMessage{}
		for _, message := range messages {
			if message.Role() == agent.RoleUser || message.Role() == agent.RoleAssistant || message.Role() == agent.RoleToolResult {
				filtered = append(filtered, message)
			}
		}
		return harness.ConvertToLlm(filtered), nil
	}}
	state, err := RestoreLane(context.Background(), opened, "main")
	require.NoError(t, err)
	fixture.lane = NewLane("main", opened, fixture.models, fixture.hooks, state, func(_ harness.Context, cause error) error { return cause }, func(_ harness.Context, batch []agentharness.HarnessEvent) (func() error, error) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		for _, event := range batch {
			fixture.published = append(fixture.published, agentharness.CloneHarnessEvent(event))
		}
		return nil, nil
	}, func(harness.Context, agentharness.LaneSnapshot, func(agentharness.HarnessEvent) bool, agentharness.ResnapshotCapture[agentharness.LaneSnapshot]) (agentharness.WatchHandle[agentharness.LaneSnapshot], error) {
		return nil, errors.New("watch is not used by structural or reconciliation tests")
	}, func() Config { return fixture.config })
	fixture.drive = NewDrive(context.Background(), agentharness.DriveOptions{OperationID: driveProcedureOperationID})
	fixture.lane.ActiveDrive = fixture.drive
	t.Cleanup(func() { fixture.drive.CloseGate(errors.New("fixture closed")) })
	storage.ClearCommitAttempts()
	return fixture
}

func (fixture *driveProcedureFixture) events() []agentharness.HarnessEvent {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return slices.Clone(fixture.published)
}
func (fixture *driveProcedureFixture) eventTypes() []agentharness.HarnessEventType {
	events := fixture.events()
	types := make([]agentharness.HarnessEventType, len(events))
	for i, event := range events {
		types[i] = event.Type()
	}
	return types
}
func (fixture *driveProcedureFixture) cancelledDeferred() []ai.DeferredHandle {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return slices.Clone(fixture.cancelled)
}
func (fixture *driveProcedureFixture) state(t *testing.T) session.OperationState {
	t.Helper()
	operation := fixture.lane.SnapshotState().Operation
	require.NotNil(t, operation)
	return operation.State
}

func driveTestScope(settings ...harness.CompactionSettings) session.OperationScope {
	compactionSettings := compaction.DefaultCompactionSettings
	if len(settings) > 0 {
		compactionSettings = settings[0]
	}
	return session.OperationScope{Control: session.Control{Status: session.ControlRunning}, Settings: session.RunSettings{Compaction: compactionSettings, SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: session.ToolExecutionParallel}}
}
func driveTestAssistant(text string) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "faux", Provider: "faux", ModelID: "faux-1", Usage: &ai.Usage{}, StopReason: ai.StopReasonStop, Timestamp: runtimeNow()}}
}
func driveTestEntry(id string, parent *string, text string) session.Entry {
	return session.Entry{ID: id, ParentID: parent, Type: session.EntryTypeMessage, Message: laneUser(text, 1)}
}
func driveTestResponse(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}
func driveTestError(message string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("")}, StopReason: "error", ErrorMessage: message})
}
func driveTestCompactionTask() session.SummaryTask {
	return session.SummaryTask{TaskID: "task", Reason: "manual", Boundary: session.ResultBoundary{Kind: session.BoundaryFinish}}
}
func driveTestRunTask(reason string, continuation session.Continuation, trigger string) session.SummaryTask {
	return session.SummaryTask{TaskID: "task", Reason: reason, Boundary: session.ResultBoundary{Kind: session.BoundaryResumeCheckpoint, ResumeAfter: &session.CheckpointData{Continuation: continuation, TriggerEntryID: trigger}}}
}
func driveTestNavigationTask(target string) session.SummaryTask {
	return session.SummaryTask{TaskID: "task", Boundary: session.ResultBoundary{Kind: session.BoundaryCommitNavigation, TargetID: target}}
}
func driveTestSummaryReady(scope session.OperationScope, task session.SummaryTask, configuration session.LaneConfiguration) session.OperationState {
	return session.OperationState{OperationScope: scope, At: session.AtSummaryReady, Task: task, SummaryContext: session.SummaryContext{ResultEntryID: "summary-entry", Configuration: configuration, RetryPolicy: session.NormalizedRetryPolicy{MaxAttempts: 2, BaseDelayMs: 10, MaxAgentDelayMs: 30000}}, NextAttempt: 1}
}
func driveTestCompactionPreparation() session.DurableStructuralPreparation {
	return session.DurableStructuralPreparation{Kind: session.PreparationCompaction, MessagesToSummarize: []agent.AgentMessage{laneUser("history", 1)}, TurnPrefixMessages: []agent.AgentMessage{}, RetainedTail: []agent.AgentMessage{laneUser("tail", 1)}, TokensBefore: 1000, FileOps: session.DurableFileOperations{Read: []string{}, Written: []string{}, Edited: []string{}}, Settings: harness.CompactionSettings{Enabled: true, ReserveTokens: 1000, KeepRecentTokens: 10}}
}
func driveTestBranchPreparation() session.DurableStructuralPreparation {
	return session.DurableStructuralPreparation{Kind: session.PreparationBranchSummary, Messages: []agent.AgentMessage{laneUser("abandoned", 1)}, FileOps: session.DurableFileOperations{Read: []string{}, Written: []string{}, Edited: []string{}}, TotalTokens: 10}
}

type driveTestInstallOptions struct {
	Entries     []session.Entry
	TipID       *string
	Preparation *session.DurableStructuralPreparation
	TaskID      string
	Writes      []session.Write
}

// upstream: packages/agent/test/harness/runtime/drive-structural.test.ts:211-278; drive-reconcile.test.ts:168-204.
func (fixture *driveProcedureFixture) install(t *testing.T, state session.OperationState, intent session.OperationIntent, options driveTestInstallOptions) {
	t.Helper()
	tip := options.TipID
	if tip == nil && len(options.Entries) > 0 {
		tip = new(options.Entries[len(options.Entries)-1].ID)
	}
	meta := session.OperationMeta{OperationID: driveProcedureOperationID, Lane: "main", SourceTipID: tip, StartedAt: 1, Intent: intent}
	_, err := fixture.lane.Command(context.Background(), func(projection LaneState, _ session.SessionReader) (LaneCommand[any], error) {
		writes := []session.Write{}
		for _, entry := range options.Entries {
			writes = append(writes, session.InsertEntry(entry))
		}
		writes = append(writes, session.SetValue(session.BranchTip("main"), tip))
		writes = append(writes, options.Writes...)
		writes = append(writes, session.SetValue(session.OperationMetaValue(driveProcedureOperationID), meta), session.SetValue(session.OperationStateValue(driveProcedureOperationID), state), session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: new(driveProcedureOperationID), LastOperationID: projection.LastOperationID, Inbox: projection.Inbox}))
		if options.Preparation != nil {
			taskID := options.TaskID
			if taskID == "" {
				taskID = "task"
			}
			writes = append(writes, session.SetValue(session.OperationPreparation(driveProcedureOperationID, taskID), *options.Preparation))
		}
		projection.TipID = tip
		projection.Operation = &session.Operation{Meta: meta, State: state}
		return LaneCommand[any]{Kind: CommandCommit, Writes: writes, Next: projection, Materialize: func(session.CommitResult) any { return nil }}, nil
	})
	require.NoError(t, err)
	fixture.storage.ClearCommitAttempts()
}
func (fixture *driveProcedureFixture) cancel(t *testing.T) {
	t.Helper()
	_, err := fixture.lane.Command(context.Background(), func(projection LaneState, _ session.SessionReader) (LaneCommand[any], error) {
		if projection.Operation == nil {
			return LaneCommand[any]{}, fmt.Errorf("fixture has no operation")
		}
		current := *projection.Operation
		current.State.Control = session.Control{Status: session.ControlCancelRequested, RequestedAt: 2}
		projection.Operation = &current
		return LaneCommand[any]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.OperationStateValue(driveProcedureOperationID), current.State)}, Next: projection, Materialize: func(session.CommitResult) any { return nil }}, nil
	})
	require.NoError(t, err)
}
func driveTestEntryAt(t *testing.T, fixture *driveProcedureFixture, id string) session.Entry {
	t.Helper()
	entry, err := fixture.session.GetEntry(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, entry)
	return *entry
}
func driveTestValue[T any](t *testing.T, fixture *driveProcedureFixture, address session.Value[T]) *session.StoredValue[T] {
	t.Helper()
	value, err := session.GetValue(context.Background(), fixture.session, address)
	require.NoError(t, err)
	return value
}
func driveTestEventsOf(fixture *driveProcedureFixture, kind agentharness.HarnessEventType) []agentharness.HarnessEvent {
	return slices.DeleteFunc(fixture.events(), func(event agentharness.HarnessEvent) bool { return event.Type() != kind })
}
func driveTestFreezeTime(t *testing.T, now int64) *int64 {
	t.Helper()
	original := runtimeNow
	runtimeNow = func() int64 { return now }
	t.Cleanup(func() { runtimeNow = original })
	return &now
}

type driveTestCallResult[T any] struct {
	result T
	err    error
}

func driveTestAsync[T any](t *testing.T, release func(), call func() (T, error)) <-chan driveTestCallResult[T] {
	t.Helper()
	result := make(chan driveTestCallResult[T], 1)
	done := make(chan struct{})
	go func() { defer close(done); value, err := call(); result <- driveTestCallResult[T]{value, err} }()
	t.Cleanup(func() {
		if release != nil {
			release()
		}
		<-done
	})
	return result
}

type driveTestQueuedEntry struct {
	id, kind string
	pending  session.PendingEntry
}

func driveTestQueuedMessage(id, kind, text string) driveTestQueuedEntry {
	return driveTestQueuedEntry{id: id, kind: kind, pending: session.PendingEntry{Type: session.PendingEntryMessage, Message: laneUser(text, 1)}}
}
func (fixture *driveProcedureFixture) queue(t *testing.T, queued ...driveTestQueuedEntry) {
	t.Helper()
	_, err := fixture.lane.Command(context.Background(), func(projection LaneState, _ session.SessionReader) (LaneCommand[any], error) {
		inbox := slices.Clone(projection.Inbox)
		writes := []session.Write{}
		for _, item := range queued {
			inbox = append(inbox, session.InboxItem{EntryID: item.id, Kind: item.kind})
			writes = append(writes, session.SetValue(session.PendingEntryValue(item.id), item.pending))
		}
		writes = append(writes, session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: new(driveProcedureOperationID), LastOperationID: nil, Inbox: inbox}))
		projection.Inbox = inbox
		return LaneCommand[any]{Kind: CommandCommit, Writes: writes, Next: projection, Materialize: func(session.CommitResult) any { return nil }}, nil
	})
	require.NoError(t, err)
}

func driveTestContinue(t *testing.T, result ProcedureResult, err error) {
	t.Helper()
	require.NoError(t, err)
	require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, result)
}
func driveTestSettled(t *testing.T, result ProcedureResult, err error, kind, status string) {
	t.Helper()
	require.NoError(t, err)
	require.Equal(t, ProcedureSettled, result.Kind)
	require.Equal(t, driveProcedureOperationID, result.Record.OperationID)
	require.Equal(t, kind, result.Record.Kind)
	require.Equal(t, status, result.Record.Status)
}
