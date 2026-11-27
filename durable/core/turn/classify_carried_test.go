package turn

import "testing"

// Pi c719d5b0 (#11572, #11537): Zhipu/Z.ai usage limit code 1308 arrives as HTTP 429 and resets after hours, so it is not retried.
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

// Pi 9b88b94e2 packages/ai/test/retry.test.ts:106 (#12064): a gateway ends the stream with an Anthropic-style error event (type changed from api_error to gateway_notice so the row still isolates connection.?closed after Pi a00a5048d) reading
// "Connection closed."; the agent proxy's own message for a stream that hit EOF before a done or error event matches too.
// The quota/limit pattern is still checked first.
func TestConnectionClosedIsRetryable(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{`{"type":"error","error":{"type":"gateway_notice","message":"Connection closed."}}`, true},
		{"Connection closed by proxy server before the response completed", true},
		{"Connection closed.", true},
		{"connection_closed", true},
		{`{"type":"error","error":{"type":"api_error","message":"Connection closed. insufficient_quota"}}`, false},
		{"The connection was closed by the user", false},
	} {
		message := jobj("role", "assistant", "stopReason", "error", "errorMessage", tc.text)
		if got := IsRetryableAssistantError(message); got != tc.want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// Pi 566344046 packages/ai/test/retry.test.ts:100 (#11879): the Magpie gateway relaying a dropped upstream Anthropic stream. The generated
// pattern list gained the entry when it was regenerated for #12064.
func TestStreamCutShortMidReplyIsRetryable(t *testing.T) {
	message := jobj("role", "assistant", "stopReason", "error", "errorMessage", "Claude Code's stream from Anthropic was cut short after the reply had begun")
	if !IsRetryableAssistantError(message) {
		t.Error("a stream cut short after the reply had begun is not retryable")
	}
}

// Pi d3398f82 (#11492, #11494): LiteLLM's pre-call check ContextWindowExceededError is a context overflow.
// The pattern is pinned here by hand.
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

// Pi 4699b5b26 packages/ai/test/retry.test.ts:65,77 (#12128): a provider error that reports only a Node socket error code is retried;
// a code inside a longer identifier is not.
func TestNodeSocketErrorCodesAreRetryable(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"read ECONNRESET", true},
		{"connect ETIMEDOUT 140.82.113.3:443", true},
		{"write EPIPE", true},
		{"connect ECONNREFUSED 127.0.0.1:443", true},
		{"ECONNABORTED", true},
		{"Connection reset by peer", true},
		{"Invalid value EXECONNRESET", false},
		{"Unknown field ETIMEDOUTX", false},
		{"Invalid key ERR_EPIPE_ID", false},
	} {
		message := jobj("role", "assistant", "stopReason", "error", "errorMessage", tc.text)
		if got := IsRetryableAssistantError(message); got != tc.want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// Pi bb1a2b3542b5 packages/ai/test/retry.test.ts (#12124): the Codex WebSocket lifetime limit, as text or as the error code, is retried;
// a generic connection limit is not.
func TestCodexWebSocketConnectionLimitIsRetryable(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"Codex error: Responses websocket connection limit reached (60 minutes). Create a new websocket connection to continue.", true},
		{"Codex error: websocket_connection_limit_reached", true},
		{"WEBSOCKET CONNECTION LIMIT REACHED", true},
		{"websocket-connection-limit-reached", true},
		{"connection limit reached", false},
		{"websocket connection limit", false},
		{"connection limit reached on the websocket", false},
		{"websocket connection limit reached: insufficient_quota", false},
	} {
		message := jobj("role", "assistant", "stopReason", "error", "errorMessage", tc.text)
		if got := IsRetryableAssistantError(message); got != tc.want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// Pi 0fbdbd3f packages/ai/test/retry.test.ts:82-88 (#10996), node-fetch src/index.js:108:
// node-fetch formats transport errors as "request to <url> failed, reason: <reason>" (sometimes with an empty reason).
// The pattern in retry.ts:78 ("request to \\S+ failed, reason:") requires \S in the durable regex engine.
func TestNodeFetchRequestFailedIsRetryable(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"request to https://oauth2.googleapis.com/token failed, reason:", true},
		{"request to https://oauth2.googleapis.com/token failed, reason: connect ECONNREFUSED 127.0.0.1:9", true},
		{"request to https://api.example.com/v1/messages failed, reason: socket hang up", true},
		{"request to https://api.example.com/v1/messages failed, reason: write EPROTO", true},
		{"request to   failed, reason: empty url", false},
		{"request to https://api.example.com/v1/messages failed, reason: insufficient_quota", false},
	} {
		message := jobj("role", "assistant", "stopReason", "error", "errorMessage", tc.text)
		if got := IsRetryableAssistantError(message); got != tc.want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// Pi fb62c069b, 25190b72c, a00a5048d packages/ai/test/retry.test.ts: Cloudflare HTTP 525,
// gateway provider-unavailable and partial-response errors, and admission ceiling / temporary provider failures.
func TestPiMainRetryPatternsAreRetryable(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		// Pi fb62c069b
		{"525 <!DOCTYPE html><title>SSL handshake failed</title>", true},
		{"400 payload 5250000 bytes exceeds limit", false},
		// Pi 25190b72c (#12352, #12227)
		{"The provider is temporarily unavailable. Retry shortly.", true},
		{"The upstream provider is temporarily unavailable. Try again shortly.", true},
		{"The provider ended the response before it was complete.", true},
		{"The connection to the provider was reset. Retry shortly.", true},
		{"The provider reported a failure while generating the response.", true},
		{"Provider unavailable for this model", false},
		{"The provider is unavailable in your region", false},
		// Pi a00a5048d
		{"Unable to process request. Repeat the request later.", true},
		{"waited 73.4s in queue, reaching this model's admission ceiling of 60s; retry after the hinted delay", true},
		{`{"type":"error","error":{"details":null,"type":"api_error","message":"Unable to complete this request right now. Please try again."},"request_id":"req_011CgfE1bTgPTDsor4TsApFv"}`, true},
		{"Provider finish_reason: error", true},
		{"Provider returned an empty response", true},
		{"Upstream error from Relace: The model stopped before completing the response.", true},
		{"The operation was aborted", false},
		{`{"type":"error","error":{"type":"invalid_request_error","message":"messages: at least one message is required"}}`, false},
		{"Provider finish_reason: content_filter", false},
		{`{"type":"error","error":{"type":"api_error","message":"Monthly quota exceeded"}}`, false},
	} {
		message := jobj("role", "assistant", "stopReason", "error", "errorMessage", tc.text)
		if got := IsRetryableAssistantError(message); got != tc.want {
			t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
