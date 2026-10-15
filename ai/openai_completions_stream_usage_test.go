package ai

import "testing"

// openai-completions.ts:834 sends stream_options {include_usage:true} unless compat.supportsUsageInStreaming is exactly false (`!== false`): an unset flag, which
// detectCompat defaults to true (line 1649), and true send it; false omits it.
//
// mutation-checked: gating on the flag being true (unset omits it), ignoring the flag, and inverting it each fail a row.
func TestOpenAICompletionsSendsStreamOptionsUnlessUsageInStreamingIsFalse(t *testing.T) {
	registerTestModel(t, GeneratedModel{ID: "usage-model", Provider: "custom-usage", Reasoning: false, MaxOutputTokens: 4096})
	for _, tc := range []struct {
		name string
		flag *bool
		want bool
	}{
		{"unset", nil, true},
		{"true", new(true), true},
		{"false", new(false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compat := &OpenAICompat{MaxTokensField: "max_tokens", SupportsUsageInStreaming: tc.flag}
			request := captureOpenAIRequestMap(t, "custom-usage", "usage-model", compat, StreamOptions{MaxTokens: 100})
			options, present := request["stream_options"].(map[string]any)
			if present != tc.want || (present && options["include_usage"] != true) {
				t.Fatalf("stream_options = %#v, want present=%v with include_usage true", request["stream_options"], tc.want)
			}
		})
	}
}
