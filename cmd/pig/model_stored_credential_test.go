package main

import (
	"context"
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

// TestBuildModelStoredCredentialOwnsProvider drives the startup model builder
// to the wire for providers other than Anthropic. Upstream resolveProviderAuth
// lets a stored auth.json credential own the provider and consults ambient
// environment keys only when nothing is stored.
func TestBuildModelStoredCredentialOwnsProvider(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provider   string
		model      string
		env        map[string]string
		stored     *ai.Credential
		wantHeader string
	}{
		{
			name:     "stored api key is sent without an env key",
			provider: "openai", model: "gpt-4o-mini",
			stored:     &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"},
			wantHeader: "Bearer stored-key",
		},
		{
			name:     "stored api key outranks the env key",
			provider: "openai", model: "gpt-4o-mini",
			env:        map[string]string{"OPENAI_API_KEY": "env-key"},
			stored:     &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"},
			wantHeader: "Bearer stored-key",
		},
		{
			name:     "env key is used when nothing is stored",
			provider: "openai", model: "gpt-4o-mini",
			env:        map[string]string{"OPENAI_API_KEY": "env-key"},
			wantHeader: "Bearer env-key",
		},
		{
			name:     "stored api key for an OpenAI-compatible built-in",
			provider: "deepseek", model: "deepseek-v4-pro",
			env:        map[string]string{"DEEPSEEK_API_KEY": "env-key"},
			stored:     &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-deepseek"},
			wantHeader: "Bearer stored-deepseek",
		},
		{
			name:     "stored subscription login outranks OPENAI_API_KEY",
			provider: "openai-codex", model: "gpt-5.5",
			env:        map[string]string{"OPENAI_API_KEY": "env-key"},
			stored:     &ai.Credential{Type: ai.CredentialOAuth, Refresh: "refresh", Access: codexStoredAccessToken, Expires: time.Now().Add(time.Hour).UnixMilli()},
			wantHeader: "Bearer " + codexStoredAccessToken,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
				t.Setenv(name, tc.env[name])
			}
			authorization := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case authorization <- r.Header.Get("Authorization"):
				default:
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			dir := t.TempDir()
			agentDirForModelOverride = dir
			t.Cleanup(func() { agentDirForModelOverride = "" })
			if tc.stored != nil {
				auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := auth.Set(tc.provider, *tc.stored); err != nil {
					t.Fatal(err)
				}
			}
			config := `{"providers":{"` + tc.provider + `":{"baseUrl":"` + server.URL + `"}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			model, _, _, err := buildModel(tc.provider+"/"+tc.model, codingagent.NewModelRegistry(dir))
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
			case got := <-authorization:
				if got != tc.wantHeader {
					t.Fatalf("Authorization = %q, want %q", got, tc.wantHeader)
				}
			default:
				t.Fatal("no request reached the provider")
			}
		})
	}
}

// codexStoredAccessToken is an unsigned JWT whose payload carries the
// chatgpt_account_id claim the Codex OAuth provider requires.
const codexStoredAccessToken = "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdCJ9fQ.sig"
