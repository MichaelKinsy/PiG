package ai

// Port of coding-agent core/preferred-providers.ts: the preferred partner providers and the order provider lists show
// them in. Kept free of UI code, as upstream keeps it, so provider lists can offer it from here.

import "github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"

// PreferredProviderIDs are the ids of the preferred partner providers, most preferred first (PREFERRED_PROVIDER_IDS).
var PreferredProviderIDs = []string{RadiusProviderID, "openrouter"}

// ProviderRef is the part of a provider that CompareProviders orders by: upstream's `{ id: string; name: string }`.
type ProviderRef struct {
	ID   string
	Name string
}

// IsPreferredProvider reports whether the provider is a preferred partner (isPreferredProvider).
func IsPreferredProvider(providerID string) bool {
	return preferredRank(providerID) < len(PreferredProviderIDs)
}

// CompareProviders orders providers: preferred partners first, in their order, then all others alphabetically by name,
// as `a.name.localeCompare(b.name)` does (compareProviders).
func CompareProviders(a, b ProviderRef) int {
	if rankA, rankB := preferredRank(a.ID), preferredRank(b.ID); rankA != rankB {
		return rankA - rankB
	}
	return localecompare.Compare(a.Name, b.Name)
}

func preferredRank(providerID string) int {
	for index, id := range PreferredProviderIDs {
		if id == providerID {
			return index
		}
	}
	return len(PreferredProviderIDs)
}
