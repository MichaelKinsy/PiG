package turn

import "testing"

// Pi c719d5b0 (#11572, #11537, pig divergence (D217)): Zhipu/Z.ai usage limit code 1308 arrives as HTTP 429 and resets after hours, so it is not retried.
// Pi added no test; the inputs are the provider's body shapes.
func TestZhipuUsageLimit1308IsNotRetryable(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{`429 {"error":{"code":"1308","message":"Usage limit reached for 5 hour. Your limit will reset at 2026-10-29 18:00:00"}}`, false},
		{`429 {"error":{"code":1308,"message":"Usage limit reached"}}`, false},
		{`429 {"error":{"code":"13081","message":"Too many requests"}}`, true},
	} {
		message := jobj("role", "assistant", "stopReason", "error", "errorMessage", tc.text)
		if got := IsRetryableAssistantError(message); got != tc.want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// Pi d3398f82 (#11492, #11494, pig divergence (D217)): LiteLLM's pre-call check ContextWindowExceededError is a context overflow.
// The pinned corpus (classify.hex) predates it, so the pattern is carried by oracle/gen_patterns.mjs and pinned here.
func TestLiteLLMContextWindowExceededIsOverflow(t *testing.T) {
	const litellm = "400: {\"message\":\"litellm.ContextWindowExceededError: litellm.BadRequestError: litellm._pre_call_checks: Context Window exceeded for given call. No models have context window large enough for this call.\\nModel=Qwen3.8-Flash-Next, Max Input Tokens=262144, Got=265262\\nmodel=Qwen3.8-Flash-Next. context_window_fallbacks=None. fallbacks=[].\"}"
	for _, tc := range []struct {
		stop, text string
		want       bool
	}{
		{"error", litellm, true},
		{"stop", litellm, false},
		{"error", "Throttling error: Context Window exceeded for given call", false},
	} {
		message := jobj("role", "assistant", "stopReason", tc.stop, "provider", "ollama", "errorMessage", tc.text)
		if got := IsContextOverflow(message); got != tc.want {
			t.Errorf("IsContextOverflow(%s %q) = %v, want %v", tc.stop, tc.text, got, tc.want)
		}
	}
}
