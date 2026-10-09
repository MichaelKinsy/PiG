package codingagent

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
)

// radiusComposedModelsLocked applies provider-composer.ts applyModelsJson:
// preserve catalog URLs for oauth:radius, upsert definitions, then overrides.
func (r *ModelRegistry) radiusComposedModelsLocked(provider *ai.RadiusProvider) []ModelEntry {
	entries, _ := r.radiusComposeLocked(provider)
	return entries
}

// radiusCompositionFailureLocked is the error applyModelsJson throws for the provider's models.json definitions, or "". Pi records it as a
// composition error and keeps the provider's catalog models without the configuration; getError() reports it as `Provider "<id>": <error>`.
func (r *ModelRegistry) radiusCompositionFailureLocked(provider *ai.RadiusProvider) string {
	_, failure := r.radiusComposeLocked(provider)
	return failure
}

func (r *ModelRegistry) radiusComposeLocked(provider *ai.RadiusProvider) ([]ModelEntry, string) {
	models := provider.GetModels()
	entries := make([]ModelEntry, 0, len(models))
	for _, model := range models {
		entries = append(entries, radiusModelEntry(model))
	}
	if r.config == nil {
		return entries, ""
	}
	configured, ok := r.config.Providers[provider.ID()]
	if !ok {
		return entries, ""
	}
	catalog := slices.Clone(entries)
	for i := range entries {
		if configured.OAuth == nil || configured.OAuth.Kind != "radius" {
			entries[i].BaseURL = firstModelValue(configured.BaseURL, entries[i].BaseURL)
		}
		entries[i].Compat = mergeCompat((*providerCompat)(entries[i].Compat), configured.Compat)
	}
	for _, definition := range configured.Models {
		entry := r.resolveModelDef(provider.ID(), configured, definition)
		defaults := radiusModelDefaults(entries, definition.ID, firstModelValue(definition.API, configured.API))
		entry.API = firstModelValue(entry.API, defaults.API)
		entry.BaseURL = firstModelValue(entry.BaseURL, defaults.BaseURL)
		// modelFromJson:216-223 with the catalog's models as the defaults: the api is checked before the base URL.
		if entry.API == "" {
			return catalog, fmt.Sprintf(`Provider %s, model %s: no "api" specified. Set at provider or model level.`, provider.ID(), definition.ID)
		}
		if entry.BaseURL == "" {
			return catalog, fmt.Sprintf("Provider %s: \"baseUrl\" is required when defining custom models.", provider.ID())
		}
		index := -1
		for i := range entries {
			if entries[i].ModelID == entry.ModelID {
				index = i
				break
			}
		}
		if index < 0 {
			entries = append(entries, entry)
		} else {
			entries[index] = entry
		}
	}
	for i := range entries {
		entry := &entries[i]
		if override, ok := configured.ModelOverrides[entry.ModelID]; ok {
			r.applyOverride(entry, override)
		}
		entry.Headers = r.composeRequestHeadersLocked(entry.ModelHeaders, provider.ID(), entry.ModelID, &configured, nil)
		entry.Env = r.providerEnv(provider.ID())
		entry.Insecure = configured.Insecure
		entry.AuthHeader = configured.AuthHeader != nil && *configured.AuthHeader
	}
	return entries, ""
}

func radiusModelDefaults(entries []ModelEntry, id, api string) ModelEntry {
	for _, entry := range entries {
		if entry.ModelID == id {
			return entry
		}
	}
	for _, candidateAPI := range []string{api, string(ai.APIOpenAICompletions)} {
		for _, entry := range entries {
			if candidateAPI != "" && entry.API == candidateAPI {
				return entry
			}
		}
	}
	if len(entries) > 0 {
		return entries[0]
	}
	return ModelEntry{}
}

func (r *ModelRegistry) radiusConfiguredKey(providerID string) (string, map[string]string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.config != nil {
		if configured, ok := r.config.Providers[providerID]; ok && configured.APIKey != "" {
			return configured.APIKey, r.providerEnv(providerID), true
		}
	}
	return "", nil, false
}

func (r *ModelRegistry) resolveRadiusAPIKey(providerID string, stored *ai.Credential) (string, error) {
	if stored != nil && stored.Type == ai.CredentialAPIKey {
		if stored.Key != "" {
			return stored.Key, nil
		}
	} else if raw, env, ok := r.radiusConfiguredKey(providerID); ok {
		key, err := configvalue.ResolveOrError(raw, fmt.Sprintf("API key for provider %q", providerID), env)
		if err != nil || key != "" {
			return key, err
		}
	}
	// Every Radius gateway provider resolves RADIUS_API_KEY.
	return ai.GetEnvAPIKey(RadiusProviderID, nil), nil
}
