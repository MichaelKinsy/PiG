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
			model, _, _, err := buildModel(tc.provider+"/"+tc.model, testServices(t, dir))
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

// TestBuildModelStoredCredentialUsesServicesAgentDir proves the startup
// builder reads auth.json from the Services container's AgentDir rather than
// re-resolving the agent dir on its own: a credential that exists only under a
// decoy agent dir must not leak into the request.
func TestBuildModelStoredCredentialUsesServicesAgentDir(t *testing.T) {
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

	servicesDir := t.TempDir()
	decoyDir := t.TempDir()

	// Only the decoy agent dir holds a credential for myco.
	decoy, err := ai.NewAuthStorage(filepath.Join(decoyDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := decoy.Set("myco", ai.Credential{Type: ai.CredentialAPIKey, Key: "decoy-key"}); err != nil {
		t.Fatal(err)
	}
	// agentDirForModel() must not decide the auth.json path any more.
	agentDirForModelOverride = decoyDir
	t.Cleanup(func() { agentDirForModelOverride = "" })

	// services.AgentDir() holds no stored credential for myco.
	if err := os.WriteFile(filepath.Join(servicesDir, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `{"providers":{"myco":{"baseUrl":"` + server.URL + `","api":"openai-completions","models":[{"id":"m1","name":"M1"}]}}}`
	if err := os.WriteFile(filepath.Join(servicesDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	model, _, _, err := buildModel("myco/m1", testServices(t, servicesDir))
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
		if got == "Bearer decoy-key" {
			t.Fatalf("Authorization = %q: startup read auth.json from the wrong agent dir", got)
		}
	default:
		t.Fatal("no request reached the provider")
	}
}

// codexStoredAccessToken is an unsigned JWT whose payload carries the
// chatgpt_account_id claim the Codex OAuth provider requires.
const codexStoredAccessToken = "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdCJ9fQ.sig"
