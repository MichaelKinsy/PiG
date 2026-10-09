package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type capturedProviderRequest struct {
	header http.Header
	host   string
	body   []byte
}

func rejectingProviderServer(t *testing.T) (*httptest.Server, <-chan capturedProviderRequest) {
	t.Helper()
	requests := make(chan capturedProviderRequest, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests <- capturedProviderRequest{header: request.Header.Clone(), host: request.Host, body: body}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("x-amzn-errortype", "ValidationException")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"message":"expected test rejection"}`))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func providerWireTranscript() TranscriptContext {
	return NormalizeContext(Context{
		SystemPrompt: "system",
		Messages:     []Message{UserMessage{Content: UserText("hello")}},
	})
}

func requestHeaderOptions() StreamOptions {
	return StreamOptions{Headers: ProviderHeaders{
		"x-configured": new("request"),
		"X-Delete":     nil,
		"X-Request":    new("yes"),
	}}
}

func assertPreparedProviderHeaders(t *testing.T, request capturedProviderRequest) {
	t.Helper()
	if got := request.header.Get("X-Configured"); got != "request" {
		t.Fatalf("X-Configured = %q, want request", got)
	}
	if got := request.header.Get("X-Delete"); got != "" {
		t.Fatalf("X-Delete = %q, want deleted", got)
	}
	if got := request.header.Get("X-Request"); got != "yes" {
		t.Fatalf("X-Request = %q, want yes", got)
	}
}

func TestAnthropicAppliesPreparedHeadersAndRequestEnvironment(t *testing.T) {
	server, requests := rejectingProviderServer(t)
	provider := NewAnthropicProvider(AnthropicConfig{
		APIKey: "configured-key", Model: "claude-test", BaseURL: server.URL,
		ExtraHeaders: map[string]string{"X-Configured": "configured", "X-Delete": "configured"},
		Env:          ProviderEnv{"PI_CACHE_RETENTION": "long"},
	})
	options := requestHeaderOptions()
	options.Env = ProviderEnv{"PI_CACHE_RETENTION": "none"}
	options.Headers["x-api-key"] = nil
	stream, err := provider.Stream(context.Background(), providerWireTranscript(), options)
	result := requireAnthropicSetupError(t, stream, err)
	if !strings.Contains(result.ErrorMessage, "expected test rejection") {
		t.Fatalf("Stream rejection = %q", result.ErrorMessage)
	}
	request := <-requests
	assertPreparedProviderHeaders(t, request)
	if request.header.Get("x-api-key") != "" {
		t.Fatalf("x-api-key = %q, want deleted", request.header.Get("x-api-key"))
	}
	// anthropic-messages.ts:60-84: env=none falls back to short rather than disabling cache.
	if !strings.Contains(string(request.body), `"cache_control":{"type":"ephemeral"}`) || strings.Contains(string(request.body), `"ttl"`) {
		t.Fatalf("request environment did not override configured long retention with short: %s", request.body)
	}
}

func TestGoogleAppliesPreparedHeaders(t *testing.T) {
	server, requests := rejectingProviderServer(t)
	provider := NewGoogleProvider(GoogleConfig{
		APIKey: "configured-key", Model: "gemini-test", BaseURL: server.URL,
		ExtraHeaders: map[string]string{"X-Configured": "configured", "X-Delete": "configured"},
	})
	options := requestHeaderOptions()
	options.Headers["x-goog-api-key"] = nil
	if _, err := provider.Stream(context.Background(), providerWireTranscript(), options); err == nil {
		t.Fatal("Stream error = nil, want test rejection")
	}
	request := <-requests
	assertPreparedProviderHeaders(t, request)
	if request.header.Get("x-goog-api-key") != "" {
		t.Fatalf("x-goog-api-key = %q, want deleted", request.header.Get("x-goog-api-key"))
	}
}
