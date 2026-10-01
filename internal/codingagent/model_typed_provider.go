package codingagent

// Ports the typed model surface of packages/coding-agent/src/core/model-runtime.ts: the built-in provider list with its image models, and the composed provider each typed accessor reads.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

var builtinImageModels struct {
	once sync.Once
	byID map[string][]*ai.ImageModel
}

// builtinImageModelsOf returns the shared built-in image model pointers of one provider, so repeated reads see the same models.
func builtinImageModelsOf(providerID string) []*ai.ImageModel {
	builtinImageModels.once.Do(func() {
		builtinImageModels.byID = map[string][]*ai.ImageModel{}
		for _, provider := range ai.GetImageProviders() {
			for _, model := range ai.GetImageModels(provider) {
				builtinImageModels.byID[provider] = append(builtinImageModels.byID[provider], new(model))
			}
		}
	})
	return builtinImageModels.byID[providerID]
}

var builtinClassifierModels struct {
	once sync.Once
	byID map[string][]*ai.ClassifierModel
}

// builtinClassifierModelsOf returns the shared built-in classifier model pointers of one provider, so repeated reads see the same models.
func builtinClassifierModelsOf(providerID string) []*ai.ClassifierModel {
	builtinClassifierModels.once.Do(func() {
		builtinClassifierModels.byID = map[string][]*ai.ClassifierModel{}
		for _, provider := range ai.ListProviders() {
			builtinClassifierModels.byID[provider] = ai.GetBuiltinClassifierModels(provider)
		}
	})
	return builtinClassifierModels.byID[providerID]
}

// builtinTypedBase assembles a built-in provider with its chat, image and classifier models. It returns nil when the provider has no built-in model of any type.
func builtinTypedBase(id string, fallback ai.ModelsStreamFunction) *ai.ModelsProvider {
	chat := ai.ListModels(id)
	images := builtinImageModelsOf(id)
	classifiers := builtinClassifierModelsOf(id)
	if len(chat) == 0 && len(images) == 0 && len(classifiers) == 0 {
		return nil
	}
	auth, _ := ai.BuiltinProviderAuth(id)
	chatModels := func() []*ai.Model {
		models := make([]*ai.Model, 0, len(chat))
		for _, generated := range ai.ListModels(id) {
			model := generated.ToModel()
			model.Capabilities = generated.ToCapabilities()
			models = append(models, model)
		}
		return models
	}
	base := &ai.ModelsProvider{ID: id, Name: ai.ProviderDisplayName(id), Auth: auth, GetModels: func() ([]*ai.Model, error) { return chatModels(), nil }, Stream: fallback, StreamSimple: fallback}
	base.GetAllModels = func() ([]ai.AnyModel, error) {
		all := ai.AnyModels(chatModels())
		for _, image := range images {
			all = append(all, image)
		}
		for _, classifier := range classifiers {
			all = append(all, classifier)
		}
		return all, nil
	}
	if implementations := ai.BuiltinClassifiers(id); len(implementations) > 0 {
		base.Classify = func(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
			implementation := implementations[model.API]
			if implementation == nil || implementation.Classify == nil {
				// upstream: packages/ai/src/models.ts:createProvider resolves an error result for a classifier API the provider lacks.
				return ai.ClassifierErrorResult(model, ai.NewModelsError(ai.ModelsErrorProvider, fmt.Sprintf("Provider %s has no classifier implementation for %q", id, model.API), nil), false), nil
			}
			return implementation.Classify(ctx, model, request, options)
		}
	}
	if len(images) > 0 {
		ai.RegisterBuiltInImagesAPIProviders()
		base.GenerateImages = func(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
			implementation, ok := ai.GetImagesAPIProvider(model.API)
			if !ok {
				// upstream: packages/ai/src/models.ts:createProvider resolves an error result for an image API the provider lacks.
				return ai.ImageErrorResult(model, ai.NewModelsError(ai.ModelsErrorProvider, fmt.Sprintf("Provider %s has no image generation implementation for %q", id, model.API), nil), false), nil
			}
			return implementation.GenerateImages(ctx, *model, request, options), nil
		}
	}
	return base
}

// GetTypedProvider returns the composed provider with the given ID: a registered native or composed provider, else the built-in provider composed with models.json and legacy registrations, streaming through fallback. It returns nil for an unknown provider.
func (r *ModelRegistry) GetTypedProvider(id string, fallback ai.ModelsStreamFunction) *ai.ModelsProvider {
	if provider := r.GetProvider(id); provider != nil {
		return provider
	}
	base := r.builtinBase(id, fallback)
	r.mu.RLock()
	dynamic, registered := r.dynamic[id]
	configured := false
	if r.config != nil {
		_, configured = r.config.Providers[id]
	}
	r.mu.RUnlock()
	if base == nil {
		if !registered && !configured {
			return nil
		}
		base = &ai.ModelsProvider{ID: id, Name: id, GetModels: func() ([]*ai.Model, error) { return nil, nil }, Stream: fallback, StreamSimple: fallback}
	}
	var input *ProviderConfigInput
	if registered {
		input = new(providerModelInput(id, dynamic))
	}
	provider, err := r.composeNativeProvider(base, input)
	if err != nil {
		return nil
	}
	// A native Provider object owns the image and classifier operations of all of its models (pi-ai Provider.generateImages and Provider.classify).
	if native := r.NativeProvider(id); native != nil {
		if native.GenerateImages != nil {
			provider.GenerateImages = native.GenerateImages
		}
		if native.Classify != nil {
			provider.Classify = native.Classify
		}
	}
	return provider
}

// typedModelProviderIDs lists, in provider order, the providers that can hold a non-chat model: those with built-in image or classifier models and those an extension registered.
func (r *ModelRegistry) typedModelProviderIDs() []string {
	r.mu.RLock()
	registered := make(map[string]bool, len(r.dynamic))
	for id := range r.dynamic {
		registered[id] = true
	}
	r.mu.RUnlock()
	var ids []string
	for _, id := range r.modelProviderIDs() {
		if registered[id] || len(builtinImageModelsOf(id)) > 0 || len(builtinClassifierModelsOf(id)) > 0 || r.GetProvider(id) != nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// GetProviderAllModelData lists the models of every type of one provider without resolving credentials.
func (r *ModelRegistry) GetProviderAllModelData(id string) []ai.AnyModel {
	provider := r.GetTypedProvider(id, nil)
	if provider == nil {
		return nil
	}
	models, err := allProviderModels(provider)
	if err != nil {
		return nil
	}
	return models
}

// GetAllProviderModelData lists the models of every type of every provider in provider order.
func (r *ModelRegistry) GetAllProviderModelData() []ai.AnyModel {
	var models []ai.AnyModel
	for _, id := range r.modelProviderIDs() {
		models = append(models, r.GetProviderAllModelData(id)...)
	}
	return models
}

// GetAvailableAllModelDataContext lists the models of every type whose provider has working credentials, checking providers concurrently like GetAvailableModelDataContext. Chat models follow the provider's chat availability; every other model type is kept (models.ts getAllAvailable).
func (r *ModelRegistry) GetAvailableAllModelDataContext(ctx context.Context, providerID string) ([]ai.AnyModel, error) {
	ids := r.modelProviderIDs()
	if providerID != "" {
		ids = []string{providerID}
	}
	available := make([][]ai.AnyModel, len(ids))
	tasks := make([]func(context.Context) error, len(ids))
	for i, id := range ids {
		tasks[i] = func(ctx context.Context) error {
			var err error
			available[i], err = r.availableProviderAllModels(ctx, id)
			return err
		}
	}
	if err := r.AwaitModelTasks(ctx, tasks...); err != nil {
		return nil, err
	}
	result := make([]ai.AnyModel, 0)
	for i := range ids {
		result = append(result, available[i]...)
	}
	return result, nil
}

func (r *ModelRegistry) availableProviderAllModels(ctx context.Context, id string) ([]ai.AnyModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.GetProvider(id) != nil {
		return r.NativeModels().GetAllAvailable(ctx, id)
	}
	check, err := r.CheckRegistryAuth(ctx, id)
	if err != nil || check == nil {
		return nil, err
	}
	filter := r.accountModelFilter(id)
	return slices.DeleteFunc(r.GetProviderAllModelData(id), func(model ai.AnyModel) bool {
		return ai.IsModelType(model, ai.ModelTypeChat) && !filter(model.ModelID())
	}), nil
}

// ResolveRegistryTypedModelAuth resolves request auth for an image or classifier model: provider auth plus the model's own headers and the configured headers of its definition.
func (r *ModelRegistry) ResolveRegistryTypedModelAuth(ctx context.Context, model ai.AnyModel, overrides ...ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	providerID, modelID := model.ProviderID(), model.ModelID()
	result, err := r.ResolveRegistryProviderAuth(ctx, providerID, overrides...)
	if err != nil || result == nil {
		return result, err
	}
	copy := new(*result)
	headerEnv := maps.Clone(result.Env)
	if len(overrides) > 0 && len(overrides[0].Env) > 0 {
		if headerEnv == nil {
			headerEnv = make(map[string]string)
		}
		maps.Copy(headerEnv, overrides[0].Env)
	}
	// models.json definitions and overrides are chat-only. Extension definitions are matched by type and id so colliding models cannot share headers (provider-composer.ts rawModelHeaders).
	var configured []orderedHeaderEntry
	if input := r.GetRegisteredProviderConfig(providerID); input != nil {
		for _, entry := range input.Models {
			if entry.ModelType() != model.ModelType() || entry.ModelID() != modelID {
				continue
			}
			values := map[string]*string{}
			for name, value := range anyModelDefinitionHeaders(entry) {
				values[name] = new(value)
			}
			configured = overlayHeaders(configured, orderedHeaders(values, nil))
			break
		}
	} else if definition, ok := r.registeredModelDefinition(providerID, model.ModelType(), modelID); ok {
		configured = overlayHeaders(configured, orderedHeaders(definition.Headers, definition.headerEntries))
	}
	headers, err := resolveHeadersOrError(configured, fmt.Sprintf(`model "%s/%s"`, providerID, modelID), headerEnv)
	if err != nil {
		return nil, err
	}
	copy.Auth.Headers = ai.MergeProviderHeaders(copy.Auth.Headers, ai.ProviderHeadersFromStrings(anyModelDefinitionHeaders(model)))
	copy.Auth.Headers = mergeConfiguredHeaders(copy.Auth.Headers, configured, headers)
	return copy, nil
}

// registeredModelDefinition finds the model definition of one type and id in a provider config an extension registered (provider-composer.ts rawModelHeaders reads extension.models).
func (r *ModelRegistry) registeredModelDefinition(providerID string, modelType ai.ModelType, modelID string) (modelDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	config, ok := r.dynamic[providerID]
	if !ok {
		return modelDefinition{}, false
	}
	for _, definition := range config.Models {
		definitionType := definition.Type
		if definitionType == "" {
			definitionType = ai.ModelTypeChat
		}
		if definitionType == modelType && definition.ID == modelID {
			return definition, true
		}
	}
	return modelDefinition{}, false
}

// anyModelDefinitionHeaders reads the headers a non-chat model or definition carries.
func anyModelDefinitionHeaders(model ai.AnyModel) map[string]string {
	switch typed := model.(type) {
	case *ai.Model:
		return typed.ProviderMeta.Headers
	case *ai.ImageModel:
		return typed.Headers
	case *ai.ClassifierModel:
		return typed.Headers
	}
	return nil
}
