package ai

func tokensUpstreamCases() []tokensUpstreamCase {
	return []tokensUpstreamCase{
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:90
		{"should include token stats when aborted mid-stream", "google", "gemini-2.5-flash", "", false, "", true},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:103
		{"should include token stats when aborted mid-stream", "openai", "gpt-4o-mini", APIOpenAICompletions, false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:111
		{"should include token stats when aborted mid-stream", "openai", "gpt-5.4-mini", "", false, "low", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:121
		{"should include token stats when aborted mid-stream", "azure-openai-responses", "gpt-4o-mini", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:129
		{"should include token stats when aborted mid-stream", "anthropic", "claude-sonnet-4-6", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:137
		{"should include token stats when aborted mid-stream", "xai", "grok-4.3", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:145
		{"should include token stats when aborted mid-stream", "groq", "openai/gpt-oss-20b", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:155
		{"should include token stats when aborted mid-stream", "cerebras", "preferred", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:167
		{"should include token stats when aborted mid-stream", "cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:175
		{"should include token stats when aborted mid-stream", "cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:183
		{"should include token stats when aborted mid-stream", "huggingface", "moonshotai/Kimi-K2.5", "", false, "", false},
		// .upstream/v0.99.1/packages/ai/test/tokens.test.ts:191 (0.99.1 moved Together to Kimi-K3)
		{"should include token stats when aborted mid-stream", "together", "moonshotai/Kimi-K3", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:199
		{"should include token stats when aborted mid-stream", "baseten", "zai-org/GLM-5.2", "", false, "high", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:207
		{"should include token stats when aborted mid-stream", "zai", "glm-5.2", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:215
		{"should include token stats when aborted mid-stream", "mistral", "devstral-medium-latest", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:223
		{"should include token stats when aborted mid-stream", "minimax", "MiniMax-M2.7", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:231
		{"should include token stats when aborted mid-stream", "kimi-coding", "kimi-for-coding", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:239
		{"should include token stats when aborted mid-stream", "meta", "muse-spark-1.3", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:247
		{"should include token stats when aborted mid-stream", "vercel-ai-gateway", "google/gemini-2.5-flash", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:260
		{"should include token stats when aborted mid-stream", "xiaomi", "mimo-v2.5-pro", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:270
		{"should include token stats when aborted mid-stream", "xiaomi-token-plan-cn", "mimo-v2.5-pro", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:280
		{"should include token stats when aborted mid-stream", "xiaomi-token-plan-ams", "mimo-v2.5-pro", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:290
		{"should include token stats when aborted mid-stream", "xiaomi-token-plan-sgp", "mimo-v2.5-pro", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:298
		{"should include token stats when aborted mid-stream", "qwen-token-plan", "qwen3.7-max", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:306
		{"should include token stats when aborted mid-stream", "qwen-token-plan-individual", "qwen3.8-max", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:314
		{"should include token stats when aborted mid-stream", "qwen-token-plan-cn", "qwen3.7-max", "", false, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:326
		{"should include token stats when aborted mid-stream", "anthropic", "claude-sonnet-4-6", "", true, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:336
		{"claude-haiku-4.5 - should include token stats when aborted mid-stream", "github-copilot", "claude-haiku-4.5", "", true, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:345
		{"claude-sonnet-4 - should include token stats when aborted mid-stream", "github-copilot", "claude-sonnet-4.6", "", true, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:356
		{"gpt-5.5 - should include token stats when aborted mid-stream", "openai-codex", "gpt-5.5", "", true, "", false},
		// .upstream/v0.87.1/packages/ai/test/tokens.test.ts:369
		{"should include token stats when aborted mid-stream", "amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", "", false, "", false},
	}
}
