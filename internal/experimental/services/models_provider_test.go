package services

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/ai"
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
func (s *testModelsState) Subscribe(listener func(*ModelsState, context.Context, pico3.ReplicatedStateDelivery)) (func(), error) {
	listener(s.Value(), context.Background(), pico3.ReplicatedStateDelivery{Kind: "hydrate"})
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

type testModelsLane struct {
	mu          sync.Mutex
	model       *ai.Model
	thinking    ai.ThinkingLevel
	models      []*ai.Model
	getModel    func(context.Context) (*ai.Model, error)
	getThinking func(context.Context) (ai.ThinkingLevel, error)
	setError    error
}

func (lane *testModelsLane) GetModel(ctx context.Context) (*ai.Model, error) {
	if lane.getModel != nil {
		return lane.getModel(ctx)
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return lane.model, ctx.Err()
}
func (lane *testModelsLane) GetThinkingLevel(ctx context.Context) (ai.ThinkingLevel, error) {
	if lane.getThinking != nil {
		return lane.getThinking(ctx)
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return lane.thinking, ctx.Err()
}
func (lane *testModelsLane) SetModel(ctx context.Context, ref ModelRef) error {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if lane.setError != nil {
		return lane.setError
	}
	for _, model := range lane.models {
		if model.ProviderMeta.ProviderID == ref.Provider && model.ID == ref.ModelId {
			lane.model = model
			return nil
		}
	}
	return errors.New("lane missing model")
}
func (lane *testModelsLane) SetThinkingLevel(ctx context.Context, level ai.ThinkingLevel) error {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if lane.setError != nil {
		return lane.setError
	}
	lane.thinking = level
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
		model.Capabilities.MaxThinking = ai.ThinkingHigh
	}
	return model
}
func testService(lane ModelsServiceLane, models ModelsServiceModelRuntime, settings ModelsServiceSettingsManager) (*ModelsServiceRuntime, *testModelsState) {
	var state *testModelsState
	runtime := CreateModelsService(lane, models, settings, func(initial *ModelsState) pico3.MutableReplicatedStateOf[*ModelsState] {
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

// models-provider.ts reads catalog/configuration together and appends only an absent selected identity.
func TestModelsActivationCatalogAndRefreshWithoutRuntime(t *testing.T) {
	selected := testModel("local", "selected", true)
	for _, test := range []struct {
		name      string
		selected  *ai.Model
		available []*ai.Model
		want      []ModelSummary
	}{
		{"empty", nil, nil, []ModelSummary{}},
		{"selected only", selected, nil, []ModelSummary{{ModelRef: ModelRef{"local", "selected"}, Name: "name-selected", Reasoning: true}}},
		{"same id different provider", selected, []*ai.Model{testModel("other", "selected", false)}, []ModelSummary{{ModelRef: ModelRef{"other", "selected"}, Name: "name-selected"}, {ModelRef: ModelRef{"local", "selected"}, Name: "name-selected", Reasoning: true}}},
		{"no duplicate", selected, []*ai.Model{testModel("local", "selected", false)}, []ModelSummary{{ModelRef: ModelRef{"local", "selected"}, Name: "name-selected"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lane := &testModelsLane{model: test.selected, thinking: ai.ThinkingHigh}
			var models ModelsServiceModelRuntime
			if test.available != nil {
				models = &testModelsRuntime{available: test.available}
			}
			runtime, state := testService(lane, models, nil)
			checkModelsEqual(t, state.Value(), &ModelsState{Catalog: ModelsCatalog{AvailableModels: []ModelSummary{}}, Configuration: ModelsConfiguration{ThinkingLevel: ai.ThinkingOff}, Refresh: ModelsRefresh{Status: "idle"}})
			requireModelsOK(t, runtime.Activate(t.Context()))
			checkModelsEqual(t, state.Value().Catalog, ModelsCatalog{Revision: 1, AvailableModels: test.want})
			if test.selected == nil {
				checkModelsEqual(t, state.Value().Configuration.Model, (*ModelRef)(nil))
			} else {
				checkModelsEqual(t, state.Value().Configuration.Model, &ModelRef{"local", "selected"})
			}
			checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingHigh)
			if models == nil {
				requireModelsOK(t, runtime.Service.Refresh(t.Context()))
				checkModelsEqual(t, state.Value().Catalog.Revision, 2)
				checkModelsEqual(t, state.changes[1].Refresh.Status, "refreshing")
				checkModelsEqual(t, state.Value().Refresh.Status, "done")
			}
		})
	}
}

func TestModelsThinkingValidationCycleAndNoPersistence(t *testing.T) {
	model := testModel("local", "reasoner", true)
	model.ThinkingLevelMap = ai.ThinkingLevelMap{ai.ThinkingMinimal: nil}
	lane := &testModelsLane{model: model, thinking: ai.ThinkingHigh}
	runtime, state := testService(lane, nil, nil)
	levels, err := runtime.Service.GetThinkingLevels(t.Context())
	requireModelsOK(t, err)
	checkModelsEqual(t, levels, []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh})
	levels[0] = ai.ThinkingMax
	fresh, err := runtime.Service.GetThinkingLevels(t.Context())
	requireModelsOK(t, err)
	checkModelsEqual(t, fresh[0], ai.ThinkingOff)
	requireModelsOK(t, runtime.Service.CycleThinking(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingOff)
	lane.thinking = "invalid"
	requireModelsOK(t, runtime.Service.CycleThinking(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingOff)
	err = runtime.Service.SelectThinking(t.Context(), ai.ThinkingMax)
	if err == nil || err.Error() != "Thinking level max is unavailable; choose one of: off, low, medium, high" {
		t.Fatal(err)
	}
	requireModelsOK(t, runtime.Service.SelectThinking(t.Context(), ai.ThinkingMedium))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingMedium)
	checkModelsEqual(t, state.Value().Catalog.Revision, 0)
	lane.model = nil
	levels, err = runtime.Service.GetThinkingLevels(t.Context())
	requireModelsOK(t, err)
	checkModelsEqual(t, levels, []ai.ThinkingLevel{ai.ThinkingOff})
	requireModelsOK(t, runtime.Service.CycleThinking(t.Context()))
	checkModelsEqual(t, state.Value().Configuration.ThinkingLevel, ai.ThinkingOff)
}

func TestModelsSelectWaitsForSettingsBeforePublication(t *testing.T) {
	model := testModel("local", "chosen", true)
	lane := &testModelsLane{models: []*ai.Model{model}, thinking: ai.ThinkingLow}
	models := &testModelsRuntime{all: lane.models}
	entered, release := make(chan struct{}), make(chan struct{})
	settings := &testModelsSettings{flush: func() error { close(entered); <-release; return nil }}
	runtime, state := testService(lane, models, settings)
	err := runtime.Service.Select(t.Context(), ModelRef{"missing", "unknown"})
	if err == nil || err.Error() != "Unknown model: missing/unknown" {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runtime.Service.Select(t.Context(), ModelRef{"local", "chosen"}) }()
	<-entered
	checkModelsEqual(t, settings.selected, ModelRef{"local", "chosen"})
	checkModelsEqual(t, state.Value().Configuration.Model, (*ModelRef)(nil))
	select {
	case err := <-done:
		t.Fatalf("returned before flush: %v", err)
	default:
	}
	close(release)
	requireModelsOK(t, <-done)
	checkModelsEqual(t, state.Value().Configuration, ModelsConfiguration{Model: &ModelRef{"local", "chosen"}, ThinkingLevel: ai.ThinkingLow})
	checkModelsEqual(t, state.Value().Catalog.Revision, 0)
	checkModelsEqual(t, state.Value().Refresh.Status, "idle")
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

func TestModelsActivationStartsAllReadsAndJoinsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan string, 3)
	lane := &testModelsLane{
		getModel: func(ctx context.Context) (*ai.Model, error) { started <- "model"; <-ctx.Done(); return nil, ctx.Err() },
		getThinking: func(ctx context.Context) (ai.ThinkingLevel, error) {
			started <- "thinking"
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	runtime, state := testService(lane, nil, nil)
	done := make(chan error, 1)
	go func() { done <- runtime.Activate(ctx) }()
	reads := []string{<-started, <-started, <-started}
	slices.Sort(reads)
	checkModelsEqual(t, reads, []string{"model", "model", "thinking"})
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	checkModelsEqual(t, state.Value().Catalog.Revision, 0)
	checkModelsEqual(t, len(state.changes), 0)
}

func TestModelsServiceProvidesBeforeActivation(t *testing.T) {
	runtime, state := testService(&testModelsLane{thinking: ai.ThinkingOff}, nil, nil)
	checkModelsEqual(t, runtime.Service.State().Value().Catalog.Revision, 0)
	requireModelsOK(t, runtime.Activate(context.Background()))
	checkModelsEqual(t, state.Value().Catalog.Revision, 1)
}
