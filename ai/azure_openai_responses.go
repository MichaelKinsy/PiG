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
	ThinkingLevelMap  ThinkingLevelMap
	AzureAPIVersion   string
	AzureResourceName string
	// AzureBaseURL is the request's azureBaseUrl option; it outranks AZURE_OPENAI_BASE_URL, which outranks BaseURL (the model's base URL).
	AzureBaseURL        string
	AzureDeploymentName string
	Env                 ProviderEnv
	Compat              *OpenAIResponsesCompat
}

// NewAzureOpenAIResponsesProvider creates an Azure OpenAI Responses provider.
// Mirrors upstream providers/azure-openai-responses.ts.
func NewAzureOpenAIResponsesProvider(cfg AzureOpenAIResponsesConfig) Provider {
	providerID := cfg.ProviderID
	if providerID == "" {
		providerID = string(APIAzureOpenAIResponses)
	}
	deployment := ResolveAzureDeploymentName(cfg.Model, azureEndpointOptions(cfg))
	baseCfg := OpenAIResponsesConfig{
		api:                           APIAzureOpenAIResponses,
		StrictModeDefault:             true, // upstream azure-openai-responses.ts: supportsStrictMode ?? true
		SkipServiceTierPricing:        true,
		APIKey:                        cfg.APIKey,
		APIKeyHeader:                  "api-key",
		APIKeyPrefix:                  "",
		Model:                         cfg.Model,
		requestModel:                  deployment,
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
		GetBaseURL: func(context.Context) (string, error) {
			resolved, err := ResolveAzureConfig(cfg.BaseURL, azureEndpointOptions(cfg))
			if err != nil {
				return "", err
			}
			return strings.TrimRight(resolved.BaseURL, "/") + "/responses?api-version=" + url.QueryEscape(resolved.APIVersion), nil
		},
	}
	return NewOpenAIResponsesProvider(baseCfg)
}

func azureEndpointOptions(cfg AzureOpenAIResponsesConfig) AzureEndpointOptions {
	return AzureEndpointOptions{APIVersion: cfg.AzureAPIVersion, ResourceName: cfg.AzureResourceName, BaseURL: cfg.AzureBaseURL, DeploymentName: cfg.AzureDeploymentName, Env: cfg.Env}
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
