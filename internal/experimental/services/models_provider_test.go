package services

// pi: packages/coding-agent/src/experimental/services/models-provider.ts

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

type testModelsState struct {
	mu          sync.Mutex
	value       *ModelsState
	changes     []*ModelsState
	changeError error
}

func copyModelsState(value *ModelsState) *ModelsState {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var result ModelsState
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}
	return &result
}
func (s *testModelsState) Value() *ModelsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyModelsState(s.value)
}
func (s *testModelsState) Subscribe(listener func(*ModelsState, context.Context, chord.ReplicatedStateDelivery)) (func(), error) {
	listener(s.Value(), context.Background(), chord.ReplicatedStateDelivery{Kind: "hydrate"})
	return func() {}, nil
}
func (s *testModelsState) Change(_ context.Context, change func(*ModelsState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changeError != nil {
		return s.changeError
	}
	draft := copyModelsState(s.value)
	if err := change(draft); err != nil {
		return err
	}
	s.value = copyModelsState(draft)
	s.changes = append(s.changes, s.value)
	return nil
}
func (s *testModelsState) Replace(ctx context.Context, value *ModelsState) error {
	return s.Change(ctx, func(draft *ModelsState) error { *draft = *copyModelsState(value); return nil })
}

// testModelsLane is a conversation and its pi.agent document in one: Configure applies the change to the document and tells its subscribers, as pi-durable's commit does.
type testModelsLane struct {
	mu             sync.Mutex
	model          *ModelRef
	thinking       ai.ModelThinkingLevel
	configureError error
	configured     []ConversationConfiguration
	subscribers    []func(context.Context)
}

func (lane *testModelsLane) Value() *AgentState {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return &AgentState{Model: lane.model, ThinkingLevel: lane.thinking}
}
func (lane *testModelsLane) Subscribe(listener func(context.Context)) func() {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	lane.subscribers = append(lane.subscribers, listener)
	return func() {}
}
func (lane *testModelsLane) Dispose() {}
func (lane *testModelsLane) Configure(ctx context.Context, configuration ConversationConfiguration) error {
	lane.mu.Lock()
	if lane.configureError != nil {
		defer lane.mu.Unlock()
		return lane.configureError
	}
	lane.configured = append(lane.configured, configuration)
	if configuration.Model != nil {
		lane.model = configuration.Model
	}
	if configuration.ThinkingLevel != nil {
		lane.thinking = *configuration.ThinkingLevel
	}
	listeners := append([]func(context.Context){}, lane.subscribers...)
	lane.mu.Unlock()
	for _, listener := range listeners {
		listener(ctx)
	}
	return nil
}

type testModelsRuntime struct {
	available []*ai.Model
	all       []*ai.Model
	refresh   func(context.Context) (ModelsRefreshResult, error)
}

func (runtime *testModelsRuntime) GetAvailableSnapshot() []*ai.Model { return runtime.available }
func (runtime *testModelsRuntime) GetModel(provider, id string) *ai.Model {
	for _, model := range runtime.all {
		if model.ProviderMeta.ProviderID == provider && model.ID == id {
			return model
		}
	}
	return nil
}
func (runtime *testModelsRuntime) Refresh(ctx context.Context) (ModelsRefreshResult, error) {
	return runtime.refresh(ctx)
}

type testModelsSettings struct {
	selected ModelRef
	flush    func() error
	setError error
	onSet    func(ModelRef)
}

func (settings *testModelsSettings) SetDefaultModelAndProvider(provider, id string) error {
	settings.selected = ModelRef{Provider: provider, ModelId: id}
	if settings.onSet != nil {
		settings.onSet(settings.selected)
	}
	return settings.setError
}
func (settings *testModelsSettings) Flush() error { return settings.flush() }

func testModel(provider, id string, reasoning bool) *ai.Model {
	model := &ai.Model{ID: id, DisplayName: "name-" + id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider, Reasoning: reasoning}}
	if reasoning {
		model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	}
	return model
}
func testService(lane *testModelsLane, models ModelsServiceModelRuntime, settings ModelsServiceSettingsManager) (*ModelsServiceRuntime, *testModelsState) {
	var state *testModelsState
	runtime := CreateModelsService(lane, lane, models, settings, func(initial *ModelsState) chord.MutableReplicatedStateOf[*ModelsState] {
		state = &testModelsState{value: copyModelsState(initial)}
		return state
	})
	return runtime, state
}
func checkModelsEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func requireModelsOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func refOf(model *ai.Model) *ModelRef {
	if model == nil {
		return nil
	}
	return &ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID}
}

// models-provider.ts:62-76 reads the catalog from the runtime and appends only an absent selected identity; the selected model is the agent document's reference resolved through the runtime.
func TestModelsActivationCatalogAndRefreshWithoutRuntime(t *testing.T) {
	selected := testModel("local", "selected", true)
	for _, test := range []struct {
		name      string
		selected  *ai.Model
		available []*ai.Model
		want      []ModelSummary
	}{
		{"empty", nil, []*ai.Model{}, []ModelSummary{}},
		{"selected only", selected, []*ai.Model{}, []ModelSummary{{ModelRef: ModelRef{"local", "selected"}, Name: "name-selected", Reasoning: true}}},
		{"same id different provider", selected, []*ai.Model{testModel("other", "selected", false)}, []ModelSummary{{ModelRef: ModelRef{"other", "selected"}, Name: "name-selected"}, {ModelRef: ModelRef{"local", "selected"}, Name: "name-selected", Reasoning: true}}},
		{"no duplicate", selected, []*ai.Model{testModel("local", "selected", false)}, []ModelSummary{{ModelRef: ModelRef{"local", "selected"}, Name: "name-selected"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lane := &testModelsLane{model: refOf(test.selected), thinking: ai.ThinkingHigh}
			models := &testModelsRuntime{available: test.available, all: []*ai.Model{selected}}
			runtime, state := testService(lane, models, nil)
			// The configuration starts from the agent document; the catalog waits for activation.
			checkModelsEqual(t, state.Value(), &ModelsState{Catalog: ModelsCatalog{AvailableModels: []ModelSummary{}}, Configuration: ModelsConfiguration{Model: refOf(test.selected), ThinkingLevel: ai.ThinkingHigh}, Refresh: ModelsRefresh{Status: "idle"}})
			requireModelsOK(t, runtime.Activate(t.Context()))
			checkModelsEqual(t, state.Value().Catalog, ModelsCatalog{Revision: 1, AvailableModels: test.want})
			checkModelsEqual(t, state.Value().Configuration.Model, refOf(test.selected))
		})
	}
	t.Run("without a runtime", func(t *testing.T) {
		runtime, state := testService(&testModelsLane{model: &ModelRef{"local", "selected"}, thinking: ai.ThinkingLow}, nil, nil)
		requireModelsOK(t, runtime.Activate(t.Context()))
		checkModelsEqual(t, state.Value().Catalog, ModelsCatalog{Revision: 1, AvailableModels: []ModelSummary{}})
		requireModelsOK(t, runtime.Service.Refresh(t.Context()))
		checkModelsEqual(t, state.Value().Catalog.Revision, 2)
		checkModelsEqual(t, state.changes[1].Refresh.Status, "refreshing")
		checkModelsEqual(t, state.Value().Refresh.Status, "done")
	})
}

// models-provider.ts:68-81 and :102-119: cycling and selecting a level configure the conversation; the published configuration follows the agent document (:150-165), so it changes only when the document change is synced.
func TestModelsThinkingValidationCycleAndNoPersistence(t *testing.T) {
	model := testModel("local", "reasoner", true)
	model.ThinkingLevelMap = ai.ThinkingLevelMap{ai.ThinkingMinimal: nil}
	lane := &testModelsLane{model: refOf(model), thinking: ai.ThinkingHigh}
	runtime, state := testService(lane, &testModelsRuntime{all: []*ai.Model{model}}, nil)
	levels, err := runtime.Service.GetThinkingLevels(t.Context())
	requireModelsOK(t, err)
	checkModelsEqual(t, levels, []ai.ModelThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh})
	levels[0] = ai.ThinkingMax
	fresh, err := runtime.Service.GetThinkingLevels(t.Context())
	requireModelsOK(t, err)
	checkModelsEqual(t, fresh[0], ai.ThinkingOff)
	requireModelsOK(t, runtime.Service.CycleThinking(t.Context()))
	off := ai.ThinkingOff
	checkModelsEqual(t, lane.configured, []ConversationConfiguration{{ThinkingLevel: &off}})
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingHigh))
	requireModelsOK(t, runtime.SyncConfiguration(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingOff)
	lane.thinking = "invalid"
	requireModelsOK(t, runtime.Service.CycleThinking(t.Context()))
	checkModelsEqual(t, lane.thinking, ai.ThinkingOff)
	err = runtime.Service.SelectThinking(t.Context(), ai.ThinkingMax)
	if err == nil || err.Error() != "Thinking level max is unavailable; choose one of: off, low, medium, high" {
		t.Fatal(err)
	}
	requireModelsOK(t, runtime.Service.SelectThinking(t.Context(), ai.ThinkingMedium))
	requireModelsOK(t, runtime.SyncConfiguration(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ModelThinkingLevel(ai.ThinkingMedium))
	checkModelsEqual(t, state.Value().Catalog.Revision, 0)
	lane.model = nil
	levels, err = runtime.Service.GetThinkingLevels(t.Context())
	requireModelsOK(t, err)
	checkModelsEqual(t, levels, []ai.ModelThinkingLevel{ai.ThinkingOff})
	requireModelsOK(t, runtime.Service.CycleThinking(t.Context()))
	checkModelsEqual(t, lane.thinking, ai.ThinkingOff)
}

// models-provider.ts:88-101: selecting a model clamps the current level to it, configures both together, then persists the default and flushes. Publication follows the document.
func TestModelsSelectConfiguresClampsAndPersists(t *testing.T) {
	reasoner, plain := testModel("local", "chosen", true), testModel("other", "chosen", false)
	lane := &testModelsLane{model: refOf(reasoner), thinking: ai.ThinkingHigh}
	models := &testModelsRuntime{all: []*ai.Model{reasoner, plain}}
	entered, release := make(chan struct{}), make(chan struct{})
	settings := &testModelsSettings{flush: func() error { close(entered); <-release; return nil }}
	runtime, state := testService(lane, models, settings)
	err := runtime.Service.Select(t.Context(), ModelRef{"missing", "unknown"})
	if err == nil || err.Error() != "Unknown model: missing/unknown" {
		t.Fatal(err)
	}
	checkModelsEqual(t, len(lane.configured), 0)
	done := make(chan error, 1)
	go func() { done <- runtime.Service.Select(t.Context(), ModelRef{"other", "chosen"}) }()
	<-entered
	checkModelsEqual(t, settings.selected, ModelRef{"other", "chosen"})
	off := ai.ThinkingOff
	checkModelsEqual(t, lane.configured, []ConversationConfiguration{{Model: &ModelRef{"other", "chosen"}, ThinkingLevel: &off}})
	select {
	case err := <-done:
		t.Fatalf("returned before flush: %v", err)
	default:
	}
	close(release)
	requireModelsOK(t, <-done)
	requireModelsOK(t, runtime.SyncConfiguration(t.Context()))
	checkModelsEqual(t, state.Value().Configuration, ModelsConfiguration{Model: &ModelRef{"other", "chosen"}, ThinkingLevel: ai.ThinkingOff})
	checkModelsEqual(t, state.Value().Catalog.Revision, 0)
	checkModelsEqual(t, state.Value().Refresh.Status, "idle")
}

func TestModelsSelectFailuresDoNotPersistOrPublish(t *testing.T) {
	failure := errors.New("injected failure")
	for _, mode := range []string{"configure", "settings", "flush", "no runtime"} {
		t.Run(mode, func(t *testing.T) {
			model := testModel("local", "chosen", true)
			lane := &testModelsLane{thinking: ai.ThinkingOff}
			settings := &testModelsSettings{flush: func() error { return nil }}
			runtime, state := testService(lane, &testModelsRuntime{all: []*ai.Model{model}}, settings)
			switch mode {
			case "configure":
				lane.configureError = failure
			case "settings":
				settings.setError = failure
			case "flush":
				settings.flush = func() error { return failure }
			case "no runtime":
				runtime, state = testService(lane, nil, settings)
			}
			err := runtime.Service.Select(t.Context(), ModelRef{"local", "chosen"})
			if mode == "no runtime" {
				if err == nil || err.Error() != "Unknown model: local/chosen" {
					t.Fatal(err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if mode == "configure" && settings.selected != (ModelRef{}) {
				t.Fatalf("persisted %+v after a configure failure", settings.selected)
			}
			checkModelsEqual(t, state.Value().Configuration.Model, (*ModelRef)(nil))
			checkModelsEqual(t, len(state.changes), 0)
		})
	}
}

// models-provider.ts:150-165: a configuration that equals the published one publishes nothing.
func TestModelsSyncConfigurationPublishesOnlyChanges(t *testing.T) {
	lane := &testModelsLane{model: &ModelRef{"local", "a"}, thinking: ai.ThinkingLow}
	runtime, state := testService(lane, nil, nil)
	requireModelsOK(t, runtime.SyncConfiguration(t.Context()))
	checkModelsEqual(t, len(state.changes), 0)
	lane.model = &ModelRef{"local", "b"}
	requireModelsOK(t, runtime.SyncConfiguration(t.Context()))
	checkModelsEqual(t, len(state.changes), 1)
	checkModelsEqual(t, state.Value().Configuration.Model, &ModelRef{"local", "b"})
	requireModelsOK(t, runtime.SyncConfiguration(t.Context()))
	checkModelsEqual(t, len(state.changes), 1)
	lane.model, lane.thinking = nil, ""
	requireModelsOK(t, runtime.SyncConfiguration(t.Context()))
	checkModelsEqual(t, state.Value().Configuration, ModelsConfiguration{ThinkingLevel: ai.ThinkingOff})
}

func TestModelsRefreshWarningFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"done", "warning", "reject", "cancel", "aborted-result"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			failure := errors.New("refresh failed")
			models := &testModelsRuntime{refresh: func(received context.Context) (ModelsRefreshResult, error) {
				if received != ctx {
					return ModelsRefreshResult{}, errors.New("wrong context")
				}
				close(entered)
				<-release
				switch mode {
				case "reject":
					return ModelsRefreshResult{}, failure
				case "cancel":
					<-received.Done()
					return ModelsRefreshResult{}, received.Err()
				case "warning":
					return ModelsRefreshResult{Errors: map[string]error{"local": failure}}, nil
				case "aborted-result":
					return ModelsRefreshResult{Aborted: true}, nil
				default:
					return ModelsRefreshResult{}, nil
				}
			}}
			runtime, state := testService(&testModelsLane{thinking: ai.ThinkingOff}, models, nil)
			done := make(chan error, 1)
			go func() { done <- runtime.Service.Refresh(ctx) }()
			<-entered
			checkModelsEqual(t, state.Value().Refresh.Status, "refreshing")
			checkModelsEqual(t, state.Value().Catalog.Revision, 0)
			if mode == "cancel" {
				cancel()
			}
			close(release)
			err := <-done
			switch mode {
			case "reject", "cancel":
				want := failure
				if mode == "cancel" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatal(err)
				}
				checkModelsEqual(t, state.Value().Refresh.Status, "refreshing")
				checkModelsEqual(t, state.Value().Catalog.Revision, 0)
			default:
				requireModelsOK(t, err)
				checkModelsEqual(t, state.Value().Catalog.Revision, 1)
				want := ModelsRefresh{Status: "done"}
				if mode == "warning" {
					want = ModelsRefresh{Status: "warning", Errors: map[string]string{"local": "refresh failed"}}
				}
				checkModelsEqual(t, state.Value().Refresh, want)
			}
		})
	}
}

func TestModelsRefreshPublicationFailureDoesNotStartRuntime(t *testing.T) {
	called := false
	models := &testModelsRuntime{refresh: func(context.Context) (ModelsRefreshResult, error) { called = true; return ModelsRefreshResult{}, nil }}
	runtime, state := testService(&testModelsLane{}, models, nil)
	failure := errors.New("publication failed")
	state.changeError = failure
	if err := runtime.Service.Refresh(t.Context()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if called {
		t.Fatal("refresh started after publication rejected")
	}
	checkModelsEqual(t, state.Value().Refresh.Status, "idle")
}

func TestModelsConcurrentCatalogRevisionsAreDistinct(t *testing.T) {
	runtime, state := testService(&testModelsLane{thinking: ai.ThinkingOff}, nil, nil)
	var work sync.WaitGroup
	const operations = 100
	for range operations {
		work.Go(func() {
			if err := runtime.Activate(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	work.Wait()
	checkModelsEqual(t, state.Value().Catalog.Revision, operations)
}

func TestModelsServiceProvidesBeforeActivation(t *testing.T) {
	runtime, state := testService(&testModelsLane{thinking: ai.ThinkingOff}, nil, nil)
	checkModelsEqual(t, runtime.Service.State().Value().Catalog.Revision, 0)
	requireModelsOK(t, runtime.Activate(context.Background()))
	checkModelsEqual(t, state.Value().Catalog.Revision, 1)
}
