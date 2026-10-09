package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.87.1 packages/ai/src/models.ts:628 deletes the credential on logout; auth/resolve.ts:87-109 then uses ambient auth on the next request. The already-built startup provider must not retain the deleted key as its fallback.
func TestBuildModelLogoutFallsBackToEnv(t *testing.T) {
	for _, apiKind := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses, ai.APIAnthropicMessages} {
		for _, credentialType := range []ai.CredentialType{ai.CredentialAPIKey, ai.CredentialOAuth} {
			t.Run(string(apiKind)+"/"+string(credentialType), func(t *testing.T) {
				t.Setenv("MYCO_API_KEY", "env-key")
				headers := make(chan string, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					key := r.Header.Get("Authorization")
					if apiKind == ai.APIAnthropicMessages {
						key = r.Header.Get("X-Api-Key")
					}
					headers <- strings.TrimPrefix(key, "Bearer ")
					w.Header().Set("Content-Type", "text/event-stream")
					switch apiKind {
					case ai.APIAnthropicMessages:
						_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					case ai.APIOpenAIResponses:
						_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
					default:
						_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					}
				}))
				defer server.Close()
				dir := t.TempDir()
				config := `{"providers":{"myco":{"baseUrl":"` + server.URL + `","apiKey":"$MYCO_API_KEY","api":"` + string(apiKind) + `","models":[{"id":"m1"}]}}}`
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
				if err != nil {
					t.Fatal(err)
				}
				credential := ai.Credential{Type: credentialType, Key: "stored-key", Access: "stored-key", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
				if err := auth.Set("myco", credential); err != nil {
					t.Fatal(err)
				}
				ai.RegisterOAuthProvider("myco", failingRefreshOAuthProvider{id: "myco"})
				t.Cleanup(func() { ai.UnregisterOAuthProvider("myco") })
				model, _, _, err := buildModel("myco/m1", testServices(t, dir))
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"stored-key", "env-key"} {
					stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{})
					if err != nil {
						t.Fatal(err)
					}
					for range stream.Events(t.Context()) {
					}
					if got := <-headers; got != want {
						t.Fatalf("request key = %q, want %q", got, want)
					}
					// Same credential deletion as /logout, without rebuilding the model.
					if err := auth.Delete(t.Context(), "myco"); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

// These API kinds have no per-request key callback. Keep their shared builder's once-at-build resolution, including refresh errors, rather than silently dropping auth when startup stops pre-resolving it.
func TestBuildModelResolvesStaticAPIKeyOnce(t *testing.T) {
	for _, apiKind := range []ai.API{ai.APIOpenAICodexResponses, ai.APIAzureOpenAIResponses, ai.APIGoogleGenerativeAI, ai.APIGoogleVertex, ai.APIBedrockConverseStream, ai.APIMistralConversations} {
		t.Run(string(apiKind), func(t *testing.T) {
			dir := t.TempDir()
			config := `{"providers":{"static":{"baseUrl":"http://localhost:1","api":"` + string(apiKind) + `","models":[{"id":"m1"}]}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := auth.Set("static", ai.Credential{Type: ai.CredentialOAuth, Access: "old", Refresh: "stale", Expires: 1}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			ai.RegisterOAuthProvider("static", failingRefreshOAuthProvider{id: "static", refresh: func(context.Context) (ai.OAuthCredentials, error) {
				calls++
				return ai.OAuthCredentials{}, io.ErrUnexpectedEOF
			}})
			t.Cleanup(func() { ai.UnregisterOAuthProvider("static") })
			_, _, _, err = buildModel("static/m1", testServices(t, dir))
			if err == nil || !strings.Contains(err.Error(), io.ErrUnexpectedEOF.Error()) || calls != 1 {
				t.Fatalf("build error = %v, refresh calls = %d; want one failed refresh", err, calls)
			}
			calls, keyCalls := 0, 0
			ai.RegisterOAuthProvider("static", failingRefreshOAuthProvider{id: "static", keyCalls: &keyCalls, refresh: func(context.Context) (ai.OAuthCredentials, error) {
				calls++
				return ai.OAuthCredentials{Access: "fresh", Refresh: "rotated", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
			}})
			_, _, _, err = buildModel("static/m1", testServices(t, dir))
			if err != nil || calls != 1 || keyCalls != 1 {
				t.Fatalf("build error = %v, refresh calls = %d, key derivations = %d; want one resolution", err, calls, keyCalls)
			}
		})
	}
}

// Pi 0.87.1 packages/ai/src/providers/radius.ts:36-38 owns OAuth per gateway. Startup must not send this credential through the global OAuth provider registry.
func TestBuildModelRadiusStoredCredential(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600}`)
		case "/v1/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"start\"}\n\ndata: {\"type\":\"done\",\"reason\":\"stop\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}}}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	dir := t.TempDir()
	config := `{"providers":{"radius-dev":{"baseUrl":"` + gateway.URL + `/v1","oauth":"radius","api":"pi-messages","models":[{"id":"auto"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("radius-dev", ai.Credential{Type: ai.CredentialOAuth, Access: "old", Refresh: "stale", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	model, _, _, err := buildModel("radius-dev/auto", testServices(t, dir))
	if err != nil {
		t.Fatalf("Radius startup: %v", err)
	}
	mu.Lock()
	startupRequests := slices.Clone(seen)
	mu.Unlock()
	if len(startupRequests) != 0 {
		t.Fatalf("startup resolved Radius auth: %v", startupRequests)
	}
	stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(t.Context()) {
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"/v1/oauth/token ", "/v1/messages Bearer fresh-access"}; !slices.Equal(seen, want) {
		t.Fatalf("Radius requests = %q, want %q", seen, want)
	}
	stored, ok, err := auth.GetRaw("radius-dev")
	if err != nil || !ok || stored.Access != "fresh-access" || stored.Refresh != "fresh-refresh" {
		t.Fatalf("stored Radius credential = %+v, %v, %v", stored, ok, err)
	}
}
