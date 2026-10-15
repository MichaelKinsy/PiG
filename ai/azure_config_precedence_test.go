package ai

import (
	"strings"
	"testing"
)

// packages/ai/src/api/azure-openai-config.ts:72-107 (resolveAzureBaseUrl, resolveAzureConfig).
func TestResolveAzureConfigPrecedence(t *testing.T) {
	// getProviderEnvValue falls back to the process environment; keep the defaults below hermetic.
	for _, name := range []string{"AZURE_OPENAI_BASE_URL", "AZURE_OPENAI_RESOURCE_NAME", "AZURE_OPENAI_API_VERSION"} {
		t.Setenv(name, "")
	}
	env := ProviderEnv{
		"AZURE_OPENAI_BASE_URL":      "https://env-base.openai.azure.com/openai/v1",
		"AZURE_OPENAI_RESOURCE_NAME": "env-res",
		"AZURE_OPENAI_API_VERSION":   "env-version",
	}
	// :74 the explicit azureBaseUrl option outranks AZURE_OPENAI_BASE_URL, which outranks the resource name (:75-80) and model.baseUrl (:82).
	config, err := ResolveAzureConfig("https://model.example/v1", StreamOptions{Env: env, AzureBaseURL: " https://opt.openai.azure.com ", AzureAPIVersion: "opt-version"})
	if err != nil || config.BaseURL != "https://opt.openai.azure.com/openai/v1" || config.APIVersion != "opt-version" {
		t.Fatalf("explicit options = %+v, %v", config, err)
	}
	// :74 a whitespace-only base URL falls through to the resource name.
	config, err = ResolveAzureConfig("", StreamOptions{Env: ProviderEnv{"AZURE_OPENAI_RESOURCE_NAME": "res"}, AzureBaseURL: "  "})
	if err != nil || config.BaseURL != "https://res.openai.azure.com/openai/v1" {
		t.Fatalf("blank base URL = %+v, %v", config, err)
	}
	// :75 the explicit azureResourceName outranks the environment one.
	config, err = ResolveAzureConfig("", StreamOptions{Env: ProviderEnv{"AZURE_OPENAI_RESOURCE_NAME": "env-res"}, AzureResourceName: "opt-res"})
	if err != nil || config.BaseURL != "https://opt-res.openai.azure.com/openai/v1" {
		t.Fatalf("explicit resource = %+v, %v", config, err)
	}
	// :102-105 the API version defaults to "v1" with no option or environment value.
	config, err = ResolveAzureConfig("https://model.example/v1", StreamOptions{Env: ProviderEnv{}})
	if err != nil || config.APIVersion != "v1" {
		t.Fatalf("default version = %+v, %v", config, err)
	}
	// :101 an unresolved endpoint fails before the version is read.
	if _, err := ResolveAzureConfig("", StreamOptions{Env: ProviderEnv{}}); err == nil || !strings.HasPrefix(err.Error(), "Azure OpenAI base URL is required.") {
		t.Fatalf("missing endpoint error = %v", err)
	}
}

// :102-105 the API version is the first truthy value, untrimmed: a blank option is truthy and wins over the environment.
func TestResolveAzureConfigDoesNotTrimTheAPIVersion(t *testing.T) {
	for _, tc := range []struct {
		option, env, want string
	}{
		{" opt-version ", "env-version", " opt-version "},
		{" ", "env-version", " "},
		{"", " env-version\n", " env-version\n"},
	} {
		config, err := ResolveAzureConfig("https://res.openai.azure.com", StreamOptions{Env: ProviderEnv{"AZURE_OPENAI_API_VERSION": tc.env}, AzureAPIVersion: tc.option})
		if err != nil || config.APIVersion != tc.want {
			t.Errorf("option %q env %q: %+v, %v; want %q", tc.option, tc.env, config, err, tc.want)
		}
	}
}

// :38 normalizeAzureBaseUrl applies String.prototype.trim (which removes a BOM) before stripping trailing slashes, so a model base URL
// with a BOM or a slash before trailing whitespace still normalizes.
func TestNormalizeAzureBaseURLUsesECMAScriptTrim(t *testing.T) {
	for input, want := range map[string]string{
		"\ufeffhttps://res.openai.azure.com\ufeff": "https://res.openai.azure.com/openai/v1",
		"https://example.test/v1/ \u00a0":          "https://example.test/v1",
	} {
		if got, err := normalizeAzureBaseURL(input); err != nil || got != want {
			t.Errorf("normalizeAzureBaseURL(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

// :75 the resource name is not trimmed: a blank name is truthy in Pi, builds a host with a space, and fails URL parsing instead of
// falling back to model.baseUrl.
func TestResolveAzureBaseURLDoesNotTrimTheResourceName(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	_, err := ResolveAzureBaseURL("https://model.example/v1", StreamOptions{Env: ProviderEnv{}, AzureResourceName: " "})
	if err == nil || !strings.HasPrefix(err.Error(), "Invalid Azure OpenAI base URL: https:// .openai.azure.com/openai/v1") {
		t.Fatalf("blank resource name error = %v", err)
	}
}
