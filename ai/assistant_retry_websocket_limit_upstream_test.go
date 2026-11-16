package ai

import "testing"

// Pi bb1a2b3542b5 (#12229, #12124): the Codex WebSocket lifetime limit is recoverable on a fresh connection. The pattern is
// websocket.?connection.?limit.?reached, matched over the error text with Pi's `i` flag, as a code or as text, whatever provider reported it.
// upstream: packages/ai/test/retry.test.ts "matches the Codex websocket connection lifetime limit: %s" and "does not match a generic connection limit"
func TestIsRetryableAssistantErrorCodexWebSocketConnectionLimit(t *testing.T) {
	for _, errorMessage := range []string{
		"Codex error: Responses websocket connection limit reached (60 minutes). Create a new websocket connection to continue.",
		"Codex error: websocket_connection_limit_reached",
		// The same text from any other path, a separator `.?` accepts, and the pattern's case-insensitive flag.
		`{"type":"error","error":{"code":"websocket_connection_limit_reached"}}`,
		"WEBSOCKET CONNECTION LIMIT REACHED",
		"websocket-connection-limit-reached",
		"websocketconnectionlimitreached",
	} {
		t.Run("matches the Codex websocket connection lifetime limit: "+errorMessage, func(t *testing.T) {
			if !socketCodeRetryable(errorMessage) {
				t.Fatalf("IsRetryableAssistantError(%q) = false, want true", errorMessage)
			}
		})
	}
	for _, errorMessage := range []string{
		"connection limit reached",
		"websocket connection limit",
		"websocket limit reached",
		"websocket connection  limit reached",
		"connection limit reached on the websocket",
	} {
		t.Run("does not match a generic connection limit: "+errorMessage, func(t *testing.T) {
			if socketCodeRetryable(errorMessage) {
				t.Fatalf("IsRetryableAssistantError(%q) = true, want false", errorMessage)
			}
		})
	}
	// The non-retryable quota patterns are still checked first.
	if socketCodeRetryable("websocket connection limit reached: insufficient_quota") {
		t.Fatal("a quota error that names the websocket limit is retryable")
	}
}
