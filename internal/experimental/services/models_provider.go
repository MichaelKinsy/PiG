package services

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// ModelsServiceLane is the configuration boundary consumed from an AgentLane.
type ModelsServiceLane interface {
	GetModel(context.Context) (*ai.Model, error)
	SetModel(context.Context, ModelRef) error
	GetThinkingLevel(context.Context) (ai.ThinkingLevel, error)
	SetThinkingLevel(context.Context, ai.ThinkingLevel) error
}

// ModelsRefreshResult carries refresh diagnostics. An aborted result is not itself a rejected operation.
type ModelsRefreshResult struct {
	Aborted bool
	Errors  map[string]error
}

// ModelsServiceModelRuntime supplies snapshots and lookup without contacting a provider during reads. Refresh policy belongs to the supplied runtime.
type ModelsServiceModelRuntime interface {
	GetAvailableSnapshot() []*ai.Model
	GetModel(provider, modelId string) *ai.Model
	Refresh(context.Context) (ModelsRefreshResult, error)
}

// ModelsServiceSettingsManager persists model selection before publication.
type ModelsServiceSettingsManager interface {
	SetDefaultModelAndProvider(provider, model string) error
	Flush() error
}

// ModelsServiceRuntime binds one Models service to its activation operation.
type ModelsServiceRuntime struct {
	Service  Models
	provider *modelsService
}

type modelsService struct {
	lane            ModelsServiceLane
	modelRuntime    ModelsServiceModelRuntime
	settingsManager ModelsServiceSettingsManager
	state           pico3.MutableReplicatedStateOf[*ModelsState]
	catalogRevision atomic.Int64
}

// CreateModelsService creates an inactive service with an empty catalog and no selected model. State allocation and publication belong to the caller's Chord host.
func CreateModelsService(lane ModelsServiceLane, modelRuntime ModelsServiceModelRuntime, settingsManager ModelsServiceSettingsManager, createState func(*ModelsState) pico3.MutableReplicatedStateOf[*ModelsState]) *ModelsServiceRuntime {
	provider := &modelsService{
		lane:            lane,
		modelRuntime:    modelRuntime,
		settingsManager: settingsManager,
		state: createState(&ModelsState{
			Catalog:       ModelsCatalog{AvailableModels: []ModelSummary{}},
			Configuration: ModelsConfiguration{ThinkingLevel: ai.ThinkingOff},
			Refresh:       ModelsRefresh{Status: "idle"},
		}),
	}
	return &ModelsServiceRuntime{Service: provider, provider: provider}
}

// Activate reads the current catalog and lane configuration and publishes one idle snapshot.
func (runtime *ModelsServiceRuntime) Activate(ctx context.Context) error {
	return runtime.provider.publishSnapshot(ctx, ModelsRefresh{Status: "idle"})
}

func (service *modelsService) State() pico3.ReplicatedStateOf[*ModelsState] { return service.state }

func (service *modelsService) readConfiguration(ctx context.Context) (ModelsConfiguration, error) {
	selected, thinking, err := modelsReadPair(
		func() (*ai.Model, error) { return service.lane.GetModel(ctx) },
		func() (ai.ThinkingLevel, error) { return service.lane.GetThinkingLevel(ctx) },
	)
	if err != nil {
		return ModelsConfiguration{}, err
	}
	configuration := ModelsConfiguration{ThinkingLevel: thinking}
	if selected != nil {
		configuration.Model = &ModelRef{Provider: selected.ProviderMeta.ProviderID, ModelId: selected.ID}
	}
	return configuration, nil
}

func (service *modelsService) readCatalog(ctx context.Context) (ModelsCatalog, error) {
	selected, err := service.lane.GetModel(ctx)
	if err != nil {
		return ModelsCatalog{}, err
	}
	var available []*ai.Model
	if service.modelRuntime != nil {
		available = service.modelRuntime.GetAvailableSnapshot()
	}
	catalog := make([]ModelSummary, 0, len(available)+1)
	included := false
	for _, model := range available {
		catalog = append(catalog, modelSummary(model))
		if selected != nil && model.ProviderMeta.ProviderID == selected.ProviderMeta.ProviderID && model.ID == selected.ID {
			included = true
		}
	}
	if selected != nil && !included {
		catalog = append(catalog, modelSummary(selected))
	}
	return ModelsCatalog{Revision: int(service.catalogRevision.Add(1)), AvailableModels: catalog}, nil
}

func modelSummary(model *ai.Model) ModelSummary {
	return ModelSummary{ModelRef: ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID}, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning}
}

func (service *modelsService) publishSnapshot(ctx context.Context, refresh ModelsRefresh) error {
	catalog, configuration, err := modelsReadPair(
		func() (ModelsCatalog, error) { return service.readCatalog(ctx) },
		func() (ModelsConfiguration, error) { return service.readConfiguration(ctx) },
	)
	if err != nil {
		return err
	}
	return service.state.Change(ctx, func(draft *ModelsState) error {
		draft.Catalog = catalog
		draft.Configuration = configuration
		draft.Refresh = refresh
		return nil
	})
}

func (service *modelsService) publishConfiguration(ctx context.Context) error {
	configuration, err := service.readConfiguration(ctx)
	if err != nil {
		return err
	}
	return service.state.Change(ctx, func(draft *ModelsState) error { draft.Configuration = configuration; return nil })
}

func (service *modelsService) GetThinkingLevels(ctx context.Context) ([]ai.ThinkingLevel, error) {
	selected, err := service.lane.GetModel(ctx)
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return []ai.ThinkingLevel{ai.ThinkingOff}, nil
	}
	return slices.Clone(ai.GetSupportedThinkingLevels(selected)), nil
}

func (service *modelsService) CycleThinking(ctx context.Context) error {
	levels, err := service.GetThinkingLevels(ctx)
	if err != nil {
		return err
	}
	current, err := service.lane.GetThinkingLevel(ctx)
	if err != nil {
		return err
	}
	next := ai.ThinkingOff
	if len(levels) > 0 {
		next = levels[(slices.Index(levels, current)+1)%len(levels)]
	}
	if err := service.lane.SetThinkingLevel(ctx, next); err != nil {
		return err
	}
	return service.publishConfiguration(ctx)
}

func (service *modelsService) Refresh(ctx context.Context) error {
	if err := service.state.Change(ctx, func(draft *ModelsState) error { draft.Refresh = ModelsRefresh{Status: "refreshing"}; return nil }); err != nil {
		return err
	}
	refresh := ModelsRefresh{Status: "done"}
	if service.modelRuntime != nil {
		result, err := service.modelRuntime.Refresh(ctx)
		if err != nil {
			return err
		}
		if len(result.Errors) > 0 {
			refresh.Status = "warning"
			refresh.Errors = make(map[string]string, len(result.Errors))
			for id, err := range result.Errors {
				refresh.Errors[id] = err.Error()
			}
		}
	}
	return service.publishSnapshot(ctx, refresh)
}

func (service *modelsService) Select(ctx context.Context, model ModelRef) error {
	var selected *ai.Model
	if service.modelRuntime != nil {
		selected = service.modelRuntime.GetModel(model.Provider, model.ModelId)
	}
	if selected == nil {
		return fmt.Errorf("Unknown model: %s/%s", model.Provider, model.ModelId)
	}
	if err := service.lane.SetModel(ctx, ModelRef{Provider: selected.ProviderMeta.ProviderID, ModelId: selected.ID}); err != nil {
		return err
	}
	if service.settingsManager != nil {
		if err := service.settingsManager.SetDefaultModelAndProvider(selected.ProviderMeta.ProviderID, selected.ID); err != nil {
			return err
		}
		if err := service.settingsManager.Flush(); err != nil {
			return err
		}
	}
	return service.publishConfiguration(ctx)
}

func (service *modelsService) SelectThinking(ctx context.Context, level ai.ThinkingLevel) error {
	levels, err := service.GetThinkingLevels(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(levels, level) {
		names := make([]string, len(levels))
		for i, available := range levels {
			names[i] = string(available)
		}
		return fmt.Errorf("Thinking level %s is unavailable; choose one of: %s", level, strings.Join(names, ", "))
	}
	if err := service.lane.SetThinkingLevel(ctx, level); err != nil {
		return err
	}
	return service.publishConfiguration(ctx)
}

// modelsReadPair owns and joins the concurrent reads made by upstream Promise.all. Both calls receive the invoking operation's context through their closures.
func modelsReadPair[A, B any](first func() (A, error), second func() (B, error)) (A, B, error) {
	var a A
	var b B
	var failure error
	var rejected sync.Once
	recordFailure := func(err error) {
		if err != nil {
			rejected.Do(func() { failure = err })
		}
	}
	var work sync.WaitGroup
	work.Go(func() {
		var err error
		a, err = first()
		recordFailure(err)
	})
	work.Go(func() {
		var err error
		b, err = second()
		recordFailure(err)
	})
	work.Wait()
	return a, b, failure
}

// ModelsServiceFacetOptions selects the lane and optional runtime and settings collaborators.
type ModelsServiceFacetOptions struct {
	Lane            ModelsServiceLane
	ModelRuntime    ModelsServiceModelRuntime
	SettingsManager ModelsServiceSettingsManager
}

// CreateModelsServiceFacet declares a Models provider on the Chord host and activates it with a background context after setup completes.
func CreateModelsServiceFacet(options ModelsServiceFacetOptions) chord.Facet {
	return chord.DefineFacet(chord.Facet{Id: "@pi/models", Setup: func(env *chord.FacetEnvironment) error {
		var stateError error
		runtime := CreateModelsService(options.Lane, options.ModelRuntime, options.SettingsManager, func(initial *ModelsState) pico3.MutableReplicatedStateOf[*ModelsState] {
			state, err := chord.NewReplicatedState(initial)
			stateError = err
			return state
		})
		if stateError != nil {
			return stateError
		}
		if err := chord.ProvideService(env, ModelsDefinition, runtime.Service); err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error { return runtime.Activate(context.Background()) })
	}})
}
