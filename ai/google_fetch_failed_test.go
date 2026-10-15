package ai

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// @google/genai calls fetch, so a failure before response headers rejects with undici's TypeError and Pi shows its message,
// "fetch failed" (probed: Pi 1.1.0 with an unreachable proxy prints exactly that). The Go client's "Post <url>:" decoration and a
// provider prefix are not part of it.
func TestGoogleTransportFailureBeforeHeadersIsFetchFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _ := closeHTTPConnection(t, writer)
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}))
	defer server.Close()
	provider := NewGoogleProvider(GoogleConfig{BaseURL: server.URL, APIKey: "test-key", Model: "gemini-test", ProviderID: "google"})
	result := transportErrorTestResult(t, provider)
	if result.StopReason != StopReasonError || result.ErrorMessage != "fetch failed" {
		t.Fatalf("result = reason %q error %q, want Pi's %q", result.StopReason, result.ErrorMessage, "fetch failed")
	}
	if !IsRetryableAssistantError(*result) {
		t.Fatalf("fetch failed is retryable in Pi (assistant-retry patterns): %q", result.ErrorMessage)
	}
}
