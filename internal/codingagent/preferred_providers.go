package codingagent

// Port of the ordering half of coding-agent core/preferred-providers.ts (compareProviders). The preferred partners
// themselves (PREFERRED_PROVIDER_IDS, isPreferredProvider) live in package ai; the name comparison is localeCompare, whose
// collation tables package ai must not link (internal/jsstring/deps_test.go), so the comparator is here.

import (
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
)

// providerRef is the part of a provider that compareProviders orders by: upstream's `{ id: string; name: string }`.
type providerRef struct {
	ID   string
	Name string
}

// compareProviders orders providers: preferred partners first, in their order, then all others alphabetically by name, as
// `a.name.localeCompare(b.name)` does.
func compareProviders(a, b providerRef) int {
	if rankA, rankB := preferredRank(a.ID), preferredRank(b.ID); rankA != rankB {
		return rankA - rankB
	}
	return localecompare.Compare(a.Name, b.Name)
}

func preferredRank(providerID string) int {
	if index := slices.Index(ai.PreferredProviderIDs, providerID); index >= 0 {
		return index
	}
	return len(ai.PreferredProviderIDs)
}
