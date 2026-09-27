package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestStartupModelUsesCatalogBaseURL proves the CLI startup builder carries the
// catalog base URL for a provider whose endpoint differs from the client
// default. Before the fix the startup builder took the base URL from
// registry.Resolve, which never returns a built-in provider's catalog base URL,
// so a stored opencode-go key was sent to https://api.openai.com/v1 and the
// first request failed with HTTP 401.
func TestStartupModelUsesCatalogBaseURL(t *testing.T) {
	for _, tc := range []struct{ spec, wantBaseURL string }{
		{"opencode-go/deepseek-v4.1-flash", "https://opencode.ai/zen/go/v1"},
		{"opencode-go/minimax-m3", "https://opencode.ai/zen/go"},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			dir := isolateProviderAuthEnv(t)
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"opencode-go":{"type":"api_key","key":"oc_sk_test"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			model, _, _, err := resolveModel(tc.spec, "", codingagent.Settings{}, testServices(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if got := model.ProviderMeta.BaseURL; got != tc.wantBaseURL {
				t.Fatalf("baseURL = %q, want %q", got, tc.wantBaseURL)
			}
		})
	}
}

// TestStartupModelUsesCatalogAPIKind drives the startup builder to the wire: a
// catalog anthropic-messages model under an OpenAI-compatible provider must use
// the Anthropic client (/v1/messages), not OpenAI chat completions. Before the
// fix the startup builder branched on the provider id and sent every
// non-pi-messages model through the OpenAI completions client.
func TestStartupModelUsesCatalogAPIKind(t *testing.T) {
	for _, tc := range []struct{ spec, wantPath, wantAPI string }{
		{"opencode-go/minimax-m3", "/v1/messages", "anthropic-messages"},
		{"opencode-go/deepseek-v4.1-flash", "/chat/completions", "openai-completions"},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			paths := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case paths <- r.URL.Path:
				default:
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			dir := isolateProviderAuthEnv(t)
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"opencode-go":{"type":"api_key","key":"oc_sk_test"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			config := `{"providers":{"opencode-go":{"baseUrl":"` + server.URL + `"}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			model, _, _, err := resolveModel(tc.spec, "", codingagent.Settings{}, testServices(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(model.ProviderMeta.API); got != tc.wantAPI {
				t.Fatalf("API = %q, want %q", got, tc.wantAPI)
			}
			transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}})
			stream, err := model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(context.Background()) {
			}
			select {
			case got := <-paths:
				if got != tc.wantPath {
					t.Fatalf("request path = %q, want %q", got, tc.wantPath)
				}
			default:
				t.Fatal("no request reached the provider")
			}
		})
	}
}
