package ai

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const defaultAzureAPIVersion = "v1"

// AzureOpenAIResponsesConfig mirrors the upstream Azure wrapper provider while
// delegating transport/parsing to the shared OpenAI Responses implementation.
type AzureOpenAIResponsesConfig struct {
	APIKey              string
	Model               string
	ProviderID          string
	BaseURL             string
	ExtraHeaders        map[string]string
	SamplingParams      map[string]any
	AzureAPIVersion     string
	AzureResourceName   string
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
	deployment := resolveAzureDeploymentName(cfg.Model, cfg.AzureDeploymentName, cfg.Env)
	baseCfg := OpenAIResponsesConfig{
		StrictModeDefault:      true, // upstream azure-openai-responses.ts: supportsStrictMode ?? true
		SkipServiceTierPricing: true,
		APIKey:                 cfg.APIKey,
		APIKeyHeader:           "api-key",
		APIKeyPrefix:           "",
		Model:                  deployment,
		ProviderID:             providerID,
		ExtraHeaders:           cfg.ExtraHeaders,
		SamplingParams:         cfg.SamplingParams,
		Compat:                 cfg.Compat,
		BaseURLIsEndpoint:      true,
		GetAPIKey: func(context.Context) (string, error) {
			apiKey := firstNonEmptyString(cfg.APIKey, os.Getenv("AZURE_OPENAI_API_KEY"))
			if apiKey == "" {
				return "", fmt.Errorf("azure-openai-responses: AZURE_OPENAI_API_KEY is required")
			}
			return apiKey, nil
		},
		GetBaseURL: func(context.Context) (string, error) {
			baseURL, err := resolveAzureBaseURL(cfg)
			if err != nil {
				return "", err
			}
			return strings.TrimRight(baseURL, "/") + "/responses?api-version=" + url.QueryEscape(resolveAzureAPIVersion(cfg.AzureAPIVersion, cfg.Env)), nil
		},
	}
	_ = resolveAzureAPIVersion(cfg.AzureAPIVersion, cfg.Env) // parity surface; v1 path already encoded in base URL
	return NewOpenAIResponsesProvider(baseCfg)
}

func resolveAzureAPIVersion(explicit string, env ProviderEnv) string {
	return firstNonEmptyString(explicit, getProviderEnvValue("AZURE_OPENAI_API_VERSION", env), defaultAzureAPIVersion)
}

func resolveAzureDeploymentName(modelID, explicit string, env ProviderEnv) string {
	if explicit != "" {
		return explicit
	}
	for entry := range strings.SplitSeq(getProviderEnvValue("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", env), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		model, deployment, ok := strings.Cut(entry, "=")
		if ok && strings.TrimSpace(model) == modelID && strings.TrimSpace(deployment) != "" {
			return strings.TrimSpace(deployment)
		}
	}
	return modelID
}

func resolveAzureBaseURL(cfg AzureOpenAIResponsesConfig) (string, error) {
	baseURL := strings.TrimSpace(firstNonEmptyString(cfg.BaseURL, getProviderEnvValue("AZURE_OPENAI_BASE_URL", cfg.Env)))
	resourceName := strings.TrimSpace(firstNonEmptyString(cfg.AzureResourceName, getProviderEnvValue("AZURE_OPENAI_RESOURCE_NAME", cfg.Env)))
	if baseURL == "" && resourceName != "" {
		baseURL = fmt.Sprintf("https://%s.openai.azure.com/openai/v1", resourceName)
	}
	if baseURL == "" {
		return "", fmt.Errorf("azure-openai-responses: AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME is required")
	}

	trimmed := strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if trimmed == "" {
		return "", fmt.Errorf("azure-openai-responses: invalid base URL %q", baseURL)
	}
	if !strings.Contains(trimmed, "://") {
		return "", fmt.Errorf("azure-openai-responses: invalid base URL %q", baseURL)
	}

	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("azure-openai-responses: invalid base URL %q", baseURL)
	}

	isAzureHost := strings.HasSuffix(u.Hostname(), ".openai.azure.com") || strings.HasSuffix(u.Hostname(), ".cognitiveservices.azure.com") || strings.HasSuffix(u.Hostname(), ".ai.azure.com")
	normalizedPath := strings.TrimRight(u.Path, "/")
	if isAzureHost && (normalizedPath == "" || normalizedPath == "/" || normalizedPath == "/openai" || normalizedPath == "/openai/v1/responses") {
		u.Path = "/openai/v1"
		u.RawQuery = ""
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
