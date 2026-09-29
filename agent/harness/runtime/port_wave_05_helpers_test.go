package runtime

import (
	"context"
	"encoding/json"
	"errors"
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

type procedureFixture struct {
	lane         *Lane
	drive        *Drive
	session      session.Session
	storage      *sessiontesting.InstrumentedStorage
	models       *ai.Models
	model        *ai.Model
	provider     *ai.ModelsProvider
	hooks        *agentharness.HookRegistry
	config       Config
	mu           sync.Mutex
	events       []agentharness.HarnessEvent
	observations []string
	onEmit       func([]agentharness.HarnessEvent) error
}

func newProcedureFixture(t *testing.T, backend session.Storage) *procedureFixture {
	t.Helper()
	isolateHarnessTest(t)
	if backend == nil {
		backend = session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 100 }})
	}
	fixture := &procedureFixture{storage: sessiontesting.NewInstrumentedStorage(backend)}
	fixture.session = session.NewStorageBackedSession(session.SessionMetadata{ID: t.Name(), CreatedAt: 1, StorageVersion: 1}, fixture.storage, nil)
	t.Cleanup(func() { require.NoError(t, fixture.session.Close(context.Background())) })
	fixture.model = &ai.Model{ID: "faux-1", DisplayName: "Faux Model", ProviderMeta: ai.ProviderMetadata{ProviderID: "faux", API: "faux", BaseURL: "http://localhost:0"}, Input: []string{"text", "image"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}}
	fixture.provider = &ai.ModelsProvider{ID: "faux", GetModels: func() ([]*ai.Model, error) { return []*ai.Model{fixture.model}, nil }, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Faux", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}}}
	fixture.models = ai.CreateModels()
	fixture.models.SetProvider(fixture.provider)
	fixture.hooks = agentharness.NewHookRegistry(func(_ context.Context, err error, _ agentharness.HookName, _ string) error { return err })
	fixture.config = Config{Tools: []harness.AgentHarnessTool{}, RetryPolicy: ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 1}, Compaction: compaction.DefaultCompactionSettings, SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: agent.ToolModeParallel, ToProviderMessages: func(_ context.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
		return harness.ConvertToLlm(messages), nil
	}, EntryProjectors: map[string]session.EntryProjector{}}
	return fixture
}

func (fixture *procedureFixture) installLane(t *testing.T, operationID string, writes []session.Write) {
	t.Helper()
	_, err := fixture.session.Mutate(t.Context(), func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		return mutator.Commit(ctx, writes)
	})
	require.NoError(t, err)
	state, err := RestoreLane(t.Context(), fixture.session, "main")
	require.NoError(t, err)
	fixture.lane = NewLane("main", fixture.session, fixture.models, fixture.hooks, state, func(_ context.Context, err error) error { return err }, func(_ context.Context, batch []agentharness.HarnessEvent) (func() error, error) {
		copy := make([]agentharness.HarnessEvent, len(batch))
		for i, event := range batch {
			copy[i] = agentharness.CloneHarnessEvent(event)
		}
		fixture.mu.Lock()
		fixture.events = append(fixture.events, copy...)
		for _, event := range copy {
			fixture.observations = append(fixture.observations, string(event.Type()))
		}
		fixture.mu.Unlock()
		return func() error {
			if fixture.onEmit != nil {
				return fixture.onEmit(copy)
			}
			return nil
		}, nil
	}, nil, func() Config { return fixture.config })
	if operationID != "" {
		fixture.drive = NewDrive(t.Context(), agentharness.DriveOptions{OperationID: operationID})
		fixture.lane.ActiveDrive = fixture.drive
	}
}

func (fixture *procedureFixture) currentRun(t *testing.T) session.OperationState {
	t.Helper()
	state := fixture.lane.SnapshotState()
	require.NotNil(t, state.Operation, "fixture has no operation")
	return state.Operation.State
}
func (fixture *procedureFixture) eventSnapshot() []agentharness.HarnessEvent {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]agentharness.HarnessEvent{}, fixture.events...)
}
func (fixture *procedureFixture) eventTypes() []agentharness.HarnessEventType {
	events := fixture.eventSnapshot()
	types := make([]agentharness.HarnessEventType, len(events))
	for i, event := range events {
		types[i] = event.Type()
	}
	return types
}
func (fixture *procedureFixture) eventsOf(kind agentharness.HarnessEventType) []agentharness.HarnessEvent {
	var events []agentharness.HarnessEvent
	for _, event := range fixture.eventSnapshot() {
		if event.Type() == kind {
			events = append(events, event)
		}
	}
	return events
}
func (fixture *procedureFixture) requireRestores(t *testing.T) {
	t.Helper()
	restored, err := RestoreLane(t.Context(), fixture.session, "main")
	require.NoError(t, err)
	require.Equal(t, restored, fixture.lane.SnapshotState())
}
func (fixture *procedureFixture) replaceRun(t *testing.T, next session.OperationState, extra ...session.Write) {
	t.Helper()
	_, err := fixture.lane.Command(t.Context(), func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
		operation := *state.Operation
		operation.State = next
		state.Operation = &operation
		return LaneCommand[any]{Kind: CommandCommit, Writes: append(extra, session.SetValue(session.OperationStateValue(fixture.drive.OperationID), next)), Next: state, Materialize: func(session.CommitResult) any { return nil }}, nil
	})
	require.NoError(t, err)
}
func runProcedureAsync(t *testing.T, fixture *procedureFixture, invoke func() (ProcedureResult, error), release ...func()) func() error {
	t.Helper()
	result := asyncLaneCall(invoke)
	awaited := false
	t.Cleanup(func() {
		for _, unblock := range release {
			unblock()
		}
		fixture.drive.CloseGate(errors.New("test cleanup"))
		if !awaited {
			<-result
		}
	})
	return func() error { outcome := <-result; awaited = true; return outcome.err }
}

func requireToolEvent(t *testing.T, fixture *procedureFixture, kind agentharness.HarnessEventType, id string) agentharness.HarnessEvent {
	t.Helper()
	for _, event := range fixture.eventsOf(kind) {
		switch payload := event.Payload.(type) {
		case agentharness.ToolStartPayload:
			if payload.ToolCallID == id {
				return event
			}
		case agentharness.ToolEndPayload:
			if payload.ToolCallID == id {
				return event
			}
		}
	}
	t.Fatalf("missing %s for tool call %s", kind, id)
	return agentharness.HarnessEvent{}
}

func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	return result
}
func requireNoValue[T any](t *testing.T, fixture *procedureFixture, address session.Value[T]) {
	t.Helper()
	value, err := session.GetValue(t.Context(), fixture.session, address)
	require.NoError(t, err)
	require.Nil(t, value)
}
func requireEntry(t *testing.T, fixture *procedureFixture, id string) session.Entry {
	t.Helper()
	entry, err := fixture.session.GetEntry(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, entry)
	return *entry
}
