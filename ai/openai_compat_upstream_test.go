// SPDX-License-Identifier: MIT

package ai

import "testing"

func TestUpstreamCompatDetection(t *testing.T) {
	cases := []struct {
		provider, baseURL      string
		nonStandard, maxTokens bool
	}{
		{"bench", "http://127.0.0.1:9/v1", false, false},
		{"openai", "https://api.openai.com/v1", false, false},
		{"openrouter", "https://openrouter.ai/api/v1", false, false},
		{"ollama", "http://localhost:11434/v1", false, false},
		{"custom", "https://api.DeepSeek.com/v1", true, true},
		{"x", "https://llm.chutes.ai/v1", true, true},
		{"xai", "https://api.x.ai/v1", true, false},
		{"cerebras", "https://api.cerebras.ai/v1", true, false},
		{"cloudflare-workers-ai", "https://api.cloudflare.com/client/v4", true, false},
		{"x", "https://gateway.ai.cloudflare.com/v1/a/b/openai", true, true},
		{"zai-coding-cn", "https://open.bigmodel.cn/api", true, true},
	}
	for _, c := range cases {
		detected := detectOpenAICompletionsCompat(c.provider, c.baseURL, "")
		// isNonStandard is what supportsStore negates, and useMaxTokens picks the max_tokens field.
		if got := !*detected.SupportsStore; got != c.nonStandard {
			t.Errorf("non-standard(%q, %q) = %v, want %v", c.provider, c.baseURL, got, c.nonStandard)
		}
		if got := detected.MaxTokensField == "max_tokens"; got != c.maxTokens {
			t.Errorf("max_tokens(%q, %q) = %v, want %v", c.provider, c.baseURL, got, c.maxTokens)
		}
	}
}
