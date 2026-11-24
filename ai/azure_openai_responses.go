package ai

// Ports packages/ai/src/api/azure-openai-responses.ts.

import (
	"context"
	"fmt"
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
	// SamplingParamsOrder and SamplingParamsByThinkingLevelOrder are Model.SamplingParamsOrder and Model.SamplingParamsByThinkingLevelOrder.
	SamplingParamsOrder                []string
	SamplingParamsByThinkingLevelOrder map[ModelThinkingLevel][]string
	// ThinkingLevelMap overrides catalog effort values when non-nil.
	ThinkingLevelMap ThinkingLevelMap
	// Env is the provider's environment; request env entries outrank it. The Azure endpoint options (azureBaseUrl, azureResourceName, azureApiVersion, azureDeploymentName) are per-request StreamOptions fields.
	Env    ProviderEnv
	Compat *OpenAIResponsesCompat
	// IsReasoning indicates the model supports extended reasoning (model.reasoning).
	IsReasoning bool
}

// NewAzureOpenAIResponsesProvider creates an Azure OpenAI Responses provider.
// Mirrors upstream providers/azure-openai-responses.ts.
func NewAzureOpenAIResponsesProvider(cfg AzureOpenAIResponsesConfig) Provider {
	providerID := cfg.ProviderID
	if providerID == "" {
		providerID = string(APIAzureOpenAIResponses)
	}
	baseCfg := OpenAIResponsesConfig{
		api:                                APIAzureOpenAIResponses,
		StrictModeDefault:                  true, // upstream azure-openai-responses.ts: supportsStrictMode ?? true
		SkipServiceTierPricing:             true,
		APIKey:                             cfg.APIKey,
		APIKeyHeader:                       "api-key",
		APIKeyPrefix:                       "",
		Model:                              cfg.Model,
		azure:                              &azureResponsesEndpoint{env: cfg.Env, modelBaseURL: cfg.BaseURL},
		ProviderID:                         providerID,
		ExtraHeaders:                       cfg.ExtraHeaders,
		SamplingParams:                     cfg.SamplingParams,
		SamplingParamsByThinkingLevel:      cfg.SamplingParamsByThinkingLevel,
		SamplingParamsOrder:                cfg.SamplingParamsOrder,
		SamplingParamsByThinkingLevelOrder: cfg.SamplingParamsByThinkingLevelOrder,
		ModelMetadata:                      cfg.ModelMetadata,
		ThinkingLevelMap:                   cfg.ThinkingLevelMap,
		Compat:                             cfg.Compat,
		IsReasoning:                        cfg.IsReasoning,
		BaseURLIsEndpoint:                  true,
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

// azureResponsesRequestURL builds the Azure OpenAI Responses request URL the way pinned openai 7.59.0
// AzureOpenAI does (azure.mjs:41 sets defaultQuery['api-version'] = apiVersion; client.mjs:502-554 buildURL
// strips search/hash from baseURL before appending '/responses', merges baseQuery under pathQuery/defaultQuery,
// and preserves repeated baseParams).
// /responses is not in azure.mjs's _deployments_endpoints, so buildRequest does not rewrite its path.
func azureResponsesRequestURL(config AzureConfig) (string, error) {
	return openaiBuildURLWithDefaultQuery(config.BaseURL, "/responses", sdkQueryParam{name: "api-version", value: config.APIVersion})
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
		return azureResponsesRequestURL(config)
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
