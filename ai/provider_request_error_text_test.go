//go:build !pig_strip_mistral_conversations

package ai

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Mistral SDK and openai-codex-responses.ts report these failures with their own text, which Pi shows unchanged:
// a transport failure is the fetch rejection "fetch failed" (probed: Pi 1.1.0 with an unreachable proxy), and a Codex token
// without an account id is openai-codex-responses.ts:1636 "Failed to extract accountId from token".
func TestProviderRequestFailureTextIsPis(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _ := closeHTTPConnection(t, writer)
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = connection.Close()
	}))
	defer server.Close()
	for name, tc := range map[string]struct {
		provider Provider
		want     string
	}{
		"mistral transport":     {NewMistralProvider(MistralConfig{BaseURL: server.URL, APIKey: "k", Model: "mistral-large-latest"}), "fetch failed"},
		"codex without account": {NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{BaseURL: server.URL, APIKey: "not-a-jwt", Model: "gpt-5.1"}), "Failed to extract accountId from token"},
		"pi-messages transport": {NewPiMessagesProvider(PiMessagesConfig{BaseURL: server.URL, APIKey: "k", Model: "pi-1", ProviderID: "radius"}), "fetch failed"},
	} {
		t.Run(name, func(t *testing.T) {
			if result := transportErrorTestResult(t, tc.provider); result.StopReason != StopReasonError || result.ErrorMessage != tc.want {
				t.Fatalf("result = reason %q error %q, want %q", result.StopReason, result.ErrorMessage, tc.want)
			}
		})
	}
}

// pi-messages.ts createErrorEvent builds its error message as an object literal with errorMessage before timestamp, unlike the
// other API modules, whose output object exists (timestamp included) before the request.
func TestPiMessagesErrorListsErrorMessageBeforeTimestampLikePi(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _ := closeHTTPConnection(t, writer)
		_ = connection.Close()
	}))
	defer server.Close()
	result := transportErrorTestResult(t, NewPiMessagesProvider(PiMessagesConfig{BaseURL: server.URL, APIKey: "k", Model: "pi-1", ProviderID: "radius"}))
	if !result.ErrorBeforeTimestamp {
		t.Fatalf("pi-messages error message lists timestamp first: %#v", result)
	}
}
