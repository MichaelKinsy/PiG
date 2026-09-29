package ai

import "testing"

func TestCatalogReasoningEffortDetection(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/src/api/openai-completions.ts:1589-1601,1639-1640
	for _, tc := range []struct {
		provider, url string
		want          bool
	}{
		{"fireworks", "https://api.fireworks.ai/inference/v1", true},
		{"openai", "https://api.openai.com/v1", true},
		{"custom", "http://localhost:1234/v1", true},
		{"deepseek", "https://api.deepseek.com/v1", true},
		{"xai", "http://localhost/v1", false},
		{"zai", "http://localhost/v1", false},
		{"zai-coding-cn", "http://localhost/v1", false},
		{"moonshotai", "http://localhost/v1", false},
		{"moonshotai-cn", "http://localhost/v1", false},
		{"together", "http://localhost/v1", false},
		{"cloudflare-ai-gateway", "http://localhost/v1", false},
		{"nvidia", "http://localhost/v1", false},
		{"ant-ling", "http://localhost/v1", false},
		{"custom", "https://api.x.ai/v1", false},
		{"custom", "https://api.z.ai/v1", false},
		{"custom", "https://open.bigmodel.cn/api", false},
		{"custom", "https://api.moonshot.ai/v1", false},
		{"custom", "https://api.together.ai/v1", false},
		{"custom", "https://api.together.xyz/v1", false},
		{"custom", "https://gateway.ai.cloudflare.com/v1/a/b", false},
		{"custom", "https://integrate.api.nvidia.com/v1", false},
		{"custom", "https://api.ant-ling.com/v1", false},
	} {
		t.Run(tc.provider+"/"+tc.url, func(t *testing.T) {
			if got := upstreamSupportsReasoningEffort(tc.provider, tc.url); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCatalogExplicitCacheRetentionWins(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/src/api/anthropic-messages.ts:60-69
	for _, tc := range []struct {
		retention CacheRetention
		env       string
		want      CacheRetention
	}{
		{CacheRetentionNone, "long", CacheRetentionNone},
		{CacheRetentionShort, "long", CacheRetentionShort},
		{CacheRetentionLong, "short", CacheRetentionLong},
		{"", "long", CacheRetentionLong},
		{"", "short", CacheRetentionShort},
	} {
		t.Run(string(tc.retention)+"/"+tc.env, func(t *testing.T) {
			if got := resolveAnthropicCacheRetention(tc.retention, ProviderEnv{"PI_CACHE_RETENTION": tc.env}); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCatalogConfiguredAnthropicCompatWins(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/src/api/anthropic-messages.ts:206-219 reads the supplied model, not a second catalog lookup.
	compat := &AnthropicMessagesCompat{SupportsMidConvoSystemMessages: new(false), SupportsMidConvoToolChanges: new(false)}
	p := &anthropicProvider{cfg: AnthropicConfig{Model: "claude-opus-4-8", ProviderID: "anthropic", Compat: compat}}
	model := p.resolveModel()
	if model.ProviderMeta.Compat != compat {
		t.Fatalf("compat = %+v, want configured %+v", model.ProviderMeta.Compat, compat)
	}
	generated := mustGeneratedModel(t, "anthropic", "claude-opus-4-8")
	if generated.Compat.SupportsMidConvoToolChanges == nil || !*generated.Compat.SupportsMidConvoToolChanges {
		t.Fatal("mutated catalog")
	}
}
