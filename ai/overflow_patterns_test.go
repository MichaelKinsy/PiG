package ai

import "testing"

// packages/ai/src/utils/overflow.ts:37-63 (OVERFLOW_PATTERNS), :65 (CEREBRAS_BODYLESS_OVERFLOW_PATTERN), :151-169 (isContextOverflow cases 2 and 3).
func TestOverflowPatternsEachMatchTheirProviderMessage(t *testing.T) {
	samples := overflowPatternSamples()
	patterns := GetOverflowPatterns()
	if len(patterns) != len(samples) {
		t.Fatalf("%d patterns, %d samples", len(patterns), len(samples))
	}
	for i, sample := range samples {
		if !patterns[i].MatchString(sample) {
			t.Errorf("pattern %d %q does not match %q", i, patterns[i], sample)
		}
		message := AssistantMessage{Provider: "p", StopReason: StopReasonError, ErrorMessage: sample}
		if !IsContextOverflow(message, 0) {
			t.Errorf("IsContextOverflow(%q) = false", sample)
		}
		// Case 1 needs stopReason "error" and a message; a success with the same text is not an overflow.
		message.StopReason = StopReasonStop
		if IsContextOverflow(message, 0) {
			t.Errorf("a stopped message with %q must not overflow", sample)
		}
	}
	// :65 JavaScript \s covers every Unicode space, so a no-break space still separates the Cerebras status text.
	message := AssistantMessage{Provider: "cerebras", StopReason: StopReasonError, ErrorMessage: "400\u00a0status code\u3000(no body)"}
	if !IsContextOverflow(message, 0) {
		t.Error("Cerebras bodyless overflow must accept JavaScript whitespace")
	}
	// :43 the same holds between "maximum context length" and the parenthesized limit.
	message = AssistantMessage{Provider: "p", StopReason: StopReasonError, ErrorMessage: "exceeds model's maximum context length\u00a0(262144)"}
	if !IsContextOverflow(message, 0) {
		t.Error("parenthesized maximum context length must accept JavaScript whitespace")
	}
	// :44 JavaScript's "." stops at every line terminator (\n, \r, U+2028, U+2029); Go's stops only at \n. Measured with Node 24:
	// /input token count.*exceeds the maximum/i is false for "\r" and U+2028 and true for a tab or U+0085.
	for text, want := range map[string]bool{
		"input token count\r exceeds the maximum":     false,
		"input token count\u2028 exceeds the maximum": false,
		"input token count\u2029 exceeds the maximum": false,
		"input token count\n exceeds the maximum":     false,
		"input token count\t exceeds the maximum":     true,
		"input token count\u0085 exceeds the maximum": true,
	} {
		if got := IsContextOverflow(AssistantMessage{Provider: "p", StopReason: StopReasonError, ErrorMessage: text}, 0); got != want {
			t.Errorf("IsContextOverflow(%q) = %v, want %v", text, got, want)
		}
	}
	// getOverflowPatterns returns a copy.
	patterns[0] = nil
	if GetOverflowPatterns()[0] == nil {
		t.Error("GetOverflowPatterns exposes the internal slice")
	}
}

func TestOverflowUsageBoundaries(t *testing.T) {
	// :151-157 silent overflow needs input+cacheRead strictly above the window.
	stop := AssistantMessage{StopReason: StopReasonStop, Usage: Usage{Input: 100, CacheRead: 28}}
	if IsContextOverflow(stop, 128) {
		t.Error("input+cacheRead equal to the window is not overflow")
	}
	stop.Usage.CacheRead = 29
	if !IsContextOverflow(stop, 128) {
		t.Error("input+cacheRead above the window is overflow")
	}
	// :159-168 a length stop needs zero output and at least 99% of the window.
	length := AssistantMessage{StopReason: StopReasonLength, Usage: Usage{Input: 990}}
	if !IsContextOverflow(length, 1000) {
		t.Error("99% of the window with no output is overflow")
	}
	length.Usage.Input = 989
	if IsContextOverflow(length, 1000) {
		t.Error("below 99% of the window is not overflow")
	}
	length.Usage.Input, length.Usage.Output = 1000, 1
	if IsContextOverflow(length, 1000) {
		t.Error("a length stop with output is not overflow")
	}
	// A non-overflow pattern wins over an overflow pattern in the same message (:141-149).
	for _, text := range []string{"rate limit: too many tokens", "Too many requests: too many tokens", "Throttling error: too many tokens", "Service unavailable: too many tokens"} {
		if IsContextOverflow(AssistantMessage{Provider: "p", StopReason: StopReasonError, ErrorMessage: text}, 0) {
			t.Errorf("%q must not be an overflow", text)
		}
	}
	if !IsContextOverflow(AssistantMessage{Provider: "p", StopReason: StopReasonError, ErrorMessage: "wrapped Throttling error: too many tokens"}, 0) {
		t.Error("the Bedrock prefix exclusion is anchored to the start of the message")
	}
}

// overflowPatternSamples has one provider message per OVERFLOW_PATTERNS entry, in pattern order.
func overflowPatternSamples() []string {
	return []string{
		"prompt is too long: 213462 tokens > 200000 maximum",
		"Prompt exceeds max length",
		`413 {"error":{"type":"request_too_large","message":"Request exceeds the maximum size"}}`,
		"Input is too long for requested model",
		"Your input exceeds the context window of this model",
		"Requested token count exceeds the model's maximum context length of 131,072 tokens",
		"The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens",
		"Please reduce the length of the messages or completion",
		"This endpoint's maximum context length is 1000 tokens. However, you requested about 2000 tokens",
		"Input length 5 exceeds the maximum allowed input length of 4 token",
		"The input (5 tokens) is longer than the model's context length (4 tokens).",
		"prompt token count of 5 exceeds the limit of 4",
		"the request exceeds the available context size, try increasing it",
		"tokens to keep from the initial prompt is greater than the context length",
		"invalid params, context window exceeds limit",
		"Your request exceeded model token limit: 5 (requested: 6)",
		"Prompt contains 5 tokens ... too large for model with 4 maximum context length",
		"Prompt has 5 tokens, but the configured context size is 4 tokens",
		"model_context_window_exceeded",
		"prompt too long; exceeded context length by 5 tokens",
		"Range of input length should be [1, 4]",
		"context_length_exceeded",
		"too many tokens",
		"token limit exceeded",
	}
}
