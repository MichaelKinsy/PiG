package ai

import "testing"

func TestOverflowUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, errorMessage, provider string
		contextWindow                int
		want                         bool
	}{
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:33
		{"detects explicit Ollama prompt-too-long errors", "400 `prompt too long; exceeded max context length by 100918 tokens`", "ollama", 32768, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:38
		{"detects z.ai prompt-too-long errors", `400 {"code":"1261","message":"Prompt too long"}`, "zai", 1048576, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:44
		{"detects Together AI context length errors", "400 The input (516368 tokens) is longer than the model's context length (262144 tokens).", "ollama", 262144, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:51
		{"detects LiteLLM-wrapped OpenAI maximum context length errors", "Error: 503 litellm.ServiceUnavailableError: litellm.MidStreamFallbackError: litellm.APIConnectionError: APIConnectionError: OpenAIException - Requested token count exceeds the model's maximum context length of 131072 tokens.", "ollama", 131072, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:58
		{"detects OpenAI-compatible parenthesized maximum context length errors", "Error: 400 Input length (265330) exceeds model's maximum context length (262144).", "ollama", 262144, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:65
		{"detects OpenRouter Poolside maximum allowed input length errors", "Provider returned error: Input length 131393 exceeds the maximum allowed input length of 131040 tokens.", "ollama", 131072, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:72
		{"detects DS4 configured context size errors/plain", "400 Prompt has 256468 tokens, but the configured context size is 256000 tokens", "ollama", 256000, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:79
		{"detects DS4 configured context size errors/commas", "Prompt has 5,958,968 tokens, but the configured context size is 256,000 tokens", "ollama", 256000, true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:84
		{"does not treat generic non-overflow Ollama errors as overflow", "500 `model runner crashed unexpectedly`", "ollama", 32768, false},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:89
		{"only treats bodyless 400 and 413 errors as overflow for Cerebras/400/cerebras", "400 status code (no body)", "cerebras", 131072, true},
		{"only treats bodyless 400 and 413 errors as overflow for Cerebras/413/cerebras", "413 status code (no body)", "cerebras", 131072, true},
		{"only treats bodyless 400 and 413 errors as overflow for Cerebras/400/opencode-go", "400 status code (no body)", "opencode-go", 1000000, false},
		{"only treats bodyless 400 and 413 errors as overflow for Cerebras/413/opencode-go", "413 status code (no body)", "opencode-go", 1000000, false},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:97
		{"does not treat Bedrock throttling 'Too many tokens' as overflow", "Throttling error: Too many tokens, please wait before trying again.", "ollama", 200000, false},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:104
		{"does not treat Bedrock service unavailable as overflow", "Service unavailable: The service is temporarily unavailable.", "ollama", 200000, false},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:109
		{"does not treat generic rate limit errors as overflow", "Rate limit exceeded, please retry after 30 seconds.", "ollama", 200000, false},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:114
		{"does not treat HTTP 429 style errors as overflow", "Too many requests. Please slow down.", "ollama", 200000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := AssistantMessage{API: APIOpenAICompletions, Provider: tc.provider, Model: "qwen3.5:35b", StopReason: StopReasonError, ErrorMessage: tc.errorMessage}
			if got := IsContextOverflow(message, tc.contextWindow); got != tc.want {
				t.Fatalf("IsContextOverflow = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOverflowLengthStopsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		input, cacheRead, cacheWrite, output, limit int
		recoverable, want                           bool
		api                                         API
		provider, model                             string
	}{
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:148
		{name: "detects Xiaomi-style overflow (length stop with zero output and filled context)", input: 58, cacheRead: 1048512, limit: 1048576, want: true, provider: "xiaomi", model: "mimo-v2.5-pro"},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:159
		{name: "treats a length stop below the desired output limit as recoverable", input: 3, cacheRead: 253584, cacheWrite: 25554, output: 16, limit: 128000, recoverable: true, want: true, api: APIOpenAIResponses, provider: "openai", model: "gpt-5.6-sol"},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:172
		{name: "does not recover a length stop that reached the desired output limit", input: 4062, output: 1024, limit: 1024, recoverable: true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:177
		{name: "treats zero-output length stops as recoverable without context metadata", input: 100, limit: 128000, recoverable: true, want: true},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:182
		{name: "does not treat normal length stops with output as context overflow", input: 1000, output: 4096, limit: 200000},
		// .upstream/v0.87.1/packages/ai/test/overflow.test.ts:187
		{name: "does not treat zero-output length stops far below context as context overflow", input: 100, limit: 200000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, provider, model := tc.api, tc.provider, tc.model
			if api == "" {
				api = APIOpenAICompletions
			}
			if provider == "" {
				provider = "test-provider"
			}
			if model == "" {
				model = "test-model"
			}
			message := AssistantMessage{API: api, Provider: provider, Model: model, StopReason: StopReasonLength, Usage: Usage{Input: tc.input, CacheRead: tc.cacheRead, CacheWrite: tc.cacheWrite, Output: tc.output, TotalTokens: tc.input + tc.cacheRead + tc.cacheWrite + tc.output}}
			got := IsContextOverflow(message, tc.limit)
			if tc.recoverable {
				got = IsRecoverableLength(message, tc.limit)
			}
			if got != tc.want {
				t.Fatalf("classification = %v, want %v", got, tc.want)
			}
		})
	}
}
