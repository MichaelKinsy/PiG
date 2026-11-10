package ai

import (
	"slices"
	"testing"
)

// Port of coding-agent test/preferred-providers.test.ts "compareProviders".
func TestCompareProvidersOrdersPreferredPartnersFirstThenTheRestByName(t *testing.T) {
	providers := []ProviderRef{
		{ID: "zai", Name: "Z.AI"},
		{ID: "openrouter", Name: "OpenRouter"},
		{ID: "anthropic", Name: "Anthropic"},
		{ID: "radius", Name: "Radius"},
		{ID: "google", Name: "Google"},
	}
	if want := []string{"radius", "openrouter"}; !slices.Equal(PreferredProviderIDs, want) {
		t.Fatalf("PreferredProviderIDs = %v, want %v", PreferredProviderIDs, want)
	}
	slices.SortFunc(providers, CompareProviders)
	var got []string
	for _, provider := range providers {
		got = append(got, provider.ID)
	}
	if want := []string{"radius", "openrouter", "anthropic", "google", "zai"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// Port of coding-agent test/preferred-providers.test.ts "isPreferredProvider".
func TestIsPreferredProviderRecognizesOnlyThePreferredPartners(t *testing.T) {
	for id, want := range map[string]bool{"radius": true, "openrouter": true, "anthropic": false} {
		if got := IsPreferredProvider(id); got != want {
			t.Errorf("IsPreferredProvider(%q) = %v, want %v", id, got, want)
		}
	}
}
