package coding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

type codingContextRefreshProvider struct {
	legacyCalls  *int
	contextCalls *int
}

func (p codingContextRefreshProvider) ID() string               { return "anthropic" }
func (p codingContextRefreshProvider) Name() string             { return "Anthropic context probe" }
func (p codingContextRefreshProvider) UsesCallbackServer() bool { return false }
func (p codingContextRefreshProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("login not supported")
}
func (p codingContextRefreshProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	(*p.legacyCalls)++
	return ai.OAuthCredentials{}, errors.New("context-free refresh called")
}
func (p codingContextRefreshProvider) RefreshTokenContext(ctx context.Context, _ ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	(*p.contextCalls)++
	return ai.OAuthCredentials{}, ctx.Err()
}
func (p codingContextRefreshProvider) GetAPIKey(credentials ai.OAuthCredentials) string {
	return credentials.Access
}

func TestStoredAPIKeyRefreshFollowsRequestContext(t *testing.T) {
	legacyCalls, contextCalls := 0, 0
	ai.RegisterOAuthProvider("anthropic", codingContextRefreshProvider{legacyCalls: &legacyCalls, contextCalls: &contextCalls})
	t.Cleanup(func() { ai.UnregisterOAuthProvider("anthropic") })
	authPath := filepath.Join(t.TempDir(), "auth.json")
	auth, err := ai.NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	original := ai.Credential{Type: ai.CredentialOAuth, Refresh: "refresh", Access: "access", Expires: 1}
	if err := auth.Set("anthropic", original); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key, err := storedAPIKey(authPath, "anthropic", "fallback")(ctx)
	if key != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("storedAPIKey = %q, %v; want cancellation", key, err)
	}
	if contextCalls != 1 || legacyCalls != 0 {
		t.Fatalf("refresh calls: contextual=%d legacy=%d, want 1 and 0", contextCalls, legacyCalls)
	}
	stored, ok, err := auth.GetRaw("anthropic")
	if err != nil || !ok || stored.Access != original.Access || stored.Refresh != original.Refresh {
		t.Fatalf("stored credential = %#v, ok=%v, err=%v", stored, ok, err)
	}
}

// TestBuildModelAnthropicEnvAuthRequestShapes drives the session model
// builder from stored and environment auth to the wire. It mirrors upstream
// resolveProviderAuth (a stored credential first) and
// anthropicApiKeyAuth().resolve (ANTHROPIC_AUTH_TOKEN as a bearer header, then
// ANTHROPIC_OAUTH_TOKEN, then ANTHROPIC_API_KEY), with a subscription token taking the Claude Code
// identity.
func TestBuildModelAnthropicEnvAuthRequestShapes(t *testing.T) {
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
			name:          "auth token outranks both key variables",
			env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token", "ANTHROPIC_OAUTH_TOKEN": "sk-ant-oat-env", "ANTHROPIC_API_KEY": "api-key"},
			authorization: "Bearer auth-token", systemBlocks: 1,
		},
		{
			name:          "oauth token takes the Claude Code identity",
			env:           map[string]string{"ANTHROPIC_OAUTH_TOKEN": "sk-ant-oat-env", "ANTHROPIC_API_KEY": "api-key"},
			authorization: "Bearer sk-ant-oat-env", userAgent: "claude-cli/2.1.280", systemBlocks: 2,
		},
		{
			name:   "api key",
			env:    map[string]string{"ANTHROPIC_API_KEY": "api-key"},
			apiKey: "api-key", systemBlocks: 1,
		},
		{
			name:          "stored subscription login outranks ANTHROPIC_AUTH_TOKEN",
			env:           map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"},
			storedAccess:  "sk-ant-oat01-stored",
			authorization: "Bearer sk-ant-oat01-stored", userAgent: "claude-cli/2.1.280", systemBlocks: 2,
		},
		{
			name:          "stored subscription login without env auth",
			storedAccess:  "sk-ant-oat01-stored",
			authorization: "Bearer sk-ant-oat01-stored", userAgent: "claude-cli/2.1.280", systemBlocks: 2,
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
			agentDir := t.TempDir()
			config := `{"providers":{"anthropic":{"baseUrl":"` + server.URL + `"}}}`
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.storedAccess != "" || tc.storedKey != "" {
				auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
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
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			if !services.Registry().HasConfiguredAuth("anthropic") {
				t.Fatal("anthropic has no configured auth")
			}
			model, err := BuildModel("anthropic/claude-haiku-4-5", services)
			if err != nil {
				t.Fatal(err)
			}
			transcript := ai.NormalizeContext(ai.Context{SystemPrompt: "System prompt.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}})
			stream, err := model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result := collectRuntimeEvents(context.Background(), stream); len(result) == 0 {
				t.Fatal("no events")
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
