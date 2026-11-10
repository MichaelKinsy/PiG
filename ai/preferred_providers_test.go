package ai

// pi: packages/coding-agent/src/core/preferred-providers.ts

import "testing"

// Port of coding-agent test/preferred-providers.test.ts "isPreferredProvider".
func TestIsPreferredProviderRecognizesOnlyThePreferredPartners(t *testing.T) {
	for id, want := range map[string]bool{"radius": true, "openrouter": true, "anthropic": false} {
		if got := IsPreferredProvider(id); got != want {
			t.Errorf("IsPreferredProvider(%q) = %v, want %v", id, got, want)
		}
	}
}
