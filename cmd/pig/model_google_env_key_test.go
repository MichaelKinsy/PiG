package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's google provider authenticates only from GEMINI_API_KEY
// (providers/google.ts envApiKeyAuth), and google-vertex only from
// GOOGLE_CLOUD_API_KEY, else ADC (providers/google-vertex.ts). Neither reads
// GOOGLE_API_KEY, and Vertex never borrows the Gemini key.
func TestBuildModelGoogleKeysMatchUpstreamEnvVars(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		env            map[string]string
		wantKey        string
	}{
		{name: "google ignores GOOGLE_API_KEY", provider: "google", env: map[string]string{"GOOGLE_API_KEY": "google-api"}},
		{name: "google reads GEMINI_API_KEY", provider: "google", env: map[string]string{"GEMINI_API_KEY": "gemini", "GOOGLE_API_KEY": "google-api"}, wantKey: "gemini"},
		{name: "vertex ignores Gemini and GOOGLE_API_KEY", provider: "google-vertex", env: map[string]string{"GEMINI_API_KEY": "gemini", "GOOGLE_API_KEY": "google-api"}},
		{name: "vertex reads GOOGLE_CLOUD_API_KEY", provider: "google-vertex", env: map[string]string{"GEMINI_API_KEY": "gemini", "GOOGLE_CLOUD_API_KEY": "cloud"}, wantKey: "cloud"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_CLOUD_API_KEY"} {
				t.Setenv(name, tc.env[name])
			}
			keys := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case keys <- r.Header.Get("x-goog-api-key") + r.URL.Query().Get("key"):
				default:
				}
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			dir := t.TempDir()
			agentDirForModelOverride = dir
			t.Cleanup(func() { agentDirForModelOverride = "" })
			config := `{"providers":{"` + tc.provider + `":{"baseUrl":"` + server.URL + `"}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			model, _, _, err := buildModel(tc.provider+"/gemini-2.5-flash", codingagent.NewModelRegistry(dir))
			if err != nil {
				t.Fatal(err)
			}
			transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}})
			stream, err := model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(context.Background()) {
			}
			select {
			case got := <-keys:
				if got != tc.wantKey {
					t.Fatalf("request API key = %q, want %q", got, tc.wantKey)
				}
			default:
				t.Fatal("no request reached the provider")
			}
		})
	}
}
