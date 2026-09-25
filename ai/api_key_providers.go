package ai

// APIKeyProviderInfo is a static description of a provider that authenticates
// with a raw API key (as opposed to OAuth/subscription flow).
//
// Single source of truth for the API-key provider catalog. interactive.go's
// /login → "Use an API key" selector and any other surface that needs the
// full list MUST iterate APIKeyProviders(), not maintain a parallel slice.
type APIKeyProviderInfo struct {
	ID   string
	Name string
}

// builtInAPIKeyProviders mirrors the upstream API-key provider catalog. Order
// is the user-facing list order; keep it stable so the selector UI is stable.
var builtInAPIKeyProviders = []APIKeyProviderInfo{
	{ID: "amazon-bedrock", Name: "Amazon Bedrock"},
	{ID: "azure-openai-responses", Name: "Azure OpenAI Responses"},
	{ID: "cerebras", Name: "Cerebras"},
	{ID: "fireworks", Name: "Fireworks"},
	{ID: "google", Name: "Google Gemini"},
	{ID: "google-vertex", Name: "Google Vertex AI"},
	{ID: "groq", Name: "Groq"},
	{ID: "huggingface", Name: "Hugging Face"},
	{ID: "kimi-coding", Name: "Kimi For Coding"},
	{ID: "mistral", Name: "Mistral"},
	{ID: "minimax", Name: "MiniMax"},
	{ID: "minimax-cn", Name: "MiniMax (China)"},
	{ID: "openai", Name: "OpenAI"},
	{ID: "openrouter", Name: "OpenRouter"},
	{ID: "radius", Name: "Radius"},
	{ID: "vercel-ai-gateway", Name: "Vercel AI Gateway"},
	{ID: "xai", Name: "xAI"},
	{ID: "zai", Name: "ZAI"},
}

// APIKeyProviders returns the canonical API-key provider catalog.
// Callers must not mutate the returned slice; copy if you need to.
func APIKeyProviders() []APIKeyProviderInfo {
	out := make([]APIKeyProviderInfo, len(builtInAPIKeyProviders))
	copy(out, builtInAPIKeyProviders)
	return out
}

// APIKeyProviderName looks up the display name for an API-key provider id.
// Returns the id unchanged if no built-in match exists, mirroring the old
// buildAuthProviderName default branch.
func APIKeyProviderName(id string) string {
	for _, p := range builtInAPIKeyProviders {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}
