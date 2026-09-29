package runtime

import (
	"context"
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

// upstream: packages/agent/test/harness/runtime/test-utils.ts:5-24.
type controlledLaneStorage struct {
	*session.MemoryStorage
	mu               sync.Mutex
	beforeNextCommit func() error
}

func (storage *controlledLaneStorage) Commit(ctx context.Context, writes []session.Write) (session.CommitResult, error) {
	storage.mu.Lock()
	before := storage.beforeNextCommit
	storage.beforeNextCommit = nil
	storage.mu.Unlock()
	if before != nil {
		if err := before(); err != nil {
			return session.CommitResult{}, err
		}
	}
	return storage.MemoryStorage.Commit(ctx, writes)
}
func (storage *controlledLaneStorage) beforeCommit(callback func() error) {
	storage.mu.Lock()
	storage.beforeNextCommit = callback
	storage.mu.Unlock()
}

func laneTestConfiguration() session.LaneConfiguration {
	return session.LaneConfiguration{Model: session.ModelRef{Provider: "test", ModelID: "model"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{}}
}

func laneUser(text string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: timestamp}}
}

func newCommandLane(t *testing.T, emit EmitBatch) (*Lane, *ai.Model, *session.StorageBackedSession, *controlledLaneStorage) {
	t.Helper()
	isolateHarnessTest(t)
	ctx := harness.BackgroundContext()
	storage := &controlledLaneStorage{MemoryStorage: session.NewMemoryStorage(nil)}
	opened := session.NewStorageBackedSession(session.SessionMetadata{ID: "runtime-lane-0", CreatedAt: 1, StorageVersion: 1}, storage, nil)
	t.Cleanup(func() { require.NoError(t, opened.Close(ctx)) })
	_, err := opened.Mutate(ctx, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		return mutator.Commit(ctx, []session.Write{session.SetValue(session.BranchTip("main"), (*string)(nil)), session.SetValue(session.LaneConfig("main"), laneTestConfiguration()), session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{}})})
	})
	require.NoError(t, err)
	faux := newLaneFaux()
	state, err := RestoreLane(ctx, opened, "main")
	require.NoError(t, err)
	if emit == nil {
		emit = func(harness.Context, []agentharness.HarnessEvent) (func() error, error) { return nil, nil }
	}
	hooks := agentharness.NewHookRegistry(func(harness.Context, error, agentharness.HookName, string) error { return nil })
	lane := NewLane("main", opened, faux.models, hooks, state, func(_ harness.Context, err error) error { return err }, emit, nil, func() Config {
		return Config{Tools: []harness.AgentHarnessTool{}, Resources: agentharness.Resources{}, RetryPolicy: ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 1000}, Compaction: compaction.DefaultCompactionSettings, SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: agent.ToolModeParallel, ToProviderMessages: func(_ harness.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
			return harness.ConvertToLlm(messages), nil
		}}
	})
	return lane, faux.model, opened, storage
}

type publicLaneFixture struct {
	lane    *Lane
	harness *Harness
	session *session.StorageBackedSession
	faux    laneFaux
}

func newPublicLane(t *testing.T, deferred bool, resources agentharness.Resources) publicLaneFixture {
	t.Helper()
	isolateHarnessTest(t)
	ctx := harness.BackgroundContext()
	faux := newLaneFaux()
	opened := session.NewStorageBackedSession(session.SessionMetadata{ID: "public-drive-0", CreatedAt: 1, StorageVersion: 1}, session.NewMemoryStorage(nil), nil)
	options := AgentHarnessOptions{Session: opened, Models: faux.models, Model: faux.model, Resources: resources}
	if deferred {
		options.StreamOptions.Deferred = &harness.AgentHarnessDeferredOption{Enabled: true}
	}
	created, err := CreateAgentHarness(ctx, options)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, created.Harness.Close(ctx)); require.NoError(t, opened.Close(ctx)) })
	lane, err := created.Harness.Lane(ctx, "main", nil)
	require.NoError(t, err)
	return publicLaneFixture{lane: lane, harness: created.Harness, session: opened, faux: faux}
}

type laneCallResult[T any] struct {
	value T
	err   error
}

func asyncLaneCall[T any](call func() (T, error)) <-chan laneCallResult[T] {
	result := make(chan laneCallResult[T], 1)
	go func() { value, err := call(); result <- laneCallResult[T]{value: value, err: err} }()
	return result
}
func awaitLaneCall[T any](t *testing.T, result <-chan laneCallResult[T]) T {
	t.Helper()
	outcome := <-result
	require.NoError(t, outcome.err)
	return outcome.value
}
func requireLanePending[T any](t *testing.T, result <-chan laneCallResult[T]) {
	t.Helper()
	select {
	case outcome := <-result:
		t.Fatalf("call completed before release: value=%+v error=%v", outcome.value, outcome.err)
	default:
	}
}

func laneTestReleaseChannel(t *testing.T) chan struct{} {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	return release
}

type laneRelease struct {
	done chan struct{}
	once sync.Once
}

func (release *laneRelease) open() { release.once.Do(func() { close(release.done) }) }
func laneTestBarrier(t *testing.T) (chan struct{}, *laneRelease) {
	t.Helper()
	release := &laneRelease{done: make(chan struct{})}
	t.Cleanup(release.open)
	return make(chan struct{}), release
}
func blockedLaneResponse(t *testing.T, text string) (<-chan struct{}, func(), ai.FauxResponseStep) {
	t.Helper()
	started, release := laneTestBarrier(t)
	return started, release.open, ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.FauxResponse, error) {
		close(started)
		<-release.done
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"}, nil
	})
}
func acceptPublicRun(t *testing.T, lane *Lane, id string) agentharness.OperationAdmission {
	t.Helper()
	admission, err := lane.Accept(t.Context(), agentharness.OperationRequest{Kind: agentharness.RequestPrompt, OperationID: &id, PromptText: &id})
	require.NoError(t, err)
	return admission
}
func requireLaneRun(t *testing.T, result agentharness.RunOutcome, err error, status string) {
	t.Helper()
	require.NoError(t, err)
	require.NotNil(t, result.Record)
	require.Equal(t, "run", result.Record.Kind)
	require.Equal(t, status, result.Record.Status)
}
func requireLaneSettled(t *testing.T, result agentharness.DriveOutcome, err error, status string) {
	t.Helper()
	require.NoError(t, err)
	require.Equal(t, agentharness.DriveSettled, result.Kind)
	require.NotNil(t, result.Outcome)
	require.Equal(t, status, result.Outcome.Status)
}

func setLaneTestThinking(ctx harness.Context, lane *Lane, thinking ai.ThinkingLevel, observed *[]ai.ThinkingLevel) (ai.ThinkingLevel, error) {
	return Command(ctx, lane, func(state LaneState, _ session.SessionReader) (LaneCommand[ai.ThinkingLevel], error) {
		if observed != nil {
			*observed = append(*observed, state.Configuration.ThinkingLevel)
		}
		next := state
		next.Configuration.ThinkingLevel = thinking
		return LaneCommand[ai.ThinkingLevel]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.LaneConfig(lane.Name()), next.Configuration)}, Next: next, Materialize: func(session.CommitResult) ai.ThinkingLevel { return thinking }}, nil
	})
}
