package ai

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
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
	APIKey        string
	Model         string
	ModelMetadata *Model
	// ThinkingLevelMap supplies an explicit map when selected ModelMetadata is absent.
	ThinkingLevelMap ThinkingLevelMap
	ProviderID       string
	BaseURL          string
	Compat           *OpenAIResponsesCompat
	// ExtraHeaders are the model's headers (models.json). They override the default originator and User-Agent; Authorization,
	// chatgpt-account-id and the transport headers stay the provider's.
	ExtraHeaders map[string]string
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
	// Defaults first so model and caller headers can override them (openai-codex-responses.ts buildBaseCodexHeaders, #10429).
	extraHeaders := map[string]string{
		"OpenAI-Beta": "responses=experimental",
		// pig divergence (D26): PiG names itself as the Codex originator.
		"originator": pigidentity.CodexOriginator,
	}
	for name, value := range cfg.ExtraHeaders {
		for existing := range extraHeaders {
			if strings.EqualFold(existing, name) {
				delete(extraHeaders, existing)
			}
		}
		extraHeaders[name] = value
	}
	baseCfg := OpenAIResponsesConfig{
		StrictModeDefault:     true, // upstream openai-codex-responses.ts: supportsStrictMode ?? true
		StrictToolNull:        true, // codex passes strict: null
		ignoreSSEErrorObjects: true,
		Codex:                 true,
		IsReasoning:           true,
		Model:                 cfg.Model,
		ModelMetadata:         cfg.ModelMetadata,
		ThinkingLevelMap:      cfg.ThinkingLevelMap,
		ProviderID:            providerID,
		APIKeyHeader:          "Authorization",
		APIKeyPrefix:          "Bearer ",
		ExtraHeaders:          extraHeaders,
		Compat:                &compat,
		GetAPIKey: func(context.Context) (string, error) {
			apiKey := firstNonEmptyString(cfg.APIKey, os.Getenv("OPENAI_API_KEY"))
			if apiKey == "" {
				return "", fmt.Errorf("openai-codex-responses: OPENAI_API_KEY is required")
			}
			return apiKey, nil
		},
		BaseURLIsEndpoint: true,
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
