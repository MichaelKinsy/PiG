package ai

import (
	"encoding/json"
	"os/exec"
	"slices"
	"testing"
)

// The cases are the oracle Pi's (its catalog is Pi's generator over the recording in automation/gen/catalog-source; the script runs at test time, so a
// catalog refresh changes them with no file edit). The selection retains JavaScript localeCompare selection, not Go map or byte ordering.
// upstream: packages/ai/test/anthropic-eager-tool-input-e2e.test.ts:34-85 and anthropic-long-cache-retention-e2e.test.ts:22-70.
type anthropicAcceptanceCases struct {
	All, Configured, ForcedEager []string
}

func loadAnthropicAcceptanceCases(t *testing.T) anthropicAcceptanceCases {
	t.Helper()
	data, err := exec.CommandContext(t.Context(), "node", "../test/parity/testdata/generate-anthropic-e2e-cases.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("read the oracle Pi's Anthropic cases: %v\n%s", err, data)
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
		// OpenRouter's :nitro and :floor variants are PiG's addition (D199); the Pi fixture has none.
		for _, model := range piListedModels(ListModels(provider)) {
			if model.API == APIAnthropicMessages {
				expected = append(expected, provider+"/"+model.ID)
			}
		}
	}
	slices.Sort(expected)
	all := slices.Clone(cases.All)
	slices.Sort(all)
	if !slices.Equal(all, expected) {
		t.Fatal("Anthropic model denominator differs from the oracle Pi's catalog (test/parity/testdata/generate-anthropic-e2e-cases.mjs)")
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
