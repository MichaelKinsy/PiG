package turn

import "testing"

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
