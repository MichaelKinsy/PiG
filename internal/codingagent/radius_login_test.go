package codingagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

var (
	_ oauthLoginMethodPrompter = (*ai.RadiusOAuth)(nil)
	_ oauthContextLogin        = (*ai.RadiusOAuth)(nil)
)

// Pi's radiusProvider exposes auth.oauth, so /login lists "Radius".
func TestOAuthProviderListIncludesRadius(t *testing.T) {
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir()}}
	got, ok := findOAuthProvider(m.oauthProviderList("login-oauth"), "radius")
	if !ok || got.Name != "Radius" || got.AuthType != "oauth" || got.Stored {
		t.Fatalf("radius login entry = %+v, %t", got, ok)
	}
}

// The interactive login must pass its cancellable context to Radius so that
// Esc aborts gateway requests, and must answer the method prompt with the
// selection made before the dialog opened.
func TestRunOAuthProviderLoginUsesContextAndPreselectedMethod(t *testing.T) {
	requests := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer gateway.Close()
	provider := ai.CreateRadiusOAuth(ai.RadiusOAuthOptions{ID: "radius", Name: "Radius", Gateway: gateway.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runOAuthProviderLogin(ctx, provider, ai.OAuthLoginCallbacks{
		OnSelect: func(ai.OAuthSelectPrompt) (string, error) { return ai.RadiusLoginMethodDeviceCode, nil },
	})
	if err == nil || err.Error() != "Login cancelled" || requests != 0 {
		t.Fatalf("cancelled login err = %v, gateway requests = %d", err, requests)
	}
}
