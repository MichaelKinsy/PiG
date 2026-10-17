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
//   - every other provider: ModelRegistry.HasConfiguredAuth (models.json, then
//     Pi's checkProviderAuth over the stored auth.json credential and the environment)
//   - ollama: OLLAMA_HOST env set (cheap heuristic; we do NOT probe
//     localhost:11434: picking ollama with no daemon running is a
//     user error caught at switch time)
//   - amazon-bedrock, google-vertex, cloudflare-*: their resolvers' env signals (Pi reads no shared AWS config file)
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

	if auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json")); err == nil {
		registry.SetAuthStorage(auth)
	}

	for providerID := range ReachableProviders() {
		if registry.HasConfiguredAuth(providerID) {
			out[providerID] = true
		}
	}

	if os.Getenv("OLLAMA_HOST") != "" {
		out["ollama"] = true
	}

	return out
}

// envOnlyAuthenticated is the agentDir-less fallback path: env-only checks.
func envOnlyAuthenticated() map[string]bool {
	out := make(map[string]bool)
	for providerID := range ReachableProviders() {
		if hasEnvAuth(providerID) {
			out[providerID] = true
		}
	}
	if os.Getenv("OLLAMA_HOST") != "" {
		out["ollama"] = true
	}
	return out
}
