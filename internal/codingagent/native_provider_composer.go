package codingagent

// Ports packages/coding-agent/src/core/provider-composer.ts.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

// ExtensionOAuthConfig adapts the legacy provider-registration callbacks to native provider auth.
type ExtensionOAuthConfig struct {
	Name           string
	IsSubscription bool
	Login          func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error)
	RefreshToken   func(context.Context, ai.Credential) (ai.Credential, error)
	GetAPIKey      func(ai.Credential) string
	ModifyModels   func([]*ai.Model, ai.Credential) []*ai.Model
}

// ProviderConfigInput is the core registration input; Models nil preserves the base catalog and an empty slice replaces it.
type ProviderConfigInput struct {
	Name         string
	BaseURL      string
	APIKey       string
	API          ai.API
	StreamSimple ai.ModelsStreamFunction
	Headers      map[string]string
	AuthHeader   *bool
	OAuth        *ExtensionOAuthConfig
	// Models are chat, image and classifier model definitions; a model without a type is a chat model.
	Models []ai.AnyModel
	// Images and Classifiers are the implementations of the image and classifier models Models declares.
	Images        ai.ProviderImageAPIMap
	Classifiers   ai.ProviderClassifierMap
	RefreshModels func(ai.RefreshModelsContext) ([]ai.AnyModel, error)
}

// chatModelsOf keeps the chat models of a mixed list.
func chatModelsOf(models []ai.AnyModel) []*ai.Model {
	var chat []*ai.Model
	for _, model := range models {
		if typed, ok := model.(*ai.Model); ok && ai.IsModelType(typed, ai.ModelTypeChat) {
			chat = append(chat, typed)
		}
	}
	return chat
}

// NativeModelEntry lowers native model data to the configured backend representation without resolving credentials.
func NativeModelEntry(model *ai.Model) ModelEntry {
	cost := model.CostRates()
	return ModelEntry{ProviderID: model.ProviderMeta.ProviderID, ModelID: model.ID, DisplayName: model.DisplayName, API: string(model.ProviderMeta.API), BaseURL: model.ProviderMeta.BaseURL, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "", Input: slices.Clone(model.Input), ContextWindow: model.Capabilities.ContextWindow, MaxTokens: model.Capabilities.MaxOutputTokens, InputCost: cost.Input, OutputCost: cost.Output, CacheReadCost: cost.CacheRead, CacheWriteCost: cost.CacheWrite, CostTiers: slices.Clone(cost.Tiers), ModelHeaders: maps.Clone(model.ProviderMeta.Headers), Headers: maps.Clone(model.ProviderMeta.Headers), Compat: mergeCompat((*providerCompat)(model.ProviderMeta.Compat), nil), ThinkingLevelMap: cloneThinkingLevelMap(model.ThinkingLevelMap), SamplingParams: maps.Clone(model.SamplingParams), SamplingParamsByThinkingLevel: cloneSamplingParamsByThinkingLevel(model.SamplingParamsByThinkingLevel), PromptCache: maps.Clone(model.PromptCache), InputLimits: model.InputLimits.Clone()}
}

func nativeModelFromEntry(entry ModelEntry) *ai.Model {
	generated := ai.GeneratedModel{ID: entry.ModelID, Provider: entry.ProviderID, DisplayName: entry.DisplayName, API: ai.API(entry.API), BaseURL: entry.BaseURL, Headers: entry.ModelHeaders, Compat: entry.Compat, Reasoning: entry.Reasoning, Capabilities: entry.Input, ContextWindow: entry.ContextWindow, MaxOutputTokens: entry.MaxTokens, InputCostPerMTokens: entry.InputCost, OutputCostPerMTokens: entry.OutputCost, CacheReadCost: entry.CacheReadCost, CacheWriteCost: entry.CacheWriteCost, Tiers: entry.CostTiers, ThinkingLevelMap: entry.ThinkingLevelMap, SamplingParams: entry.SamplingParams, PromptCache: entry.PromptCache, InputLimits: entry.InputLimits}
	model := generated.ToModel()
	model.Capabilities = generated.ToCapabilities()
	model.SamplingParamsByThinkingLevel = cloneSamplingParamsByThinkingLevel(entry.SamplingParamsByThinkingLevel)
	return model
}

func applyNativeExtensionModels(id string, base []*ai.Model, anyDefinitions []ai.AnyModel, extension ProviderConfigInput) ([]*ai.Model, error) {
	definitions := chatModelsOf(anyDefinitions)
	models := make([]*ai.Model, 0, len(definitions))
	for _, definition := range definitions {
		model := new(*definition)
		model.ProviderMeta.ProviderID = id
		model.ProviderMeta.API = ai.API(firstModelValue(string(model.ProviderMeta.API), string(extension.API)))
		model.ProviderMeta.BaseURL = firstModelValue(model.ProviderMeta.BaseURL, extension.BaseURL)
		defaults := findNativeModelDefaults(base, model.ID, model.ProviderMeta.API)
		if defaults != nil {
			model.ProviderMeta.API = ai.API(firstModelValue(string(model.ProviderMeta.API), string(defaults.ProviderMeta.API)))
			model.ProviderMeta.BaseURL = firstModelValue(model.ProviderMeta.BaseURL, defaults.ProviderMeta.BaseURL)
		}
		if model.ProviderMeta.API == "" {
			return nil, fmt.Errorf(`Provider %s, model %s: no "api" specified. Set at provider or model level.`, id, model.ID)
		}
		if model.ProviderMeta.BaseURL == "" {
			return nil, fmt.Errorf(`Provider %s: "baseUrl" is required when defining custom models.`, id)
		}
		// upstream: packages/coding-agent/src/core/provider-composer.ts:applyExtension sets headers: undefined; request headers come from ModelRuntime.getAuth.
		model.ProviderMeta.Headers = nil
		models = append(models, model)
	}
	return models, nil
}

func findNativeModelDefaults(models []*ai.Model, id string, api ai.API) *ai.Model {
	for _, model := range models {
		if model.ID == id {
			return model
		}
	}
	if api != "" {
		for _, model := range models {
			if model.ProviderMeta.API == api {
				return model
			}
		}
	}
	for _, model := range models {
		if model.ProviderMeta.API == ai.APIOpenAICompletions {
			return model
		}
	}
	if len(models) > 0 {
		return models[0]
	}
	return nil
}

// anyModelBaseURL reads the base URL of a model of any type.
func anyModelBaseURL(model ai.AnyModel) string {
	switch typed := model.(type) {
	case *ai.Model:
		return typed.ProviderMeta.BaseURL
	case *ai.ImageModel:
		return typed.BaseURL
	case *ai.ClassifierModel:
		return typed.BaseURL
	}
	return ""
}

// withBaseURL copies a non-chat model with another base URL; chat models are returned unchanged.
func withBaseURL(model ai.AnyModel, baseURL string) ai.AnyModel {
	switch typed := model.(type) {
	case *ai.ImageModel:
		copy := *typed
		copy.BaseURL = baseURL
		return &copy
	case *ai.ClassifierModel:
		copy := *typed
		copy.BaseURL = baseURL
		return &copy
	}
	return model
}

// allProviderModels lists every model of a provider: its GetAllModels list, or its chat list (provider-composer.ts getAllProviderModels).
func allProviderModels(provider *ai.ModelsProvider) ([]ai.AnyModel, error) {
	if provider == nil {
		return nil, nil
	}
	if provider.GetAllModels != nil {
		return provider.GetAllModels()
	}
	chat, err := provider.GetModels()
	if err != nil {
		return nil, err
	}
	return ai.AnyModels(chat), nil
}

// findExtensionModelDefaults finds the model of the definition's type that supplies its missing api and base URL (provider-composer.ts findExtensionModelDefaults).
func findExtensionModelDefaults(models []ai.AnyModel, definition ai.AnyModel) ai.AnyModel {
	kind := definition.ModelType()
	var candidates []ai.AnyModel
	for _, model := range models {
		if ai.IsModelType(model, kind) {
			candidates = append(candidates, model)
		}
	}
	for _, model := range candidates {
		if model.ModelID() == definition.ModelID() {
			return model
		}
	}
	if api := anyModelAPI(definition); api != "" {
		for _, model := range candidates {
			if anyModelAPI(model) == api {
				return model
			}
		}
	}
	if kind == ai.ModelTypeChat {
		for _, model := range candidates {
			if anyModelAPI(model) == string(ai.APIOpenAICompletions) {
				return model
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return nil
}

func anyModelAPI(model ai.AnyModel) string {
	switch typed := model.(type) {
	case *ai.Model:
		return string(typed.ProviderMeta.API)
	case *ai.ImageModel:
		return string(typed.API)
	case *ai.ClassifierModel:
		return string(typed.API)
	}
	return ""
}

// extensionNonChatModel materializes an extension image or classifier definition (provider-composer.ts extensionModelFromDefinition).
func extensionNonChatModel(id string, models []ai.AnyModel, extension ProviderConfigInput, definition ai.AnyModel) (ai.AnyModel, error) {
	defaults := findExtensionModelDefaults(models, definition)
	api := anyModelAPI(definition)
	if api == "" && defaults != nil {
		api = anyModelAPI(defaults)
	}
	if api == "" {
		return nil, fmt.Errorf(`Provider %s, model %s: no "api" specified. Set it at model level.`, id, definition.ModelID())
	}
	baseURL := firstModelValue(anyModelBaseURL(definition), extension.BaseURL)
	if baseURL == "" && defaults != nil {
		baseURL = anyModelBaseURL(defaults)
	}
	if baseURL == "" {
		return nil, fmt.Errorf(`Provider %s: "baseUrl" is required when defining custom models.`, id)
	}
	// upstream: provider-composer.ts extensionModelFromDefinition sets headers: undefined; request headers come from ModelRuntime.getAuth.
	switch typed := definition.(type) {
	case *ai.ImageModel:
		copy := *typed
		copy.API, copy.Provider, copy.BaseURL, copy.Headers = ai.ImageAPI(api), id, baseURL, nil
		return &copy, nil
	case *ai.ClassifierModel:
		copy := *typed
		copy.API, copy.Provider, copy.BaseURL, copy.Headers = ai.ClassifierAPI(api), id, baseURL, nil
		return &copy, nil
	}
	return nil, fmt.Errorf("Provider %s, model %s: unsupported model type", id, definition.ModelID())
}

func (r *ModelRegistry) nativeModelConfig(id string) (providerConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.config == nil {
		return providerConfig{}, false
	}
	cfg, ok := r.config.Providers[id]
	return cfg, ok
}

func (r *ModelRegistry) composeNativeProvider(base *ai.ModelsProvider, extension *ProviderConfigInput) (*ai.ModelsProvider, error) {
	config, configured := r.nativeModelConfig(base.ID)
	if !configured && extension == nil {
		return base, nil
	}
	var mu sync.RWMutex
	var refreshed []ai.AnyModel
	var oauthCredential *ai.Credential
	configuredModels := func() ([]*ai.Model, error) {
		models, err := base.GetModels()
		if err != nil {
			return nil, err
		}
		models = slices.Clone(models)
		if configured {
			for i, model := range models {
				entry := NativeModelEntry(model)
				if config.OAuth == nil || config.OAuth.Kind != "radius" {
					entry.BaseURL = firstModelValue(config.BaseURL, entry.BaseURL)
				}
				entry.Compat = mergeCompat((*providerCompat)(entry.Compat), config.Compat)
				models[i] = nativeModelFromEntry(entry)
			}
			for _, definition := range config.Models {
				entry := modelDefinitionEntry(base.ID, config, definition)
				index := slices.IndexFunc(models, func(model *ai.Model) bool { return model.ID == definition.ID })
				defaults := findNativeModelDefaults(models, definition.ID, ai.API(firstModelValue(definition.API, config.API)))
				if defaults != nil {
					entry.API = firstModelValue(entry.API, string(defaults.ProviderMeta.API))
					entry.BaseURL = firstModelValue(entry.BaseURL, defaults.ProviderMeta.BaseURL)
				}
				if entry.API == "" || entry.BaseURL == "" {
					return nil, fmt.Errorf("Provider %s, model %s: api and baseUrl are required", base.ID, definition.ID)
				}
				model := nativeModelFromEntry(entry)
				if index >= 0 {
					models[index] = model
				} else {
					models = append(models, model)
				}
			}
		}
		return models, nil
	}
	currentModels := func() ([]*ai.Model, error) {
		models, err := configuredModels()
		if err != nil {
			return nil, err
		}
		mu.RLock()
		dynamic := refreshed
		credential := oauthCredential
		mu.RUnlock()
		if extension != nil {
			definitions := extension.Models
			if dynamic != nil {
				definitions = dynamic
			}
			if definitions != nil {
				models, err = applyNativeExtensionModels(base.ID, models, definitions, *extension)
				if err != nil {
					return nil, err
				}
			} else if extension.BaseURL != "" {
				for i, model := range models {
					copy := new(*model)
					copy.ProviderMeta.BaseURL = extension.BaseURL
					models[i] = copy
				}
			}
			if credential != nil && extension.OAuth != nil && extension.OAuth.ModifyModels != nil {
				models = extension.OAuth.ModifyModels(models, *credential)
			}
		}
		for i, model := range models {
			if override, ok := config.ModelOverrides[model.ID]; ok {
				entry := NativeModelEntry(model)
				applyModelOverride(&entry, override)
				models[i] = nativeModelFromEntry(entry)
			}
		}
		return models, nil
	}
	// configuredAllModels applies the models.json provider baseUrl to the base models of every type; chat models come from currentModels (provider-composer.ts applyModelsJson).
	configuredAllModels := func() ([]ai.AnyModel, error) {
		models, err := allProviderModels(base)
		if err != nil || !configured || config.OAuth != nil && config.OAuth.Kind == "radius" {
			return models, err
		}
		models = slices.Clone(models)
		for i, model := range models {
			if !ai.IsModelType(model, ai.ModelTypeChat) {
				models[i] = withBaseURL(model, firstModelValue(config.BaseURL, anyModelBaseURL(model)))
			}
		}
		return models, nil
	}
	// currentAllModels lists every model type in provider-composer.ts getAllModels order: the extension definitions, else the models.json-composed base models; with an OAuth credential, the modifyModels chat projection comes first and the other types follow.
	currentAllModels := func() ([]ai.AnyModel, error) {
		chat, err := currentModels()
		if err != nil {
			return nil, err
		}
		configuredAll, err := configuredAllModels()
		if err != nil {
			return nil, err
		}
		mu.RLock()
		dynamic := refreshed
		credential := oauthCredential
		mu.RUnlock()
		var definitions []ai.AnyModel
		if extension != nil {
			definitions = extension.Models
			if dynamic != nil {
				definitions = dynamic
			}
		}
		var all []ai.AnyModel
		if definitions != nil {
			next := 0
			for _, definition := range definitions {
				if ai.IsModelType(definition, ai.ModelTypeChat) {
					if next < len(chat) {
						all = append(all, chat[next])
						next++
					}
					continue
				}
				model, err := extensionNonChatModel(base.ID, configuredAll, *extension, definition)
				if err != nil {
					return nil, err
				}
				all = append(all, model)
			}
		} else {
			chatByID := make(map[string]*ai.Model, len(chat))
			for _, model := range chat {
				chatByID[model.ID] = model
			}
			emitted := map[string]bool{}
			for _, model := range configuredAll {
				if ai.IsModelType(model, ai.ModelTypeChat) {
					if replacement := chatByID[model.ModelID()]; replacement != nil {
						all = append(all, replacement)
						emitted[replacement.ID] = true
					}
					continue
				}
				if extension != nil {
					model = withBaseURL(model, firstModelValue(extension.BaseURL, anyModelBaseURL(model)))
				}
				all = append(all, model)
			}
			for _, model := range chat {
				if !emitted[model.ID] {
					all = append(all, model)
				}
			}
		}
		if credential != nil && extension != nil && extension.OAuth != nil && extension.OAuth.ModifyModels != nil {
			projected := ai.AnyModels(chat)
			for _, model := range all {
				if !ai.IsModelType(model, ai.ModelTypeChat) {
					projected = append(projected, model)
				}
			}
			all = projected
		}
		return all, nil
	}
	if _, err := currentAllModels(); err != nil {
		return nil, err
	}
	authConfig := config
	auth := base.Auth
	if extension != nil {
		if extension.APIKey != "" {
			authConfig.APIKey = extension.APIKey
		}
		if extension.AuthHeader != nil {
			authConfig.AuthHeader = extension.AuthHeader
		}
		authConfig.Headers = maps.Clone(config.Headers)
		if authConfig.Headers == nil {
			authConfig.Headers = map[string]*string{}
		}
		for key, value := range extension.Headers {
			authConfig.Headers[key] = new(value)
		}
		if extension.OAuth != nil {
			legacy := extension.OAuth
			auth.OAuth = &ai.OAuthAuth{Name: legacy.Name, IsSubscription: legacy.IsSubscription, Login: adaptExtensionOAuthLogin(legacy.Login), Refresh: legacy.RefreshToken, ToAuth: func(credential ai.Credential) (ai.ModelAuth, error) {
				return ai.ModelAuth{APIKey: legacy.GetAPIKey(credential)}, nil
			}}
		}
	}
	provider := new(*base)
	provider.Name = firstModelValue(config.Name, base.Name)
	if extension != nil {
		oauthName := ""
		if extension.OAuth != nil {
			oauthName = extension.OAuth.Name
		}
		provider.Name = firstModelValue(extension.Name, provider.Name, oauthName)
	}
	provider.Name = firstModelValue(provider.Name, base.ID)
	provider.GetModels = currentModels
	provider.GetAllModels = currentAllModels
	provider.Auth = ai.ProviderAuth{APIKey: composeAPIKeyAuth(base.ID, auth, authConfig), OAuth: composeOAuthAuth(base.ID, auth.OAuth, authConfig)}
	if base.RefreshModels != nil || (extension != nil && (extension.RefreshModels != nil || extension.OAuth != nil && extension.OAuth.ModifyModels != nil)) {
		provider.RefreshModels = func(ctx ai.RefreshModelsContext) error {
			if base.RefreshModels != nil {
				if err := base.RefreshModels(ctx); err != nil {
					return err
				}
			}
			var models []ai.AnyModel
			if extension != nil && extension.RefreshModels != nil {
				var err error
				models, err = extension.RefreshModels(ctx)
				if err != nil {
					return err
				}
			}
			if ctx.Signal.Err() != nil {
				return context.Cause(ctx.Signal)
			}
			if models != nil {
				defaults, err := configuredModels()
				if err != nil {
					return err
				}
				if _, err := applyNativeExtensionModels(base.ID, defaults, models, *extension); err != nil {
					return err
				}
				configuredAll, err := configuredAllModels()
				if err != nil {
					return err
				}
				for _, definition := range models {
					if ai.IsModelType(definition, ai.ModelTypeChat) {
						continue
					}
					if _, err := extensionNonChatModel(base.ID, configuredAll, *extension, definition); err != nil {
						return err
					}
				}
			}
			_, err := ctx.Publish(ai.ModelsPublication{Update: func() {
				mu.Lock()
				defer mu.Unlock()
				if models != nil {
					refreshed = slices.Clone(models)
				}
				oauthCredential = nil
				if ctx.Credential != nil && ctx.Credential.Type == ai.CredentialOAuth {
					oauthCredential = new(*ctx.Credential)
				}
			}})
			return err
		}
	}
	if extension != nil && extension.StreamSimple != nil {
		// upstream: provider-composer.ts:composeModelProvider runs the extension's streamSimple only for a model whose api is the extension's api; another model reaches the base provider or its API implementation.
		custom, api := extension.StreamSimple, extension.API
		provider.Stream = composedExtensionStream(custom, api, base.Stream)
		provider.StreamSimple = composedExtensionStream(custom, api, base.StreamSimple)
	}
	if baseImages := base.GenerateImages; baseImages != nil || (extension != nil && len(extension.Images) > 0) {
		provider.GenerateImages = func(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
			if extension != nil {
				if implementation := extension.Images[model.API]; implementation != nil && implementation.GenerateImages != nil {
					return implementation.GenerateImages(ctx, model, request, options)
				}
			}
			if baseImages != nil {
				return baseImages(ctx, model, request, options)
			}
			// upstream: provider-composer.ts:composeModelProvider resolves imageErrorResult for an image API without an implementation.
			return ai.ImageErrorResult(model, fmt.Errorf("Provider %s has no image implementation for %q", base.ID, model.API), false), nil
		}
	}
	if baseClassify := base.Classify; baseClassify != nil || (extension != nil && len(extension.Classifiers) > 0) {
		provider.Classify = func(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
			if extension != nil {
				if implementation := extension.Classifiers[model.API]; implementation != nil && implementation.Classify != nil {
					return implementation.Classify(ctx, model, request, options)
				}
			}
			if baseClassify != nil {
				return baseClassify(ctx, model, request, options)
			}
			// upstream: provider-composer.ts:composeModelProvider resolves classifierErrorResult for a classifier API without an implementation.
			return ai.ClassifierErrorResult(model, fmt.Errorf("Provider %s has no classifier implementation for %q", base.ID, model.API), false), nil
		}
	}
	return provider, nil
}

// composedExtensionStream is the stream function of a provider an extension composed with its own streamSimple: a model of the extension's api runs that callback, and any other model runs the base stream function.
// upstream: provider-composer.ts:composeModelProvider (streamWith)
func composedExtensionStream(custom ai.ModelsStreamFunction, api ai.API, base ai.ModelsStreamFunction) ai.ModelsStreamFunction {
	return func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		if model != nil && model.ProviderMeta.API == api {
			return custom(ctx, model, transcript, options)
		}
		if base == nil {
			var modelAPI ai.API
			if model != nil {
				modelAPI = model.ProviderMeta.API
			}
			return nil, fmt.Errorf("No API provider registered for api: %s", modelAPI)
		}
		return base(ctx, model, transcript, options)
	}
}
