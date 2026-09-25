package coding

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

// TestBuildModelStoredCredentialOwnsProvider mirrors upstream
// resolveProviderAuth (packages/ai/src/auth/resolve.ts): a stored auth.json
// credential owns the provider, and ambient environment keys are consulted
// only when nothing is stored. It drives the per-request ModelRuntime path to
// the wire for providers other than Anthropic.
func TestBuildModelStoredCredentialOwnsProvider(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provider   string
		model      string
		env        map[string]string
		stored     *ai.Credential
		runtimeKey string
		wantHeader string
	}{
		{
			name:     "runtime api key outranks stored and env keys",
			provider: "openai", model: "gpt-4o-mini",
			env:        map[string]string{"OPENAI_API_KEY": "env-key"},
			stored:     &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"},
			runtimeKey: "runtime-key",
			wantHeader: "Bearer runtime-key",
		},
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
			provider: "groq", model: "llama-3.3-70b-versatile",
			env:        map[string]string{"GROQ_API_KEY": "env-key"},
			stored:     &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-groq"},
			wantHeader: "Bearer stored-groq",
		},
		{
			name:     "stored subscription login outranks OPENAI_API_KEY",
			provider: "openai-codex", model: "gpt-5.5",
			env:        map[string]string{"OPENAI_API_KEY": "env-key"},
			stored:     &ai.Credential{Type: ai.CredentialOAuth, Refresh: "refresh", Access: codexTestAccessToken, Expires: time.Now().Add(time.Hour).UnixMilli()},
			wantHeader: "Bearer " + codexTestAccessToken,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"OPENAI_API_KEY", "GROQ_API_KEY"} {
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
			agentDir := t.TempDir()
			config := `{"providers":{"` + tc.provider + `":{"baseUrl":"` + server.URL + `"}}}`
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.stored != nil {
				auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := auth.Set(tc.provider, *tc.stored); err != nil {
					t.Fatal(err)
				}
			}
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			if tc.runtimeKey != "" {
				services.Registry().SetRuntimeAPIKey(tc.provider, tc.runtimeKey)
			}
			if !services.Registry().HasConfiguredAuth(tc.provider) {
				t.Fatalf("%s has no configured auth", tc.provider)
			}
			model := services.ModelRuntime().GetModel(tc.provider, tc.model)
			if model == nil {
				t.Fatalf("no model %s/%s", tc.provider, tc.model)
			}
			request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}
			_ = collectRuntimeEvents(context.Background(), services.ModelRuntime().Stream(context.Background(), model, request, ai.StreamOptions{}))
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

// codexTestAccessToken is an unsigned JWT whose payload carries the
// chatgpt_account_id claim the Codex OAuth provider requires.
const codexTestAccessToken = "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdCJ9fQ.sig"
