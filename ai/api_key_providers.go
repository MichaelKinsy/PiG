package ai

import (
	"slices"
	"strings"
)

// APIKeyProviderInfo is a static description of a provider that authenticates
// with a raw API key (as opposed to OAuth/subscription flow).
type APIKeyProviderInfo struct {
	ID   string
	Name string
}

// APIKeyProviders returns the built-in providers that expose an API-key auth
// method, sorted by provider.name.
//
// Mirrors upstream interactive-mode.ts:getLoginProviderOptions filtered by
// auth.apiKey: upstream iterates ModelRuntime.getProviders() (the whole
// catalog) rather than a hand-maintained subset, so a provider such as
// opencode-go appears in /login → "Sign in with an API key". Deriving the
// list here keeps a newly added upstream provider from being silently
// omitted.
// pig additive (D92): a provider whose catalog models are all on stripped APIs (ProviderStripped) is left out.
func APIKeyProviders() []APIKeyProviderInfo {
	providerIDs := ListRuntimeProviders()
	out := make([]APIKeyProviderInfo, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		auth, err := BuiltinProviderAuth(providerID)
		if err != nil || auth.APIKey == nil || ProviderStripped(providerID) {
			continue
		}
		out = append(out, APIKeyProviderInfo{ID: providerID, Name: ProviderDisplayName(providerID)})
	}
	slices.SortFunc(out, compareAPIKeyProviderNames)
	return out
}

// compareAPIKeyProviderNames orders the selector the way upstream's
// provider.name.localeCompare does: case-insensitive primary order with the
// original spelling as the tie-break.
func compareAPIKeyProviderNames(a, b APIKeyProviderInfo) int {
	if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}

// APIKeyProviderName looks up the display name for an API-key provider id.
// Returns "" when the provider has no built-in API-key auth method.
func APIKeyProviderName(id string) string {
	for _, p := range APIKeyProviders() {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}
