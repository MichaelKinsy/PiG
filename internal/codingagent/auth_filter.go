// Model picker auth-filter scope toggle.
//
// AuthenticatedProviders + ReachableProviders are the single source of
// truth for "which providers can the user actually talk to right now"
// vs "which providers does coding.BuildModel even know how to wire".
//
// Used by interactive `/model` overlay to default the picker scope to
// {auth ∩ reachable} and clamp the all-scope view to {reachable} (the
// other ~19 catalog providers would mis-route through BuildModel's
// OpenAI-compatible default branch and silently fail at first token).
//
// Mirrors upstream pi-coding-agent's
// `model-selector.ts::scopedModels` filter logic: pig computes the
// scoped set here at picker-open time rather than carrying it on
// SettingsManager.
package codingagent

import (
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
)

// ReachableProviders returns the set of provider IDs that
// coding.BuildModel has an explicit `case` for. Anything else falls
// through BuildModel's default branch which constructs an
// OpenAI-compatible client: fine for OpenAI-shaped APIs but a silent
// mis-route for Anthropic/Bedrock/Vertex/etc. that would error at
// first request.
//
// SINGLE SOURCE OF TRUTH. Keep this set aligned with
// coding/model.go::BuildModel so provider additions touch one map entry here.
func ReachableProviders() map[string]bool {
	return map[string]bool{
		"github-copilot": true, // OAuth (auth.json)
		"anthropic":      true, // ANTHROPIC_AUTH_TOKEN, ANTHROPIC_OAUTH_TOKEN, ANTHROPIC_API_KEY, or OAuth (auth.json)
		"openai":         true, // OPENAI_API_KEY
		"openai-codex":   true, // OAuth (auth.json → ChatGPT Plus/Pro subscription)
		"openrouter":     true, // OPENROUTER_API_KEY
		"groq":           true, // GROQ_API_KEY
		"ollama":         true, // OLLAMA_HOST or http://localhost:11434/v1
		"amazon-bedrock": true, // AWS default credentials chain or AWS_BEARER_TOKEN_BEDROCK
	}
}

// AuthenticatedProviders returns the subset of ReachableProviders for
// which credentials are detectable in the environment / auth.json /
// model registry, without probing the network.
//
//   - github-copilot: OAuth credential present and not expired
//   - openai/openrouter/groq: <ID_UPPER>_API_KEY env var set OR
//     registry has any non-empty APIKey entry for the provider
//   - ollama: OLLAMA_HOST env set (cheap heuristic; we do NOT probe
//     localhost:11434: picking ollama with no daemon running is a
//     user error caught at switch time)
//
// agentDir is the pig config dir (typically ~/.pig/agent). auth.json
// and models.json are read from there.
func AuthenticatedProviders(agentDir string) map[string]bool {
	out := make(map[string]bool, 5)
	if agentDir == "" {
		// Caller didn't plumb agentDir: fall back to env-only checks
		// rather than crashing. Better to under-detect than panic the
		// picker.
		auth := envOnlyAuthenticated()
		return auth
	}

	// github-copilot OAuth: presence of a refresh token means the
	// user has gone through the device flow; the access-token expiry
	// is auto-refreshed at first request, so we don't gate on it here.
	// (Upstream pi delegates the same decision to SettingsManager which
	// probes the refresh path; we use the cheaper on-disk check.)
	if auth, err := ai.NewAuthStorage(agentDir + "/auth.json"); err == nil {
		for _, providerID := range []string{"github-copilot", "openai-codex"} {
			if cred, ok, _ := auth.Get(providerID); ok {
				if cred.Type == ai.CredentialOAuth && cred.Refresh != "" {
					out[providerID] = true
				}
			}
		}
	}

	// API-key / ADC providers: env var OR registry entry.
	registry := NewModelRegistry(agentDir)
	for _, p := range []string{"openai", "openrouter", "groq", "anthropic", "azure-openai-responses", "google", "google-vertex", "mistral"} {
		if hasEnvAuth(p) || registry.HasAnyKey(p) {
			out[p] = true
		}
	}

	// ollama: heuristic: OLLAMA_HOST set.
	if os.Getenv("OLLAMA_HOST") != "" {
		out["ollama"] = true
	}

	// amazon-bedrock: detect any AWS auth signal. The AWS SDK resolves
	// the actual credentials at request time; we only need a cheap
	// boolean here to decide picker eligibility.
	if hasBedrockAuthSignal() {
		out["amazon-bedrock"] = true
	}

	return out
}

// envOnlyAuthenticated is the agentDir-less fallback path.
func envOnlyAuthenticated() map[string]bool {
	out := make(map[string]bool, 5)
	for _, p := range []string{"openai", "openrouter", "groq", "anthropic", "azure-openai-responses", "google", "google-vertex", "mistral"} {
		if hasEnvAuth(p) {
			out[p] = true
		}
	}
	if os.Getenv("OLLAMA_HOST") != "" {
		out["ollama"] = true
	}
	if hasBedrockAuthSignal() {
		out["amazon-bedrock"] = true
	}
	return out
}

// hasBedrockAuthSignal returns true when any of the AWS env vars or
// shared config files the SDK would consult are present. This is the
// cheapest heuristic we can run without actually constructing an SDK
// config; it intentionally errs on the side of "available" so the
// picker shows Bedrock when AWS auth is likely to succeed.
func hasBedrockAuthSignal() bool {
	// Matches upstream env-api-keys.ts amazon-bedrock check exactly.
	if os.Getenv("AWS_PROFILE") != "" ||
		(os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "") ||
		os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" ||
		os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		for _, rel := range []string{".aws/credentials", ".aws/config"} {
			if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
				return true
			}
		}
	}
	return false
}
