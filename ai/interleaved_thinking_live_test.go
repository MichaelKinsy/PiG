//go:build live

package ai

import "testing"

// These cases ask a remote Claude model to think before and after a real tool
// result. The faux companion tests cover the deterministic transcript and
// assertions; only model-generated interleaving is live-only.
func TestInterleavedThinkingLiveUpstream(t *testing.T) {
	for _, tc := range interleavedThinkingCases() {
		t.Run(tc.name, func(t *testing.T) {
			var p Provider
			if tc.provider == "amazon-bedrock" {
				// .upstream/v0.87.1/packages/ai/test/bedrock-utils.ts:13
				requireBedrockLiveCredentials(t)
				p = NewBedrockProvider(tc.id, "")
			} else {
				key := liveProviderKey(t, tc.provider)
				model := mustGeneratedModel(t, tc.provider, tc.id)
				p = NewAnthropicProvider(AnthropicConfig{APIKey: key, ProviderID: tc.provider, Model: tc.id, BaseURL: model.BaseURL, Compat: model.Compat})
			}
			assertInterleavedThinking(t, p)
		})
	}
}
