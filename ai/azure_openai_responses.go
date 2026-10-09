package ai

// Ports packages/ai/src/api/azure-openai-responses.ts.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// AzureOpenAIResponsesConfig mirrors the upstream Azure wrapper provider while
// delegating transport/parsing to the shared OpenAI Responses implementation.
type AzureOpenAIResponsesConfig struct {
	ModelMetadata  *Model
	APIKey         string
	Model          string
	ProviderID     string
	BaseURL        string
	ExtraHeaders   map[string]string
	SamplingParams map[string]any
	// SamplingParamsByThinkingLevel overrides SamplingParams for the effective thinking level.
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel
	// ThinkingLevelMap overrides catalog effort values when non-nil.
	ThinkingLevelMap ThinkingLevelMap
	// Env is the provider's environment; request env entries outrank it. The Azure endpoint options (azureBaseUrl, azureResourceName, azureApiVersion, azureDeploymentName) are per-request StreamOptions fields.
	Env    ProviderEnv
	Compat *OpenAIResponsesCompat
}

// NewAzureOpenAIResponsesProvider creates an Azure OpenAI Responses provider.
// Mirrors upstream providers/azure-openai-responses.ts.
func NewAzureOpenAIResponsesProvider(cfg AzureOpenAIResponsesConfig) Provider {
	providerID := cfg.ProviderID
	if providerID == "" {
		providerID = string(APIAzureOpenAIResponses)
	}
	baseCfg := OpenAIResponsesConfig{
		api:                           APIAzureOpenAIResponses,
		StrictModeDefault:             true, // upstream azure-openai-responses.ts: supportsStrictMode ?? true
		SkipServiceTierPricing:        true,
		APIKey:                        cfg.APIKey,
		APIKeyHeader:                  "api-key",
		APIKeyPrefix:                  "",
		Model:                         cfg.Model,
		azure:                         &azureResponsesEndpoint{env: cfg.Env, modelBaseURL: cfg.BaseURL},
		ProviderID:                    providerID,
		ExtraHeaders:                  cfg.ExtraHeaders,
		SamplingParams:                cfg.SamplingParams,
		SamplingParamsByThinkingLevel: cfg.SamplingParamsByThinkingLevel,
		ModelMetadata:                 cfg.ModelMetadata,
		ThinkingLevelMap:              cfg.ThinkingLevelMap,
		Compat:                        cfg.Compat,
		BaseURLIsEndpoint:             true,
		GetAPIKey: func(context.Context) (string, error) {
			apiKey := firstNonEmptyString(cfg.APIKey, os.Getenv("AZURE_OPENAI_API_KEY"))
			if apiKey == "" {
				return "", fmt.Errorf("azure-openai-responses: AZURE_OPENAI_API_KEY is required")
			}
			return apiKey, nil
		},
	}
	return NewOpenAIResponsesProvider(baseCfg)
}

// azureResponsesEndpoint resolves the Azure endpoint and deployment for each Responses request (azure-openai-responses.ts resolveAzureConfig and resolveDeploymentName over the request's options).
type azureResponsesEndpoint struct {
	// env is the provider's environment; request env entries outrank it, as stream options carry env upstream.
	env ProviderEnv
	// modelBaseURL is the model's configured base URL; the endpoint falls back to it last.
	modelBaseURL string
}

// resolve returns the provider for one request: the same configuration with the deployment name as the wire model and the resolved endpoint.
func (e *azureResponsesEndpoint) resolve(p *openAIResponsesProvider, opts StreamOptions) *openAIResponsesProvider {
	options := opts
	options.Env = mergeProviderEnv(e.env, opts.Env)
	resolved := *p
	resolved.cfg.requestModel = ResolveAzureDeploymentName(p.cfg.Model, options)
	resolved.cfg.GetBaseURL = func(context.Context) (string, error) {
		config, err := ResolveAzureConfig(e.modelBaseURL, options)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(config.BaseURL, "/") + "/responses?api-version=" + url.QueryEscape(config.APIVersion), nil
	}
	resolved.cfg.azure = nil
	return &resolved
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
