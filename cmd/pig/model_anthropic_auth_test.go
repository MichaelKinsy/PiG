package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestBuildModelAnthropicAuthRequestShapes drives the startup model builder
// to the wire. Upstream resolveProviderAuth consults a stored credential
// before ambient env, and anthropicApiKeyAuth().resolve sends
// ANTHROPIC_AUTH_TOKEN as a bearer header ahead of ANTHROPIC_API_KEY.
func TestBuildModelAnthropicAuthRequestShapes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		env           map[string]string
		storedAccess  string
		storedKey     string
		apiKey        string
		authorization string
		userAgent     string
		systemBlocks  int
	}{
		{
			name:          "auth token outranks the API key",
			env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token", "ANTHROPIC_API_KEY": "api-key"},
			authorization: "Bearer auth-token", systemBlocks: 1,
		},
		{
			name:          "stored subscription login takes the Claude Code identity",
			env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"},
			storedAccess:  "sk-ant-oat01-stored",
			authorization: "Bearer sk-ant-oat01-stored", userAgent: "claude-cli/2.1.280", systemBlocks: 2,
		},
		{
			name:   "api key",
			env:    map[string]string{"ANTHROPIC_API_KEY": "api-key"},
			apiKey: "api-key", systemBlocks: 1,
		},
		{
			name:      "stored API key outranks ambient auth",
			env:       map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token", "ANTHROPIC_API_KEY": "ambient-key"},
			storedKey: "stored-key", apiKey: "stored-key", systemBlocks: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
				t.Setenv(name, tc.env[name])
			}
			var header http.Header
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				header = r.Header.Clone()
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			dir := t.TempDir()
			agentDirForModelOverride = dir
			t.Cleanup(func() { agentDirForModelOverride = "" })
			if tc.storedAccess != "" || tc.storedKey != "" {
				auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
				if err != nil {
					t.Fatal(err)
				}
				credential := ai.Credential{Type: ai.CredentialAPIKey, Key: tc.storedKey}
				if tc.storedAccess != "" {
					credential = ai.Credential{Type: ai.CredentialOAuth, Refresh: "refresh", Access: tc.storedAccess, Expires: time.Now().Add(time.Hour).UnixMilli()}
				}
				if err := auth.Set("anthropic", credential); err != nil {
					t.Fatal(err)
				}
			}
			config := `{"providers":{"anthropic":{"baseUrl":"` + server.URL + `"}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			model, _, _, err := buildModel("anthropic/claude-haiku-4-5", codingagent.NewModelRegistry(dir))
			if err != nil {
				t.Fatal(err)
			}
			transcript := ai.NormalizeContext(ai.Context{SystemPrompt: "System prompt.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}})
			stream, err := model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(context.Background()) {
			}
			if got := header.Get("X-Api-Key"); got != tc.apiKey {
				t.Errorf("X-Api-Key = %q, want %q", got, tc.apiKey)
			}
			if got := header.Get("Authorization"); got != tc.authorization {
				t.Errorf("Authorization = %q, want %q", got, tc.authorization)
			}
			if tc.userAgent != "" && header.Get("User-Agent") != tc.userAgent {
				t.Errorf("User-Agent = %q, want %q", header.Get("User-Agent"), tc.userAgent)
			}
			if system, _ := body["system"].([]any); len(system) != tc.systemBlocks {
				t.Errorf("system blocks = %d, want %d", len(system), tc.systemBlocks)
			}
		})
	}
}
