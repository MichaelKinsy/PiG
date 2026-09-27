// Model picker auth-filter scope toggle.
//
// AuthenticatedProviders + ReachableProviders are the single source of
// truth for "which providers can the user actually talk to right now"
// vs "which providers can ModelRuntime compose". Reachable is the whole
// built-in catalog plus ollama; authenticated is the subset whose
// credentials resolve from the environment, models.json, or auth.json.
//
// Used by the interactive model surfaces (cycleModel, showScopedModels,
// --list-models) to default the picker scope to {auth ∩ reachable}.
//
// Mirrors upstream pi-coding-agent's model-runtime.ts availability filter:
// upstream derives `available` from checkAuth() over the whole provider
// catalog, so pig derives both sets from the catalog rather than a
// hand-maintained provider list.
package codingagent

import (
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
)

// ReachableProviders returns the set of provider IDs that ModelRuntime can
// compose: every provider in the built-in catalog plus ollama, which is
// configured from the environment rather than declared in the catalog.
//
// coding.BuildModel's buildProviderForEntry switches on a provider's API
// kind, not its id, so every catalog provider is wireable. Deriving this
// from the catalog keeps a new upstream provider from being silently
// omitted from the picker.
func ReachableProviders() map[string]bool {
	catalog := ai.ListRuntimeProviders()
	out := make(map[string]bool, len(catalog)+1)
	for _, providerID := range catalog {
		out[providerID] = true
	}
	out["ollama"] = true
	return out
}

// AuthenticatedProviders returns the subset of ReachableProviders for
// which credentials are detectable in the environment / auth.json /
// model registry, without probing the network.
//
//   - github-copilot / openai-codex: OAuth credential present with a
//     refresh token (a half-completed login is not configured auth)
//   - every other provider: its env API key, a models.json apiKey, or any
//     stored auth.json credential (api_key or oauth)
//   - ollama: OLLAMA_HOST env set (cheap heuristic; we do NOT probe
//     localhost:11434: picking ollama with no daemon running is a
//     user error caught at switch time)
//   - amazon-bedrock: any AWS auth signal, including shared config files
//   - google-vertex: GOOGLE_CLOUD_API_KEY or ADC (via hasEnvAuth)
//
// agentDir is the pig config dir (typically ~/.pig/agent). auth.json
// and models.json are read from there.
func AuthenticatedProviders(agentDir string) map[string]bool {
	if agentDir == "" {
		// Caller didn't plumb agentDir: fall back to env-only checks
		// rather than crashing. Better to under-detect than panic the
		// picker.
		return envOnlyAuthenticated()
	}

	out := make(map[string]bool)
	registry := NewModelRegistry(agentDir)

	// OAuth providers whose stored credential is only usable with a refresh
	// token. A half-completed login must not count as configured auth.
	if auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json")); err == nil {
		registry.SetAuthStorage(auth)
		for _, providerID := range []string{"github-copilot", "openai-codex"} {
			if cred, ok, _ := auth.Get(providerID); ok {
				if cred.Type == ai.CredentialOAuth && cred.Refresh != "" {
					out[providerID] = true
				}
			}
		}
	}

	for providerID := range ReachableProviders() {
		switch providerID {
		case "github-copilot", "openai-codex":
			// Handled above: these need a valid OAuth credential, not an env key.
			continue
		}
		if hasEnvAuth(providerID) || registry.HasAnyKey(providerID) || registry.hasStoredCredential(providerID) {
			out[providerID] = true
		}
	}

	if os.Getenv("OLLAMA_HOST") != "" {
		out["ollama"] = true
	}

	// amazon-bedrock: detect any AWS auth signal. The AWS SDK resolves the
	// actual credentials at request time; we only need a cheap boolean here to
	// decide picker eligibility. hasEnvAuth already covers the env signals;
	// hasBedrockAuthSignal also covers the shared credentials/config files.
	if hasBedrockAuthSignal() {
		out["amazon-bedrock"] = true
	}

	return out
}

// envOnlyAuthenticated is the agentDir-less fallback path: env-only checks.
func envOnlyAuthenticated() map[string]bool {
	out := make(map[string]bool)
	for providerID := range ReachableProviders() {
		switch providerID {
		case "github-copilot", "openai-codex":
			continue
		}
		if hasEnvAuth(providerID) {
			out[providerID] = true
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
