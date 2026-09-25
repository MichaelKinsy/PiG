package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream ModelRuntime.create configures a Radius provider for the built-in
// id and for each models.json "oauth": "radius" gateway, then its offline
// refresh restores the stored dynamic catalog before request auth runs.
func TestRequestAuthRuntimeComposesRadiusGateways(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"radius-dev":{"name":"Dev Gateway","baseUrl":"https://gateway.example/v1","oauth":"radius"}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(ai.PiMessagesModel{
		RadiusGatewayModel: ai.RadiusGatewayModel{ID: "dev-model", Name: "Dev Model", Input: []string{"text"}},
		API:                ai.APIPiMessages, Provider: "radius-dev", BaseURL: "https://gateway.example/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := ai.NewInMemoryModelsStore()
	if err := store.Write(context.Background(), "radius-dev", ai.ModelsStoreEntry{Models: []json.RawMessage{stored}}); err != nil {
		t.Fatal(err)
	}
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{
		"radius-dev": {Type: ai.CredentialOAuth, Access: "dev-access", Refresh: "dev-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	env := map[string]string{"RADIUS_API_KEY": "env-key"}
	authContext := ai.AuthContext{
		Env:        func(name string) (string, bool) { return env[name], strings.TrimSpace(env[name]) != "" },
		FileExists: func(string) bool { return false },
	}
	runtime, err := NewRequestAuthRuntime(context.Background(), RequestAuthRuntimeOptions{
		Credentials: credentials, AgentDir: dir, ModelsStore: store, AuthContext: &authContext, RefreshOnCreate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message := runtime.GetError(); message != "" {
		t.Fatalf("GetError = %q", message)
	}
	gateway := runtime.GetProvider("radius-dev")
	if gateway == nil || gateway.Auth.OAuth == nil || gateway.Auth.OAuth.Name != "Dev Gateway" || gateway.Auth.APIKey == nil || gateway.Auth.APIKey.Name != "Radius API key" {
		t.Fatalf("radius-dev provider = %+v", gateway)
	}
	if !slices.ContainsFunc(runtime.GetModels("radius-dev"), func(model RuntimeModel) bool { return model.ID == "dev-model" && model.Name == "Dev Model" }) {
		t.Fatalf("radius-dev models = %+v", runtime.GetModels("radius-dev"))
	}
	for _, tc := range []struct{ provider, key, source string }{
		{"radius-dev", "dev-access", "OAuth"},
		{"radius", "env-key", "RADIUS_API_KEY"},
	} {
		result, err := runtime.GetAuth(context.Background(), tc.provider, ai.AuthResolutionOverrides{})
		if err != nil || result == nil || result.Auth.APIKey != tc.key || result.Source != tc.source {
			t.Fatalf("%s auth = %+v, %v", tc.provider, result, err)
		}
		if !runtime.HasConfiguredAuth(tc.provider) {
			t.Fatalf("%s is not configured", tc.provider)
		}
	}
}

type runtimeContextOAuthProbe struct {
	contextual chan context.Context
	legacy     chan struct{}
}

func (p *runtimeContextOAuthProbe) ID() string               { return "meta" }
func (p *runtimeContextOAuthProbe) Name() string             { return "Meta probe" }
func (p *runtimeContextOAuthProbe) UsesCallbackServer() bool { return false }
func (p *runtimeContextOAuthProbe) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, nil
}
func (p *runtimeContextOAuthProbe) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	close(p.legacy)
	return ai.OAuthCredentials{}, errors.New("context-free refresh called")
}
func (p *runtimeContextOAuthProbe) RefreshTokenContext(ctx context.Context, _ ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	p.contextual <- ctx
	<-ctx.Done()
	return ai.OAuthCredentials{}, ctx.Err()
}
func (p *runtimeContextOAuthProbe) GetAPIKey(credentials ai.OAuthCredentials) string {
	return credentials.Access
}

// RequestAuthRuntime must retain the operation context while adapting the
// built-in OAuth method; otherwise cancellation only stops the waiter and the
// provider refresh continues in the background.
func TestRequestAuthRuntimeOAuthRefreshFollowsOwnerContext(t *testing.T) {
	probe := &runtimeContextOAuthProbe{contextual: make(chan context.Context, 1), legacy: make(chan struct{})}
	ai.RegisterOAuthProvider("meta", probe)
	t.Cleanup(func() { ai.UnregisterOAuthProvider("meta") })
	credentials := ai.NewInMemoryAuthStorage(map[string]ai.Credential{
		"meta": {Type: ai.CredentialOAuth, Access: "expired", Refresh: "refresh", Expires: 1},
	})
	runtime, err := NewRequestAuthRuntime(context.Background(), RequestAuthRuntimeOptions{Credentials: credentials})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := runtime.GetAuth(ctx, "meta", ai.AuthResolutionOverrides{})
		result <- err
	}()
	select {
	case <-probe.contextual:
	case <-probe.legacy:
		t.Fatal("request auth invoked the context-free refresh method")
	case err := <-result:
		t.Fatalf("request auth returned before contextual refresh: %v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request auth = %v", err)
	}
}
