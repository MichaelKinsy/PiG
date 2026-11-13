package ai

// Port of coding-agent core/preferred-providers.ts: the preferred partner providers. Kept free of UI code, as upstream
// keeps it, so provider lists can offer it from here. compareProviders, which orders provider lists by name with
// localeCompare, lives in internal/codingagent: this package links no collation tables (internal/jsstring/deps_test.go).

import "slices"

// PreferredProviderIDs are the ids of the preferred partner providers, most preferred first (PREFERRED_PROVIDER_IDS).
var PreferredProviderIDs = []string{RadiusProviderID, "openrouter"}

// IsPreferredProvider reports whether the provider is a preferred partner (isPreferredProvider).
func IsPreferredProvider(providerID string) bool {
	return slices.Contains(PreferredProviderIDs, providerID)
}
