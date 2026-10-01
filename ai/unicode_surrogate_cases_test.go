package ai

func unicodeUpstreamCases() []unicodeUpstreamCase {
	return []unicodeUpstreamCase{
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:289
		{"should handle emoji in tool results", "google", "gemini-2.5-flash", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:293
		{"should handle real-world LinkedIn comment data with emoji", "google", "gemini-2.5-flash", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:297
		{"should handle unpaired high surrogate (0xD83D) in tool results", "google", "gemini-2.5-flash", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:305
		{"should handle emoji in tool results", "openai", "gpt-4o-mini", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:309
		{"should handle real-world LinkedIn comment data with emoji", "openai", "gpt-4o-mini", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:313
		{"should handle unpaired high surrogate (0xD83D) in tool results", "openai", "gpt-4o-mini", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:321
		{"should handle emoji in tool results", "openai", "gpt-5-mini", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:325
		{"should handle real-world LinkedIn comment data with emoji", "openai", "gpt-5-mini", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:329
		{"should handle unpaired high surrogate (0xD83D) in tool results", "openai", "gpt-5-mini", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:339
		{"should handle emoji in tool results", "azure-openai-responses", "gpt-4o-mini", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:343
		{"should handle real-world LinkedIn comment data with emoji", "azure-openai-responses", "gpt-4o-mini", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:347
		{"should handle unpaired high surrogate (0xD83D) in tool results", "azure-openai-responses", "gpt-4o-mini", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:355
		{"should handle emoji in tool results", "anthropic", "claude-haiku-4-5", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:359
		{"should handle real-world LinkedIn comment data with emoji", "anthropic", "claude-haiku-4-5", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:363
		{"should handle unpaired high surrogate (0xD83D) in tool results", "anthropic", "claude-haiku-4-5", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:375
		{"should handle emoji in tool results", "anthropic", "claude-haiku-4-5", 0, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:379
		{"should handle real-world LinkedIn comment data with emoji", "anthropic", "claude-haiku-4-5", 1, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:387
		{"should handle unpaired high surrogate (0xD83D) in tool results", "anthropic", "claude-haiku-4-5", 2, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:397
		{"claude-haiku-4.5 - should handle emoji in tool results", "github-copilot", "claude-haiku-4.5", 0, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:406
		{"claude-haiku-4.5 - should handle real-world LinkedIn comment data with emoji", "github-copilot", "claude-haiku-4.5", 1, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:415
		{"claude-haiku-4.5 - should handle unpaired high surrogate (0xD83D) in tool results", "github-copilot", "claude-haiku-4.5", 2, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:424
		{"claude-sonnet-4 - should handle emoji in tool results", "github-copilot", "claude-sonnet-4.6", 0, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:433
		{"claude-sonnet-4 - should handle real-world LinkedIn comment data with emoji", "github-copilot", "claude-sonnet-4.6", 1, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:442
		{"claude-sonnet-4 - should handle unpaired high surrogate (0xD83D) in tool results", "github-copilot", "claude-sonnet-4.6", 2, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:455
		{"should handle emoji in tool results", "xai", "grok-4.3", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:459
		{"should handle real-world LinkedIn comment data with emoji", "xai", "grok-4.3", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:463
		{"should handle unpaired high surrogate (0xD83D) in tool results", "xai", "grok-4.3", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:471
		{"should handle emoji in tool results", "groq", "openai/gpt-oss-20b", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:475
		{"should handle real-world LinkedIn comment data with emoji", "groq", "openai/gpt-oss-20b", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:479
		{"should handle unpaired high surrogate (0xD83D) in tool results", "groq", "openai/gpt-oss-20b", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:487
		{"should handle emoji in tool results", "cerebras", "gpt-oss-120b", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:491
		{"should handle real-world LinkedIn comment data with emoji", "cerebras", "gpt-oss-120b", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:495
		{"should handle unpaired high surrogate (0xD83D) in tool results", "cerebras", "gpt-oss-120b", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:503
		{"should handle emoji in tool results", "cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:507
		{"should handle real-world LinkedIn comment data with emoji", "cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:511
		{"should handle unpaired high surrogate (0xD83D) in tool results", "cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:519
		{"should handle emoji in tool results", "cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:523
		{"should handle real-world LinkedIn comment data with emoji", "cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:527
		{"should handle unpaired high surrogate (0xD83D) in tool results", "cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:535
		{"should handle emoji in tool results", "huggingface", "moonshotai/Kimi-K2.5", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:539
		{"should handle real-world LinkedIn comment data with emoji", "huggingface", "moonshotai/Kimi-K2.5", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:543
		{"should handle unpaired high surrogate (0xD83D) in tool results", "huggingface", "moonshotai/Kimi-K2.5", 2, false, ""},
		// .upstream/v0.99.1/packages/ai/test/unicode-surrogate.test.ts:552 (0.99.1 moved Together to Kimi-K3)
		{"should handle emoji in tool results", "together", "moonshotai/Kimi-K3", 0, false, "high"},
		// .upstream/v0.99.1/packages/ai/test/unicode-surrogate.test.ts:556
		{"should handle real-world LinkedIn comment data with emoji", "together", "moonshotai/Kimi-K3", 1, false, "high"},
		// .upstream/v0.99.1/packages/ai/test/unicode-surrogate.test.ts:560
		{"should handle unpaired high surrogate (0xD83D) in tool results", "together", "moonshotai/Kimi-K3", 2, false, "high"},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:569
		{"should handle emoji in tool results", "baseten", "zai-org/GLM-5.2", 0, false, "high"},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:573
		{"should handle real-world LinkedIn comment data with emoji", "baseten", "zai-org/GLM-5.2", 1, false, "high"},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:577
		{"should handle unpaired high surrogate (0xD83D) in tool results", "baseten", "zai-org/GLM-5.2", 2, false, "high"},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:585
		{"should handle emoji in tool results", "zai", "glm-5.2", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:589
		{"should handle real-world LinkedIn comment data with emoji", "zai", "glm-5.2", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:593
		{"should handle unpaired high surrogate (0xD83D) in tool results", "zai", "glm-5.2", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:601
		{"should handle emoji in tool results", "mistral", "devstral-medium-latest", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:605
		{"should handle real-world LinkedIn comment data with emoji", "mistral", "devstral-medium-latest", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:609
		{"should handle unpaired high surrogate (0xD83D) in tool results", "mistral", "devstral-medium-latest", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:617
		{"should handle emoji in tool results", "minimax", "MiniMax-M2.7", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:621
		{"should handle real-world LinkedIn comment data with emoji", "minimax", "MiniMax-M2.7", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:625
		{"should handle unpaired high surrogate (0xD83D) in tool results", "minimax", "MiniMax-M2.7", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:633
		{"should handle emoji in tool results", "xiaomi", "mimo-v2.5-pro", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:637
		{"should handle real-world LinkedIn comment data with emoji", "xiaomi", "mimo-v2.5-pro", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:641
		{"should handle unpaired high surrogate (0xD83D) in tool results", "xiaomi", "mimo-v2.5-pro", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:651
		{"should handle emoji in tool results", "xiaomi-token-plan-cn", "mimo-v2.5-pro", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:655
		{"should handle real-world LinkedIn comment data with emoji", "xiaomi-token-plan-cn", "mimo-v2.5-pro", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:659
		{"should handle unpaired high surrogate (0xD83D) in tool results", "xiaomi-token-plan-cn", "mimo-v2.5-pro", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:674
		{"should handle emoji in tool results", "xiaomi-token-plan-ams", "mimo-v2.5-pro", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:678
		{"should handle real-world LinkedIn comment data with emoji", "xiaomi-token-plan-ams", "mimo-v2.5-pro", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:682
		{"should handle unpaired high surrogate (0xD83D) in tool results", "xiaomi-token-plan-ams", "mimo-v2.5-pro", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:697
		{"should handle emoji in tool results", "xiaomi-token-plan-sgp", "mimo-v2.5-pro", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:701
		{"should handle real-world LinkedIn comment data with emoji", "xiaomi-token-plan-sgp", "mimo-v2.5-pro", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:705
		{"should handle unpaired high surrogate (0xD83D) in tool results", "xiaomi-token-plan-sgp", "mimo-v2.5-pro", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:718
		{"should handle emoji in tool results", "qwen-token-plan", "qwen3.7-max", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:722
		{"should handle real-world LinkedIn comment data with emoji", "qwen-token-plan", "qwen3.7-max", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:726
		{"should handle unpaired high surrogate (0xD83D) in tool results", "qwen-token-plan", "qwen3.7-max", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:734
		{"should handle emoji in tool results", "qwen-token-plan-individual", "qwen3.8-max", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:738
		{"should handle real-world LinkedIn comment data with emoji", "qwen-token-plan-individual", "qwen3.8-max", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:742
		{"should handle unpaired high surrogate (0xD83D) in tool results", "qwen-token-plan-individual", "qwen3.8-max", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:750
		{"should handle emoji in tool results", "qwen-token-plan-cn", "qwen3.7-max", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:754
		{"should handle real-world LinkedIn comment data with emoji", "qwen-token-plan-cn", "qwen3.7-max", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:758
		{"should handle unpaired high surrogate (0xD83D) in tool results", "qwen-token-plan-cn", "qwen3.7-max", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:766
		{"should handle emoji in tool results", "kimi-coding", "kimi-for-coding", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:770
		{"should handle real-world LinkedIn comment data with emoji", "kimi-coding", "kimi-for-coding", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:774
		{"should handle unpaired high surrogate (0xD83D) in tool results", "kimi-coding", "kimi-for-coding", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:782
		{"should handle emoji in tool results", "vercel-ai-gateway", "google/gemini-2.5-flash", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:786
		{"should handle real-world LinkedIn comment data with emoji", "vercel-ai-gateway", "google/gemini-2.5-flash", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:790
		{"should handle unpaired high surrogate (0xD83D) in tool results", "vercel-ai-gateway", "google/gemini-2.5-flash", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:798
		{"should handle emoji in tool results", "amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", 0, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:802
		{"should handle real-world LinkedIn comment data with emoji", "amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", 1, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:806
		{"should handle unpaired high surrogate (0xD83D) in tool results", "amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", 2, false, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:812
		{"gpt-5.5 - should handle emoji in tool results", "openai-codex", "gpt-5.5", 0, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:821
		{"gpt-5.5 - should handle real-world LinkedIn comment data with emoji", "openai-codex", "gpt-5.5", 1, true, ""},
		// .upstream/v0.87.1/packages/ai/test/unicode-surrogate.test.ts:830
		{"gpt-5.5 - should handle unpaired high surrogate (0xD83D) in tool results", "openai-codex", "gpt-5.5", 2, true, ""},
	}
}
