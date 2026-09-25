package ai

// Environment API-key discovery. Mirrors upstream packages/ai/src/env-api-keys.ts
// (findEnvKeys, getEnvApiKey). Provider env values resolve through
// getProviderEnvValue (provider_env.go).

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Anthropic credential variables. Mirrors upstream ANTHROPIC_AUTH_TOKEN_ENV,
// ANTHROPIC_OAUTH_TOKEN_ENV, and ANTHROPIC_API_KEY_ENV.
const (
	AnthropicAuthTokenEnv  = "ANTHROPIC_AUTH_TOKEN"
	AnthropicOAuthTokenEnv = "ANTHROPIC_OAUTH_TOKEN"
	AnthropicAPIKeyEnv     = "ANTHROPIC_API_KEY"
)

// envAPIKeyVars mirrors the envMap of upstream getApiKeyEnvVars. Providers
// with more than one variable are listed in getAPIKeyEnvVars.
var envAPIKeyVars = map[string]string{
	"ant-ling":                   "ANT_LING_API_KEY",
	"qwen-token-plan":            "QWEN_TOKEN_PLAN_API_KEY",
	"qwen-token-plan-cn":         "QWEN_TOKEN_PLAN_CN_API_KEY",
	"qwen-token-plan-individual": "QWEN_TOKEN_PLAN_API_KEY",
	"openai":                     "OPENAI_API_KEY",
	"azure-openai-responses":     "AZURE_OPENAI_API_KEY",
	"nvidia":                     "NVIDIA_API_KEY",
	"deepseek":                   "DEEPSEEK_API_KEY",
	"google":                     "GEMINI_API_KEY",
	"google-vertex":              "GOOGLE_CLOUD_API_KEY",
	"groq":                       "GROQ_API_KEY",
	"cerebras":                   "CEREBRAS_API_KEY",
	"xai":                        "XAI_API_KEY",
	"radius":                     "RADIUS_API_KEY",
	"openrouter":                 "OPENROUTER_API_KEY",
	"vercel-ai-gateway":          "AI_GATEWAY_API_KEY",
	"zai":                        "ZAI_API_KEY",
	"zai-coding-cn":              "ZAI_CODING_CN_API_KEY",
	"mistral":                    "MISTRAL_API_KEY",
	"minimax":                    "MINIMAX_API_KEY",
	"minimax-cn":                 "MINIMAX_CN_API_KEY",
	"moonshotai":                 "MOONSHOT_API_KEY",
	"moonshotai-cn":              "MOONSHOT_API_KEY",
	"huggingface":                "HF_TOKEN",
	"fireworks":                  "FIREWORKS_API_KEY",
	"together":                   "TOGETHER_API_KEY",
	"baseten":                    "BASETEN_API_KEY",
	"opencode":                   "OPENCODE_API_KEY",
	"opencode-go":                "OPENCODE_API_KEY",
	"kimi-coding":                "KIMI_API_KEY",
	"meta":                       "META_API_KEY",
	"cloudflare-workers-ai":      "CLOUDFLARE_API_KEY",
	"cloudflare-ai-gateway":      "CLOUDFLARE_API_KEY",
	"xiaomi":                     "XIAOMI_API_KEY",
	"xiaomi-token-plan-cn":       "XIAOMI_TOKEN_PLAN_CN_API_KEY",
	"xiaomi-token-plan-ams":      "XIAOMI_TOKEN_PLAN_AMS_API_KEY",
	"xiaomi-token-plan-sgp":      "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
}

// envAuthenticated is upstream's marker for ambient credentials that are not
// an API key (Vertex ADC, the AWS credential chain).
const envAuthenticated = "<authenticated>"

func getAPIKeyEnvVars(provider string) []string {
	switch provider {
	case "github-copilot":
		// Generic GitHub tokens are not Copilot credentials.
		return []string{"COPILOT_GITHUB_TOKEN"}
	case "anthropic":
		// ANTHROPIC_AUTH_TOKEN participates in discovery and status, but
		// GetEnvAPIKey skips it: requests send it as Authorization: Bearer.
		return []string{AnthropicAuthTokenEnv, AnthropicOAuthTokenEnv, AnthropicAPIKeyEnv}
	}
	if envVar, ok := envAPIKeyVars[provider]; ok {
		return []string{envVar}
	}
	return nil
}

// FindEnvKeys returns the configured environment variables that can provide
// an API key for provider, in lookup order, or nil. Ambient credential
// sources (AWS profiles and IAM, Google ADC) are not API keys and are not
// reported.
func FindEnvKeys(provider string, env map[string]string) []string {
	var found []string
	for _, envVar := range getAPIKeyEnvVars(provider) {
		if getProviderEnvValue(envVar, env) != "" {
			found = append(found, envVar)
		}
	}
	return found
}

// GetEnvAPIKey returns a provider's API key from its environment variables,
// or "<authenticated>" for Vertex ADC and AWS ambient credentials, or "".
func GetEnvAPIKey(provider string, env map[string]string) string {
	if envKeys := FindEnvKeys(provider, env); len(envKeys) > 0 {
		apiKeyEnv := envKeys[0]
		if provider == "anthropic" {
			index := slices.IndexFunc(envKeys, func(key string) bool { return key != AnthropicAuthTokenEnv })
			apiKeyEnv = ""
			if index >= 0 {
				apiKeyEnv = envKeys[index]
			}
		}
		if apiKeyEnv != "" {
			return getProviderEnvValue(apiKeyEnv, env)
		}
	}
	switch provider {
	case "google-vertex":
		hasProject := getProviderEnvValue("GOOGLE_CLOUD_PROJECT", env) != "" || getProviderEnvValue("GCLOUD_PROJECT", env) != ""
		if hasVertexADCCredentials(env) && hasProject && getProviderEnvValue("GOOGLE_CLOUD_LOCATION", env) != "" {
			return envAuthenticated
		}
	case "amazon-bedrock":
		value := func(name string) bool { return getProviderEnvValue(name, env) != "" }
		if value("AWS_PROFILE") ||
			(value("AWS_ACCESS_KEY_ID") && value("AWS_SECRET_ACCESS_KEY")) ||
			value("AWS_BEARER_TOKEN_BEDROCK") ||
			value("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") ||
			value("AWS_CONTAINER_CREDENTIALS_FULL_URI") ||
			value("AWS_WEB_IDENTITY_TOKEN_FILE") {
			return envAuthenticated
		}
	}
	return ""
}

// Process-wide ADC discovery is cached; explicit scoped paths bypass it.
var vertexADCCache struct {
	sync.Mutex
	exists *bool
}

func hasVertexADCCredentials(env map[string]string) bool {
	if path := env["GOOGLE_APPLICATION_CREDENTIALS"]; path != "" {
		_, err := os.Stat(path)
		return err == nil
	}
	vertexADCCache.Lock()
	defer vertexADCCache.Unlock()
	if vertexADCCache.exists != nil {
		return *vertexADCCache.exists
	}
	exists := discoverVertexADCCredentials(env)
	vertexADCCache.exists = &exists
	return exists
}

func discoverVertexADCCredentials(env map[string]string) bool {
	path := getProviderEnvValue("GOOGLE_APPLICATION_CREDENTIALS", env)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		path = filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	}
	_, err := os.Stat(path)
	return err == nil
}
