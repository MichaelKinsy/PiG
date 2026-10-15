//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"testing"
)

func TestMistralAppliesPreparedHeadersAndRequestSessionID(t *testing.T) {
	server, requests := rejectingProviderServer(t)
	provider := NewMistralProvider(MistralConfig{
		APIKey: "configured-key", Model: "mistral-test", BaseURL: server.URL,
		ExtraHeaders: map[string]string{"X-Configured": "configured", "X-Delete": "configured"},
		SessionID:    "configured-session",
	})
	options := requestHeaderOptions()
	options.SessionID = "request-session"
	options.Headers["Authorization"] = nil
	if _, err := provider.Stream(context.Background(), providerWireTranscript(), options); err == nil {
		t.Fatal("Stream error = nil, want test rejection")
	}
	request := <-requests
	assertPreparedProviderHeaders(t, request)
	if request.header.Get("Authorization") != "" {
		t.Fatalf("Authorization = %q, want deleted", request.header.Get("Authorization"))
	}
	if got := request.header.Get("x-affinity"); got != "request-session" {
		t.Fatalf("x-affinity = %q, want request-session", got)
	}
}
