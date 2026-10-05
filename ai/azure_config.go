package ai

// Ports packages/ai/src/api/azure-openai-config.ts

import (
	"cmp"
	"fmt"
	"net/url"
	"strings"
)

const defaultAzureAPIVersion = "v1"

// AzureEndpointOptions are the per-request Azure endpoint options. Azure models ship without a base URL: each user has their own resource, resolved per request.
type AzureEndpointOptions struct {
	APIVersion     string
	ResourceName   string
	BaseURL        string
	DeploymentName string
	Env            ProviderEnv
}

// AzureConfig is the resolved Azure endpoint.
type AzureConfig struct {
	BaseURL    string
	APIVersion string
}

// ResolveAzureDeploymentName returns the request model's deployment name: the explicit option, else the AZURE_OPENAI_DEPLOYMENT_NAME_MAP entry for the model, else the model ID.
// The map is parsed as upstream parseDeploymentNameMap does: each entry is trimmed with String.prototype.trim and split with split("=", 2), which drops the text after a second "="; a later entry for the same model replaces an earlier one; and a model mapped to an empty name falls back to the model ID.
func ResolveAzureDeploymentName(modelID string, options AzureEndpointOptions) string {
	if options.DeploymentName != "" {
		return options.DeploymentName
	}
	mapped := ""
	for entry := range strings.SplitSeq(getProviderEnvValue("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", options.Env), ",") {
		parts := strings.SplitN(trimJSWhitespace(entry), "=", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		if trimJSWhitespace(parts[0]) == modelID {
			mapped = trimJSWhitespace(parts[1])
		}
	}
	return cmp.Or(mapped, modelID)
}

// ResolveAzureBaseURL resolves the endpoint from the explicit option, AZURE_OPENAI_BASE_URL, the resource name, then the model's base URL, and normalizes it.
func ResolveAzureBaseURL(modelBaseURL string, options AzureEndpointOptions) (string, error) {
	baseURL := strings.TrimSpace(firstNonEmptyString(options.BaseURL, getProviderEnvValue("AZURE_OPENAI_BASE_URL", options.Env)))
	resourceName := strings.TrimSpace(firstNonEmptyString(options.ResourceName, getProviderEnvValue("AZURE_OPENAI_RESOURCE_NAME", options.Env)))
	if baseURL == "" && resourceName != "" {
		baseURL = fmt.Sprintf("https://%s.openai.azure.com/openai/v1", resourceName)
	}
	if baseURL == "" {
		baseURL = modelBaseURL
	}
	if baseURL == "" {
		return "", fmt.Errorf("Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME, or pass azureBaseUrl, azureResourceName, or model.baseUrl.")
	}
	return normalizeAzureBaseURL(baseURL)
}

// ResolveAzureConfig resolves the endpoint and API version.
func ResolveAzureConfig(modelBaseURL string, options AzureEndpointOptions) (AzureConfig, error) {
	baseURL, err := ResolveAzureBaseURL(modelBaseURL, options)
	if err != nil {
		return AzureConfig{}, err
	}
	return AzureConfig{BaseURL: baseURL, APIVersion: firstNonEmptyString(options.APIVersion, getProviderEnvValue("AZURE_OPENAI_API_VERSION", options.Env), defaultAzureAPIVersion)}, nil
}

func normalizeAzureBaseURL(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if trimmed == "" || !strings.Contains(trimmed, "://") {
		return "", fmt.Errorf("Invalid Azure OpenAI base URL: %s", baseURL)
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("Invalid Azure OpenAI base URL: %s", baseURL)
	}
	isAzureHost := strings.HasSuffix(u.Hostname(), ".openai.azure.com") || strings.HasSuffix(u.Hostname(), ".cognitiveservices.azure.com") || strings.HasSuffix(u.Hostname(), ".ai.azure.com")
	normalizedPath := strings.TrimRight(u.Path, "/")
	// Azure hosts need /openai/v1 so the request path and ?api-version=v1 resolve.
	if isAzureHost && (normalizedPath == "" || normalizedPath == "/" || normalizedPath == "/openai" || normalizedPath == "/openai/v1/responses") {
		u.Path = "/openai/v1"
		u.RawQuery = ""
	}
	return strings.TrimRight(u.String(), "/"), nil
}
