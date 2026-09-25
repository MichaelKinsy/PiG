package ai

import "testing"

// Ports packages/ai/test/overflow.test.ts.

func overflowErrorMessage(errorMessage, provider string) AssistantMessage {
	return AssistantMessage{API: "openai-completions", Provider: provider, Model: "qwen3.5:35b", StopReason: StopReasonError, ErrorMessage: errorMessage}
}

func lengthStopMessage(input, cacheRead, output int) AssistantMessage {
	return AssistantMessage{
		API: "openai-completions", Provider: "test-provider", Model: "test-model",
		Usage:      Usage{Input: input, Output: output, CacheRead: cacheRead, TotalTokens: input + cacheRead + output},
		StopReason: StopReasonLength,
	}
}

func TestIsContextOverflowUpstreamCases(t *testing.T) {
	cases := []struct {
		name          string
		message       AssistantMessage
		contextWindow int
		want          bool
	}{
		{"explicit Ollama prompt-too-long", overflowErrorMessage("400 `prompt too long; exceeded max context length by 100918 tokens`", "ollama"), 32768, true},
		{"z.ai prompt-too-long", overflowErrorMessage(`400 {"code":"1261","message":"Prompt too long"}`, "zai"), 1048576, true},
		{"Together AI context length", overflowErrorMessage("400 The input (516368 tokens) is longer than the model's context length (262144 tokens).", "ollama"), 262144, true},
		{"LiteLLM-wrapped maximum context length", overflowErrorMessage("Error: 503 litellm.ServiceUnavailableError: litellm.MidStreamFallbackError: litellm.APIConnectionError: APIConnectionError: OpenAIException - Requested token count exceeds the model's maximum context length of 131072 tokens.", "ollama"), 131072, true},
		{"parenthesized maximum context length", overflowErrorMessage("Error: 400 Input length (265330) exceeds model's maximum context length (262144).", "ollama"), 262144, true},
		{"OpenRouter Poolside maximum allowed input length", overflowErrorMessage("Provider returned error: Input length 131393 exceeds the maximum allowed input length of 131040 tokens.", "ollama"), 131072, true},
		{"DS4 configured context size", overflowErrorMessage("400 Prompt has 256468 tokens, but the configured context size is 256000 tokens", "ollama"), 256000, true},
		{"DS4 configured context size with commas", overflowErrorMessage("Prompt has 5,958,968 tokens, but the configured context size is 256,000 tokens", "ollama"), 256000, true},
		{"generic non-overflow Ollama error", overflowErrorMessage("500 `model runner crashed unexpectedly`", "ollama"), 32768, false},
		{"Cerebras bodyless 400", overflowErrorMessage("400 status code (no body)", "cerebras"), 131072, true},
		{"Cerebras bodyless 413", overflowErrorMessage("413 status code (no body)", "cerebras"), 131072, true},
		{"bodyless 400 from another provider", overflowErrorMessage("400 status code (no body)", "opencode-go"), 1000000, false},
		{"bodyless 413 from another provider", overflowErrorMessage("413 status code (no body)", "opencode-go"), 1000000, false},
		{"Bedrock throttling too many tokens", overflowErrorMessage("Throttling error: Too many tokens, please wait before trying again.", "ollama"), 200000, false},
		{"Bedrock service unavailable", overflowErrorMessage("Service unavailable: The service is temporarily unavailable.", "ollama"), 200000, false},
		{"generic rate limit", overflowErrorMessage("Rate limit exceeded, please retry after 30 seconds.", "ollama"), 200000, false},
		{"HTTP 429 style", overflowErrorMessage("Too many requests. Please slow down.", "ollama"), 200000, false},
		{"Xiaomi length stop with zero output and filled context", lengthStopMessage(58, 1048512, 0), 1048576, true},
		{"normal length stop with output", lengthStopMessage(1000, 0, 4096), 200000, false},
		{"zero-output length stop far below context", lengthStopMessage(100, 0, 0), 200000, false},
	}
	for _, tc := range cases {
		if got := IsContextOverflow(tc.message, tc.contextWindow); got != tc.want {
			t.Errorf("%s: IsContextOverflow = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsRecoverableLengthUpstreamCases(t *testing.T) {
	below := lengthStopMessage(3, 253584, 16)
	below.Usage.CacheWrite = 25554
	if !IsRecoverableLength(below, 128000) {
		t.Error("a length stop below the desired output limit must be recoverable")
	}
	if IsRecoverableLength(lengthStopMessage(4062, 0, 1024), 1024) {
		t.Error("a length stop that reached the desired output limit must not be recoverable")
	}
	if !IsRecoverableLength(lengthStopMessage(100, 0, 0), 128000) {
		t.Error("a zero-output length stop must be recoverable without context metadata")
	}
}

func TestIsContextOverflowSilentUsage(t *testing.T) {
	message := AssistantMessage{StopReason: StopReasonStop, Usage: Usage{Input: 150000}}
	if !IsContextOverflow(message, 128000) {
		t.Error("input above the context window must be a silent overflow")
	}
	if IsContextOverflow(message, 0) {
		t.Error("without a context window the usage cases are disabled")
	}
}
