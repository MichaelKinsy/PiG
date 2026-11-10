package ai

import (
	"maps"
	"slices"
	"testing"
)

// packages/ai/src/lazy-oauth.ts registerLazyOAuthFlows registers one loader per key of load.ts OAuthFlowLoaders; each key names the flow the matching providers/<id>.ts passes to lazyOAuth as load (loadAnthropicOAuth in providers/anthropic.ts, loadGitHubCopilotOAuth in github-copilot.ts, and so on). Go links the flows at build time, so registration has no Go form and the claim that replaces it is that every flow Pi registers is already resolvable and no other built-in flow exists.
func TestEveryFlowRegisterLazyOAuthFlowsRegistersIsLinkedWithoutRegistration(t *testing.T) {
	flowProviders := map[string]string{
		"anthropic":     "anthropic",
		"openaiCodex":   "openai-codex",
		"openaiChatGPT": "openai",
		"githubCopilot": "github-copilot",
		"openrouter":    "openrouter",
		"kimiCoding":    "kimi-coding",
		"meta":          "meta",
		"xai":           "xai",
		"radius":        "radius",
	}
	for flow, id := range flowProviders {
		provider, ok := GetOAuthProvider(id)
		if !ok {
			t.Errorf("flow %s: GetOAuthProvider(%q) not found", flow, id)
			continue
		}
		if provider.ID() != id {
			t.Errorf("flow %s: provider ID = %q, want %q", flow, provider.ID(), id)
		}
	}
	got := slices.Sorted(maps.Keys(builtInOAuthProviders))
	want := slices.Sorted(maps.Values(flowProviders))
	if !slices.Equal(got, want) {
		t.Errorf("built-in OAuth providers = %v, want exactly the flows lazy-oauth.ts registers: %v", got, want)
	}
}
