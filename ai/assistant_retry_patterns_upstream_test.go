package ai

import (
	"context"
	"reflect"
	"testing"
)

// Pi fb62c069b (packages/ai/test/retry.test.ts): Cloudflare HTTP 525 (SSL handshake failed)
// is retried like 520 and 524 when standalone, not inside a longer word or number.
func TestIsRetryableAssistantErrorCloudflare525(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		want    bool
	}{
		// Pi fb62c069b packages/ai/test/retry.test.ts
		{"525 SSL handshake HTML", "525 <!DOCTYPE html><title>SSL handshake failed</title>", true},
		{"525 bare", "525", true},
		{"525 in parentheses", "Cloudflare error (525): handshake failed", true},
		{"525 trailing", "HTTP status 525", true},
		// Status code boundary guards: 525 inside a longer number or identifier stays non-retryable.
		{"525 inside longer number", "400 payload 5250000 bytes exceeds limit", false},
		{"525 inside request id", "401 invalid key request_id=req_525abc", false},
		{"525 prefixed by letter", "error code x525", false},
		// Non-retryable quota exclusions are checked before the retryable patterns.
		{"525 with quota exceeded", "525 quota exceeded", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryableAssistantError(errorFaux(tc.message)); got != tc.want {
				t.Errorf("IsRetryableAssistantError(%q) = %v, want %v", tc.message, got, tc.want)
			}
		})
	}
}

// Pi 25190b72c (#12352, #12227, packages/ai/test/retry.test.ts): gateway mid-stream
// provider-unavailable and partial-response errors without an HTTP status code are retried;
// lasting "provider unavailable" without "temporarily" stays non-retryable.
func TestIsRetryableAssistantErrorGatewayProviderUnavailableAndPartialResponse(t *testing.T) {
	for _, errorMessage := range []string{
		// Pi 25190b72c packages/ai/test/retry.test.ts: matches gateway provider failure wording
		"The provider is temporarily unavailable. Retry shortly.",
		"The upstream provider is temporarily unavailable. Try again shortly.",
		"The provider ended the response before it was complete.",
		"The connection to the provider was reset. Retry shortly.",
		"The provider reported a failure while generating the response.",
		// Case-insensitivity and separator spellings accepted by `provider.?(?:is.?)?temporarily.?unavailable`.
		"PROVIDER TEMPORARILY UNAVAILABLE",
		"provider_is_temporarily_unavailable",
		"provider-temporarily-unavailable",
		"providertemporarilyunavailable",
		"ENDED THE RESPONSE BEFORE IT WAS COMPLETE",
		"CONNECTION TO THE PROVIDER WAS RESET",
		"REPORTED A FAILURE WHILE GENERATING THE RESPONSE",
	} {
		t.Run("matches gateway provider failure wording: "+errorMessage, func(t *testing.T) {
			if !IsRetryableAssistantError(errorFaux(errorMessage)) {
				t.Errorf("IsRetryableAssistantError(%q) = false, want true", errorMessage)
			}
		})
	}
	for _, errorMessage := range []string{
		// Pi 25190b72c packages/ai/test/retry.test.ts: does not match lasting provider unavailable wording
		"Provider unavailable for this model",
		"The provider is unavailable in your region",
		// Line breaks are not matched by JS `.` in `.?`.
		"provider\nis temporarily unavailable",
		// Unrelated upstream error wording stays non-retryable.
		"Upstream response did not include complete usage data",
		// Quota exclusions are checked before retryable patterns.
		"The provider is temporarily unavailable: insufficient_quota",
	} {
		t.Run("does not match lasting provider unavailable wording: "+errorMessage, func(t *testing.T) {
			if IsRetryableAssistantError(errorFaux(errorMessage)) {
				t.Errorf("IsRetryableAssistantError(%q) = true, want false", errorMessage)
			}
		})
	}
}

// Pi a00a5048d (packages/ai/test/retry.test.ts): temporary provider failures including
// admission ceiling, Anthropic streaming api_error events, provider finish_reason: error,
// empty/incomplete responses, and "repeat the request later".
func TestIsRetryableAssistantErrorMoreTemporaryProviderFailures(t *testing.T) {
	for _, errorMessage := range []string{
		// Pi a00a5048d packages/ai/test/retry.test.ts: retries temporary provider failures
		"Unable to process request. Repeat the request later.",
		"waited 73.4s in queue, reaching this model's admission ceiling of 60s; retry after the hinted delay",
		`{"type":"error","error":{"details":null,"type":"api_error","message":"Unable to complete this request right now. Please try again."},"request_id":"req_test_001"}`,
		"Provider finish_reason: error",
		"Provider returned an empty response",
		"Upstream error from Relace: The model stopped before completing the response.",
		// Case-insensitivity and JS \s whitespace around `:` in `"type"\s*:\s*"api_error"`.
		"ADMISSION CEILING",
		"\"type\" \t:\n\u00a0\"API_ERROR\"",
		"PROVIDER FINISH_REASON: ERROR",
		"RETURNED AN EMPTY RESPONSE",
		"STOPPED BEFORE COMPLETING THE RESPONSE",
		"REPEAT THE REQUEST LATER",
	} {
		t.Run("retries temporary provider failures: "+errorMessage, func(t *testing.T) {
			if !IsRetryableAssistantError(errorFaux(errorMessage)) {
				t.Errorf("IsRetryableAssistantError(%q) = false, want true", errorMessage)
			}
		})
	}
	for _, errorMessage := range []string{
		// Pi a00a5048d packages/ai/test/retry.test.ts: does not retry
		"The operation was aborted",
		`{"type":"error","error":{"type":"invalid_request_error","message":"messages: at least one message is required"}}`,
		"Provider finish_reason: content_filter",
		`{"type":"error","error":{"type":"api_error","message":"Monthly quota exceeded"}}`,
	} {
		t.Run("does not retry: "+errorMessage, func(t *testing.T) {
			if IsRetryableAssistantError(errorFaux(errorMessage)) {
				t.Errorf("IsRetryableAssistantError(%q) = true, want false", errorMessage)
			}
		})
	}
}

// Compaction/helper retry loop tests: RetryAssistantCall consults IsRetryableAssistantError on each
// failed attempt and retries until recovery for each of the three Pi commits (fb62c069b, 25190b72c, a00a5048d).
// The session turn retry path (coding/session.go -> session_recovery.go -> auto_recovery.go -> assistant_retry.go)
// is covered in coding/session_retry_patterns_upstream_test.go.
func TestRetryAssistantCallRetriesNewPatterns(t *testing.T) {
	for _, tc := range []struct {
		name         string
		errorMessage string
	}{
		{"Pi fb62c069b Cloudflare 525", "525 <!DOCTYPE html><title>SSL handshake failed</title>"},
		{"Pi 25190b72c gateway provider temporarily unavailable", "The provider is temporarily unavailable. Retry shortly."},
		{"Pi a00a5048d admission ceiling", "waited 73.4s in queue, reaching this model's admission ceiling of 60s; retry after the hinted delay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &retryRecorder{}
			calls := 0
			policy := &RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 1}
			response, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
				calls++
				if calls == 1 {
					return errorFaux(tc.errorMessage), nil
				}
				return retryFaux("recovered", "", ""), nil
			}, policy, recorder.callbacks())
			if err != nil || calls != 2 || !reflect.DeepEqual(response.Content, []AssistantContentBlock{TextContent{Text: "recovered"}}) {
				t.Fatalf("response=%#v err=%v calls=%d, want 2 calls and recovered", response, err, calls)
			}
			if len(recorder.scheduled) != 1 || recorder.scheduled[0][3] != tc.errorMessage {
				t.Fatalf("scheduled=%v, want 1 retry for %q", recorder.scheduled, tc.errorMessage)
			}
			if !reflect.DeepEqual(recorder.finished, [][3]any{{true, 1, ""}}) {
				t.Fatalf("finished=%v, want [{true 1 \"\"}]", recorder.finished)
			}
		})
	}
}
