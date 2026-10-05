package ai

func toolWithoutResultUpstreamCases() []toolWithoutResultCase {
	return []toolWithoutResultCase{
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:101
		{"should filter out tool calls without corresponding tool results", "google", "gemini-2.5-flash", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:114
		{"should filter out tool calls without corresponding tool results", "openai", "gpt-4o-mini", APIOpenAICompletions, false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:122
		{"should filter out tool calls without corresponding tool results", "openai", "gpt-5-mini", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:132
		{"should filter out tool calls without corresponding tool results", "azure", "gpt-4o-mini", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:140
		{"should filter out tool calls without corresponding tool results", "anthropic", "claude-haiku-4-5", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:148
		{"should filter out tool calls without corresponding tool results", "xai", "grok-4.3", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:156
		{"should filter out tool calls without corresponding tool results", "groq", "openai/gpt-oss-20b", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:164
		{"should filter out tool calls without corresponding tool results", "cerebras", "gpt-oss-120b", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:172
		{"should filter out tool calls without corresponding tool results", "cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:180
		{"should filter out tool calls without corresponding tool results", "cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:188
		{"should filter out tool calls without corresponding tool results", "huggingface", "moonshotai/Kimi-K2.5", "", false, ""},
		// .upstream/v0.99.1/packages/ai/test/tool-call-without-result.test.ts:196 (0.99.1 moved Together to Kimi-K3)
		{"should filter out tool calls without corresponding tool results", "together", "moonshotai/Kimi-K3", "", false, "high"},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:204
		{"should filter out tool calls without corresponding tool results", "baseten", "zai-org/GLM-5.2", "", false, "high"},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:212
		{"should filter out tool calls without corresponding tool results", "zai", "glm-5.2", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:220
		{"should filter out tool calls without corresponding tool results", "mistral", "devstral-medium-latest", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:228
		{"should filter out tool calls without corresponding tool results", "minimax", "MiniMax-M2.7", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:236
		{"should filter out tool calls without corresponding tool results", "xiaomi", "mimo-v2.5-pro", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:244
		{"should filter out tool calls without corresponding tool results", "xiaomi-token-plan-cn", "mimo-v2.5-pro", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:252
		{"should filter out tool calls without corresponding tool results", "xiaomi-token-plan-ams", "mimo-v2.5-pro", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:260
		{"should filter out tool calls without corresponding tool results", "xiaomi-token-plan-sgp", "mimo-v2.5-pro", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:268
		{"should filter out tool calls without corresponding tool results", "qwen-token-plan", "qwen3.7-max", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:276
		{"should filter out tool calls without corresponding tool results", "qwen-token-plan-individual", "qwen3.8-max", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:284
		{"should filter out tool calls without corresponding tool results", "qwen-token-plan-cn", "qwen3.7-max", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:292
		{"should filter out tool calls without corresponding tool results", "kimi-coding", "kimi-for-coding", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:300
		{"should filter out tool calls without corresponding tool results", "vercel-ai-gateway", "google/gemini-2.5-flash", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:308
		{"should filter out tool calls without corresponding tool results", "amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", "", false, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:320
		{"should filter out tool calls without corresponding tool results", "anthropic", "claude-haiku-4-5", "", true, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:330
		{"claude-haiku-4.5 - should filter out tool calls without corresponding tool results", "github-copilot", "claude-haiku-4.5", "", true, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:339
		{"claude-sonnet-4 - should filter out tool calls without corresponding tool results", "github-copilot", "claude-sonnet-4.6", "", true, ""},
		// .upstream/v0.87.1/packages/ai/test/tool-call-without-result.test.ts:350
		{"gpt-5.5 - should filter out tool calls without corresponding tool results", "openai-codex", "gpt-5.5", "", true, ""},
	}
}
