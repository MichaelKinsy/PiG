package ai

import "testing"

// openai-completions.ts:1602-1609 and :1682-1690 detect supportsLongCacheRetention as true unless the provider id or base URL names Together, Cloudflare Workers AI, Cloudflare AI
// Gateway, NVIDIA or Ant Ling (each by provider id or by either of its hosts), and :1731 lets an explicit model.compat value win over the detected one.
func TestOpenAICompletionsLongCacheRetentionIsDetectedPerProviderAndOverridable(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name, provider, baseURL string
		override                *bool
		want                    bool
	}{
		{"direct OpenAI", "openai", "https://api.openai.com/v1", nil, true},
		{"an unknown proxy", "custom", "https://proxy.example.com/v1", nil, true},
		{"Together by provider id", "together", "https://proxy.example.com/v1", nil, false},
		{"Together by api.together.ai", "custom", "https://api.together.ai/v1", nil, false},
		{"Together by api.together.xyz", "custom", "https://api.together.xyz/v1", nil, false},
		{"Cloudflare Workers AI by provider id", "cloudflare-workers-ai", "https://proxy.example.com/v1", nil, false},
		{"Cloudflare Workers AI by host", "custom", "https://api.cloudflare.com/client/v4", nil, false},
		{"Cloudflare AI Gateway by provider id", "cloudflare-ai-gateway", "https://proxy.example.com/v1", nil, false},
		{"Cloudflare AI Gateway by host", "custom", "https://gateway.ai.cloudflare.com/v1", nil, false},
		{"NVIDIA by provider id", "nvidia", "https://proxy.example.com/v1", nil, false},
		{"NVIDIA by host", "custom", "https://integrate.api.nvidia.com/v1", nil, false},
		{"Ant Ling by provider id", "ant-ling", "https://proxy.example.com/v1", nil, false},
		{"Ant Ling by host", "custom", "https://api.ant-ling.com/v1", nil, false},
		{"an explicit true wins over a detected false", "together", "https://api.together.ai/v1", &yes, true},
		{"an explicit false wins over a detected true", "openai", "https://api.openai.com/v1", &no, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var model *OpenAICompat
			if tc.override != nil {
				model = &OpenAICompat{SupportsLongCacheRetention: tc.override}
			}
			resolved := getOpenAICompletionsCompat(tc.provider, tc.baseURL, "m", model)
			if resolved.SupportsLongCacheRetention == nil || *resolved.SupportsLongCacheRetention != tc.want {
				t.Fatalf("resolved supportsLongCacheRetention = %v, want %v", resolved.SupportsLongCacheRetention, tc.want)
			}
			provider := NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", ProviderID: tc.provider, BaseURL: tc.baseURL, Compat: model}).(*openAIProvider)
			if got := provider.supportsLongCacheRetention(); got != tc.want {
				t.Errorf("provider.supportsLongCacheRetention() = %v, want %v", got, tc.want)
			}
		})
	}
}
