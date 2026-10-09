package ai

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// The Pi 1.0.3 snapshot retains JavaScript localeCompare selection, not Go map or byte ordering.
// upstream: packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:34-85 and anthropic-long-cache-retention-e2e.test.ts:22-70.
type anthropicAcceptanceCases struct {
	All, Configured, ForcedEager []string
}

func loadAnthropicAcceptanceCases(t *testing.T) anthropicAcceptanceCases {
	t.Helper()
	data, err := os.ReadFile("testdata/port-wave-13/anthropic_e2e_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases anthropicAcceptanceCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func assertAnthropicCatalogDenominator(t *testing.T, cases anthropicAcceptanceCases) {
	t.Helper()
	var expected []string
	for _, provider := range ListProviders() {
		for _, model := range ListModels(provider) {
			if model.API == APIAnthropicMessages {
				expected = append(expected, provider+"/"+model.ID)
			}
		}
	}
	slices.Sort(expected)
	if !slices.Equal(cases.All, expected) {
		t.Fatal("Anthropic model denominator differs from the Pi 1.1.0 fixture; see testdata/port-wave-13/README.md")
	}
}

// upstream: packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:127. Needs no credentials or network.
func TestAnthropicEagerToolInputCatalogDenominator(t *testing.T) {
	assertAnthropicCatalogDenominator(t, loadAnthropicAcceptanceCases(t))
}

// upstream: packages/ai/test/anthropic-long-cache-retention-e2e.test.ts:111. Needs no credentials or network.
func TestAnthropicLongCacheRetentionCatalogDenominator(t *testing.T) {
	assertAnthropicCatalogDenominator(t, loadAnthropicAcceptanceCases(t))
}
