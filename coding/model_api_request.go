package coding

import (
	"context"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// prepareAPIRequest runs an already-resolved Pi API leaf. The independent Node
// ModelRuntime owns catalog/auth resolution; re-resolving here would substitute
// the parent Session's model, endpoint and credentials for the child's values.
func (runtime *ModelRuntime) prepareAPIRequest(ctx context.Context, model *ai.Model, options ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
	entry := icodingagent.ModelEntry{
		ProviderID: model.ProviderMeta.ProviderID, ModelID: model.ID, DisplayName: model.DisplayName,
		API: string(model.ProviderMeta.API), BaseURL: model.ProviderMeta.BaseURL, APIKey: options.APIKey,
		Reasoning: model.ProviderMeta.Reasoning, Compat: model.ProviderMeta.Compat,
		ThinkingLevelMap: cloneThinkingLevelMap(model.ThinkingLevelMap), Input: append([]string(nil), model.Input...), InputLimits: model.InputLimits.Clone(),
		ContextWindow: model.Capabilities.ContextWindow, MaxTokens: model.Capabilities.MaxOutputTokens,
		SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel, Env: maps.Clone(options.Env),
	}
	provider, err := buildProviderForEntry(entry.ProviderID, entry.ModelID, model.ProviderMeta.API, entry, runtime.services, options.APIKey, true, true)
	if err != nil {
		return nil, nil, ai.StreamOptions{}, err
	}
	prepared := options
	prepared.APIKey = ""
	prepared.ModelCost = model.CostRates()
	prepared.Headers = mergeRuntimeHeaders(model.ProviderMeta.Headers, options.Headers)
	if options.TransformHeaders != nil {
		prepared.Headers, err = options.TransformHeaders(ctx, maps.Clone(prepared.Headers))
		if err != nil {
			_ = provider.Close()
			return nil, nil, ai.StreamOptions{}, err
		}
	}
	prepared.TransformHeaders = nil
	provider = newProviderAttributionProvider(provider, entry.ProviderID, entry.BaseURL, func() bool {
		return runtime.services.settings != nil && runtime.services.settings.IsInstallTelemetryEnabled()
	}, nil)
	return model, provider, prepared, nil
}
