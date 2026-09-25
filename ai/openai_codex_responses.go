package ai

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const defaultCodexBaseURL = "https://chatgpt.com/backend-api"

// OpenAICodexResponsesConfig mirrors the upstream OpenAI Codex Responses
// provider (providers/openai-codex-responses.ts). Codex uses the same
// Responses wire format as OpenAI but with a different base URL, auth
// scheme (Bearer JWT), and endpoint path (/codex/responses).
//
// Codex uses SSE when explicitly selected. Its default auto transport prefers a
// session-scoped WebSocket and falls back to SSE before output starts.
type OpenAICodexResponsesConfig struct {
	APIKey     string
	Model      string
	ProviderID string
	BaseURL    string
	Compat     *OpenAIResponsesCompat
}

// NewOpenAICodexResponsesProvider creates an OpenAI Codex Responses provider.
// Mirrors upstream providers/openai-codex-responses.ts.
func NewOpenAICodexResponsesProvider(cfg OpenAICodexResponsesConfig) Provider {
	providerID := cfg.ProviderID
	if providerID == "" {
		providerID = string(APIOpenAICodexResponses)
	}
	compat := OpenAIResponsesCompat{}
	if cfg.Compat != nil {
		compat = *cfg.Compat
	}
	compat.SendSessionIdHeader = new(false)
	compat.SupportsLongCacheRetention = new(true)
	baseCfg := OpenAIResponsesConfig{
		StrictModeDefault:     true, // upstream openai-codex-responses.ts: supportsStrictMode ?? true
		StrictToolNull:        true, // codex passes strict: null
		ignoreSSEErrorObjects: true,
		Codex:                 true,
		IsReasoning:           true,
		Model:                 cfg.Model,
		ProviderID:            providerID,
		APIKeyHeader:          "Authorization",
		APIKeyPrefix:          "Bearer ",
		ExtraHeaders: map[string]string{
			"OpenAI-Beta": "responses=experimental",
			"originator":  "pi",
		},
		Compat: &compat,
		GetAPIKey: func(context.Context) (string, error) {
			apiKey := firstNonEmptyString(cfg.APIKey, os.Getenv("OPENAI_API_KEY"))
			if apiKey == "" {
				return "", fmt.Errorf("openai-codex-responses: OPENAI_API_KEY is required")
			}
			return apiKey, nil
		},
		BaseURLIsEndpoint: true,
		forceUserAgent:    true,
		GetBaseURL: func(context.Context) (string, error) {
			return resolveCodexURL(cfg.BaseURL), nil
		},
	}
	return NewOpenAIResponsesProvider(baseCfg)
}

// resolveCodexURL mirrors upstream resolveCodexUrl.
// Ensures the URL ends with /codex/responses.
func resolveCodexURL(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultCodexBaseURL
	}
	raw = strings.TrimRight(raw, "/")
	if strings.HasSuffix(raw, "/codex/responses") {
		return raw
	}
	if strings.HasSuffix(raw, "/codex") {
		return raw + "/responses"
	}
	return raw + "/codex/responses"
}
