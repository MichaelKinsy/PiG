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
	Name          string
	BaseURL       string
	APIKey        string
	API           ai.API
	StreamSimple  ai.ModelsStreamFunction
	Headers       map[string]string
	AuthHeader    *bool
	OAuth         *ExtensionOAuthConfig
	Models        []*ai.Model
	RefreshModels func(ai.RefreshModelsContext) ([]*ai.Model, error)
}

// NativeModelEntry lowers native model data to the configured backend representation without resolving credentials.
func NativeModelEntry(model *ai.Model) ModelEntry {
	cost := model.CostRates()
	return ModelEntry{ProviderID: model.ProviderMeta.ProviderID, ModelID: model.ID, DisplayName: model.DisplayName, API: string(model.ProviderMeta.API), BaseURL: model.ProviderMeta.BaseURL, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "", Input: slices.Clone(model.Input), ContextWindow: model.Capabilities.ContextWindow, MaxTokens: model.Capabilities.MaxOutputTokens, InputCost: cost.Input, OutputCost: cost.Output, CacheReadCost: cost.CacheRead, CacheWriteCost: cost.CacheWrite, CostTiers: slices.Clone(cost.Tiers), ModelHeaders: maps.Clone(model.ProviderMeta.Headers), Headers: maps.Clone(model.ProviderMeta.Headers), Compat: mergeCompat((*providerCompat)(model.ProviderMeta.Compat), nil), ThinkingLevelMap: cloneThinkingLevelMap(model.ThinkingLevelMap), SamplingParams: maps.Clone(model.SamplingParams), PromptCache: maps.Clone(model.PromptCache), InputLimits: model.InputLimits.Clone()}
}

func nativeModelFromEntry(entry ModelEntry) *ai.Model {
	generated := ai.GeneratedModel{ID: entry.ModelID, Provider: entry.ProviderID, DisplayName: entry.DisplayName, API: ai.API(entry.API), BaseURL: entry.BaseURL, Headers: entry.ModelHeaders, Compat: entry.Compat, Reasoning: entry.Reasoning, Capabilities: entry.Input, ContextWindow: entry.ContextWindow, MaxOutputTokens: entry.MaxTokens, InputCostPerMTokens: entry.InputCost, OutputCostPerMTokens: entry.OutputCost, CacheReadCost: entry.CacheReadCost, CacheWriteCost: entry.CacheWriteCost, Tiers: entry.CostTiers, ThinkingLevelMap: entry.ThinkingLevelMap, SamplingParams: entry.SamplingParams, PromptCache: entry.PromptCache, InputLimits: entry.InputLimits}
	model := generated.ToModel()
	model.Capabilities = generated.ToCapabilities()
	return model
}

func applyNativeExtensionModels(id string, base, definitions []*ai.Model, extension ProviderConfigInput) ([]*ai.Model, error) {
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
	var refreshed []*ai.Model
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
	if _, err := currentModels(); err != nil {
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
	provider.Auth = ai.ProviderAuth{APIKey: composeAPIKeyAuth(base.ID, auth, authConfig), OAuth: composeOAuthAuth(base.ID, auth.OAuth, authConfig)}
	if base.RefreshModels != nil || (extension != nil && (extension.RefreshModels != nil || extension.OAuth != nil && extension.OAuth.ModifyModels != nil)) {
		provider.RefreshModels = func(ctx ai.RefreshModelsContext) error {
			if base.RefreshModels != nil {
				if err := base.RefreshModels(ctx); err != nil {
					return err
				}
			}
			var models []*ai.Model
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
		provider.StreamSimple = extension.StreamSimple
		provider.Stream = extension.StreamSimple
	}
	return provider, nil
}
