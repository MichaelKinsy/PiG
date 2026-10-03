package services

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// AgentState is the part of the conversation's pi.agent document that the Models service follows. An empty ThinkingLevel is unset and reads as "off".
type AgentState struct {
	Model         *ModelRef
	ThinkingLevel ai.ThinkingLevel
}

// AgentDocument is the conversation's replicated pi.agent document: its configuration follows every change, also those made by other clients. Value is nil until the document exists. Subscribe returns its unsubscribe function. The facet that acquired the document disposes it.
type AgentDocument interface {
	Value() *AgentState
	Subscribe(func(context.Context)) func()
	Dispose()
}

// ConversationConfiguration selects the model and thinking level to apply together; a nil member keeps its current value.
type ConversationConfiguration struct {
	Model         *ModelRef
	ThinkingLevel *ai.ThinkingLevel
}

// ModelsServiceConversation is the configuration boundary the Models service consumes from the root conversation.
type ModelsServiceConversation interface {
	Configure(context.Context, ConversationConfiguration) error
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

// ModelsServiceRuntime binds one Models service to its activation operation and to configuration changes of the agent document.
type ModelsServiceRuntime struct {
	Service  Models
	provider *modelsService
}

type modelsService struct {
	conversation    ModelsServiceConversation
	agent           AgentDocument
	modelRuntime    ModelsServiceModelRuntime
	settingsManager ModelsServiceSettingsManager
	state           chord.MutableReplicatedStateOf[*ModelsState]
	catalogRevision atomic.Int64
}

// CreateModelsService creates an inactive service over one conversation. Its configuration starts from the agent document and follows it after SyncConfiguration; state allocation and publication belong to the caller's Chord host.
func CreateModelsService(conversation ModelsServiceConversation, agent AgentDocument, modelRuntime ModelsServiceModelRuntime, settingsManager ModelsServiceSettingsManager, createState func(*ModelsState) chord.MutableReplicatedStateOf[*ModelsState]) *ModelsServiceRuntime {
	provider := &modelsService{
		conversation:    conversation,
		agent:           agent,
		modelRuntime:    modelRuntime,
		settingsManager: settingsManager,
	}
	provider.state = createState(&ModelsState{
		Catalog:       ModelsCatalog{AvailableModels: []ModelSummary{}},
		Configuration: configurationOf(agent.Value()),
		Refresh:       ModelsRefresh{Status: "idle"},
	})
	return &ModelsServiceRuntime{Service: provider, provider: provider}
}

// Activate reads the current catalog and publishes it with an idle refresh. Upstream reads and publishes in one synchronous turn (models-provider.ts:114-120), so catalog revisions publish in order; the read runs inside the change, which the state serializes, so concurrent calls keep that order.
func (runtime *ModelsServiceRuntime) Activate(ctx context.Context) error {
	return runtime.provider.state.Change(ctx, func(draft *ModelsState) error {
		draft.Catalog = runtime.provider.readCatalog()
		draft.Refresh = ModelsRefresh{Status: "idle"}
		return nil
	})
}

// SyncConfiguration publishes the agent document's model and thinking level when they changed.
func (runtime *ModelsServiceRuntime) SyncConfiguration(ctx context.Context) error {
	next := configurationOf(runtime.provider.agent.Value())
	current := runtime.provider.state.Value().Configuration
	if sameModelRef(current.Model, next.Model) && current.ThinkingLevel == next.ThinkingLevel {
		return nil
	}
	return runtime.provider.state.Change(ctx, func(draft *ModelsState) error { draft.Configuration = next; return nil })
}

func sameModelRef(left, right *ModelRef) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func configurationOf(value *AgentState) ModelsConfiguration {
	configuration := ModelsConfiguration{ThinkingLevel: ai.ThinkingOff}
	if value == nil {
		return configuration
	}
	if value.Model != nil {
		configuration.Model = &ModelRef{Provider: value.Model.Provider, ModelId: value.Model.ModelId}
	}
	if value.ThinkingLevel != "" {
		configuration.ThinkingLevel = value.ThinkingLevel
	}
	return configuration
}

func (service *modelsService) State() chord.ReplicatedStateOf[*ModelsState] { return service.state }

func (service *modelsService) currentThinking() ai.ThinkingLevel {
	return configurationOf(service.agent.Value()).ThinkingLevel
}

// selectedModel is the runtime's model for the agent document's reference; nil when none is selected or the runtime does not know it.
func (service *modelsService) selectedModel() *ai.Model {
	value := service.agent.Value()
	if value == nil || value.Model == nil || service.modelRuntime == nil {
		return nil
	}
	return service.modelRuntime.GetModel(value.Model.Provider, value.Model.ModelId)
}

func (service *modelsService) readCatalog() ModelsCatalog {
	selected := service.selectedModel()
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
	return ModelsCatalog{Revision: int(service.catalogRevision.Add(1)), AvailableModels: catalog}
}

func modelSummary(model *ai.Model) ModelSummary {
	return ModelSummary{ModelRef: ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID}, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning}
}

func (service *modelsService) GetThinkingLevels(context.Context) ([]ai.ThinkingLevel, error) {
	selected := service.selectedModel()
	if selected == nil {
		return []ai.ThinkingLevel{ai.ThinkingOff}, nil
	}
	return slices.Clone(ai.GetSupportedThinkingLevels(selected)), nil
}

func (service *modelsService) CycleThinking(ctx context.Context) error {
	levels, _ := service.GetThinkingLevels(ctx)
	next := ai.ThinkingOff
	if len(levels) > 0 {
		next = levels[(slices.Index(levels, service.currentThinking())+1)%len(levels)]
	}
	return service.conversation.Configure(ctx, ConversationConfiguration{ThinkingLevel: &next})
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
	// Read inside the change for the ordering Activate keeps (models-provider.ts:87-91).
	return service.state.Change(ctx, func(draft *ModelsState) error {
		draft.Catalog = service.readCatalog()
		draft.Refresh = refresh
		return nil
	})
}

func (service *modelsService) Select(ctx context.Context, model ModelRef) error {
	var selected *ai.Model
	if service.modelRuntime != nil {
		selected = service.modelRuntime.GetModel(model.Provider, model.ModelId)
	}
	if selected == nil {
		return fmt.Errorf("Unknown model: %s/%s", model.Provider, model.ModelId)
	}
	thinking := ai.ClampThinkingLevel(selected, service.currentThinking())
	if err := service.conversation.Configure(ctx, ConversationConfiguration{Model: &ModelRef{Provider: selected.ProviderMeta.ProviderID, ModelId: selected.ID}, ThinkingLevel: &thinking}); err != nil {
		return err
	}
	if service.settingsManager == nil {
		return nil
	}
	if err := service.settingsManager.SetDefaultModelAndProvider(selected.ProviderMeta.ProviderID, selected.ID); err != nil {
		return err
	}
	return service.settingsManager.Flush()
}

func (service *modelsService) SelectThinking(ctx context.Context, level ai.ThinkingLevel) error {
	levels, _ := service.GetThinkingLevels(ctx)
	if !slices.Contains(levels, level) {
		names := make([]string, len(levels))
		for i, available := range levels {
			names[i] = string(available)
		}
		return fmt.Errorf("Thinking level %s is unavailable; choose one of: %s", level, strings.Join(names, ", "))
	}
	return service.conversation.Configure(ctx, ConversationConfiguration{ThinkingLevel: &level})
}

// ModelsServiceFacetOptions selects the conversation, its agent document and the optional runtime and settings collaborators. The facet owns Agent and disposes it with the host.
type ModelsServiceFacetOptions struct {
	Conversation    ModelsServiceConversation
	Agent           AgentDocument
	ModelRuntime    ModelsServiceModelRuntime
	SettingsManager ModelsServiceSettingsManager
}

// CreateModelsServiceFacet declares a Models provider on the Chord host. After setup it publishes the catalog with a background context and then follows the agent document, so configuration changes made by any client reach the replicated state.
func CreateModelsServiceFacet(options ModelsServiceFacetOptions) chord.Facet {
	return chord.DefineFacet(chord.Facet{Id: "@pi/models", Setup: func(env *chord.FacetEnvironment) error {
		if err := env.Own(func(context.Context) error { options.Agent.Dispose(); return nil }); err != nil {
			return err
		}
		var stateError error
		runtime := CreateModelsService(options.Conversation, options.Agent, options.ModelRuntime, options.SettingsManager, func(initial *ModelsState) chord.MutableReplicatedStateOf[*ModelsState] {
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
		return env.OnActivate(func(context.Context) error {
			if err := runtime.Activate(context.Background()); err != nil {
				return err
			}
			unsubscribe := options.Agent.Subscribe(func(ctx context.Context) { _ = runtime.SyncConfiguration(ctx) })
			return env.Own(func(context.Context) error { unsubscribe(); return nil })
		})
	}})
}
