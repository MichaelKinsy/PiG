package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const opencodeGoTestKey = "oc_sk_test"

// providerRequest is the part of a provider request these tests check.
type providerRequest struct {
	path, authorization, apiKey string
}

// writeOpencodeGoAuth stores an opencode-go API key in dir/auth.json and, when
// baseURL is set, points the provider at it through models.json.
func writeOpencodeGoAuth(t *testing.T, dir, baseURL string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"opencode-go":{"type":"api_key","key":"`+opencodeGoTestKey+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if baseURL == "" {
		return
	}
	config := `{"providers":{"opencode-go":{"baseUrl":"` + baseURL + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

// recordingProviderServer answers every request with an empty event stream and
// records the first request.
func recordingProviderServer(t *testing.T) (*httptest.Server, <-chan providerRequest) {
	t.Helper()
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- providerRequest{path: r.URL.Path, authorization: r.Header.Get("Authorization"), apiKey: r.Header.Get("x-api-key")}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, requests
}

// streamOnce sends one user turn through model and returns the request the
// provider received.
func streamOnce(t *testing.T, model *ai.Model, requests <-chan providerRequest) providerRequest {
	t.Helper()
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}})
	stream, err := model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(context.Background()) {
	}
	select {
	case got := <-requests:
		return got
	default:
		t.Fatal("no request reached the provider")
		return providerRequest{}
	}
}

// assertStoredKeySent checks that the stored opencode-go key authenticated
// the request, as a bearer token or an Anthropic x-api-key header.
func assertStoredKeySent(t *testing.T, got providerRequest) {
	t.Helper()
	if got.authorization != "Bearer "+opencodeGoTestKey && got.apiKey != opencodeGoTestKey {
		t.Fatalf("stored key not sent: Authorization=%q x-api-key=%q", got.authorization, got.apiKey)
	}
}

// TestStartupModelUsesCatalogBaseURL checks that the startup builder uses the
// catalog base URL for a provider whose endpoint differs from the client
// default.
func TestStartupModelUsesCatalogBaseURL(t *testing.T) {
	for _, spec := range []string{"opencode-go/deepseek-v4.1-flash", "opencode-go/minimax-m3"} {
		t.Run(spec, func(t *testing.T) {
			catalog, ok := ai.LookupModelExact(spec)
			if !ok || catalog.BaseURL == "" {
				t.Fatalf("catalog has no base URL for %s", spec)
			}
			dir := isolateProviderAuthEnv(t)
			writeOpencodeGoAuth(t, dir, "")
			model, _, _, err := resolveModel(spec, "", codingagent.Settings{}, testServices(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if got := model.ProviderMeta.BaseURL; got != catalog.BaseURL {
				t.Fatalf("baseURL = %q, want %q", got, catalog.BaseURL)
			}
		})
	}
}

// TestStartupModelUsesCatalogAPIKind checks that the startup builder picks the
// client from the catalog API kind and authenticates with the stored key: an
// anthropic-messages model under an OpenAI-compatible provider uses
// /v1/messages, and an openai-completions model uses /chat/completions.
func TestStartupModelUsesCatalogAPIKind(t *testing.T) {
	for _, tc := range []struct{ spec, wantPath, wantAPI string }{
		{"opencode-go/minimax-m3", "/v1/messages", "anthropic-messages"},
		{"opencode-go/deepseek-v4.1-flash", "/chat/completions", "openai-completions"},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			server, requests := recordingProviderServer(t)
			dir := isolateProviderAuthEnv(t)
			writeOpencodeGoAuth(t, dir, server.URL)
			model, _, _, err := resolveModel(tc.spec, "", codingagent.Settings{}, testServices(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(model.ProviderMeta.API); got != tc.wantAPI {
				t.Fatalf("API = %q, want %q", got, tc.wantAPI)
			}
			got := streamOnce(t, model, requests)
			if !strings.HasSuffix(got.path, tc.wantPath) {
				t.Fatalf("request path = %q, want suffix %q", got.path, tc.wantPath)
			}
			assertStoredKeySent(t, got)
		})
	}
}

// TestSavedDefaultModelUsesCatalogConstruction checks the saved defaultModel
// path: a new session starting from settings builds the model the same way as
// --model.
func TestSavedDefaultModelUsesCatalogConstruction(t *testing.T) {
	server, requests := recordingProviderServer(t)
	dir := isolateProviderAuthEnv(t)
	writeOpencodeGoAuth(t, dir, server.URL)
	services := testServices(t, dir)
	settings := codingagent.Settings{DefaultProvider: "opencode-go", DefaultModel: "deepseek-v4.1-flash"}
	selected, err := selectStartupModel(context.Background(), startupModelOptions{}, settings, services)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Model == nil {
		t.Fatal("no model selected")
	}
	if got := selected.Model.ProviderMeta.ProviderID + "/" + selected.Model.ID; got != "opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("selected %s, want opencode-go/deepseek-v4.1-flash", got)
	}
	got := streamOnce(t, selected.Model, requests)
	if !strings.HasSuffix(got.path, "/chat/completions") {
		t.Fatalf("request path = %q, want suffix /chat/completions", got.path)
	}
	assertStoredKeySent(t, got)
}
