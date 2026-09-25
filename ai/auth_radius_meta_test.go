package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Upstream meta.ts and radius.ts give both providers envApiKeyAuth plus a
// lazyOAuth method; only Meta's is a subscription.
func TestBuiltinProviderAuthMetaAndRadiusMethods(t *testing.T) {
	for _, tc := range []struct {
		provider, apiKeyName, envVar, oauthName string
		subscription                            bool
	}{
		{"meta", "Meta Model API key", "META_API_KEY", "Meta (Muse subscription)", true},
		{"radius", "Radius API key", "RADIUS_API_KEY", "Radius", false},
	} {
		auth, err := BuiltinProviderAuth(tc.provider)
		if err != nil {
			t.Fatal(err)
		}
		if auth.OAuth == nil || auth.OAuth.Name != tc.oauthName || auth.OAuth.IsSubscription != tc.subscription {
			t.Fatalf("%s OAuth = %+v", tc.provider, auth.OAuth)
		}
		if auth.APIKey == nil || auth.APIKey.Name != tc.apiKeyName {
			t.Fatalf("%s api key auth = %+v", tc.provider, auth.APIKey)
		}
		result, err := auth.APIKey.Resolve(context.Background(), APIKeyAuthInput{Ctx: testAuthContext(map[string]string{tc.envVar: "env-key"})})
		if err != nil || result == nil || result.Auth.APIKey != "env-key" || result.Source != tc.envVar {
			t.Fatalf("%s env resolve = %+v, %v", tc.provider, result, err)
		}
	}
}

// A models.json Radius gateway gets its own OAuth name and gateway, and its
// refresh keeps the granted scope.
func TestRadiusProviderAuthRefreshesAtItsGateway(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"scope":"models"}`))
	}))
	defer server.Close()
	auth := RadiusProviderAuth(NewRadiusProvider(RadiusProviderOptions{ID: "radius-dev", Name: "Dev Gateway", Gateway: server.URL}))
	if auth.OAuth.Name != "Dev Gateway" || auth.OAuth.IsSubscription || auth.APIKey.Name != "Radius API key" {
		t.Fatalf("auth = %+v / %+v", auth.OAuth, auth.APIKey)
	}
	refreshed, err := auth.OAuth.Refresh(context.Background(), Credential{Type: CredentialOAuth, Access: "old", Refresh: "old-refresh", Expires: 1})
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "old-refresh" {
		t.Fatalf("token form = %v", form)
	}
	if refreshed.Access != "new-access" || refreshed.Refresh != "new-refresh" || refreshed.Scope != "models" || refreshed.Expires <= time.Now().UnixMilli() {
		t.Fatalf("refreshed = %+v", refreshed)
	}
	modelAuth, err := auth.OAuth.ToAuth(refreshed)
	if err != nil || modelAuth.APIKey != "new-access" {
		t.Fatalf("toAuth = %+v, %v", modelAuth, err)
	}
}

type contextRefreshOAuthProbe struct {
	called chan context.Context
}

func (p *contextRefreshOAuthProbe) ID() string               { return "context-probe" }
func (p *contextRefreshOAuthProbe) Name() string             { return "Context probe" }
func (p *contextRefreshOAuthProbe) UsesCallbackServer() bool { return false }
func (p *contextRefreshOAuthProbe) Login(OAuthLoginCallbacks) (OAuthCredentials, error) {
	return OAuthCredentials{}, nil
}
func (p *contextRefreshOAuthProbe) RefreshToken(OAuthCredentials) (OAuthCredentials, error) {
	return OAuthCredentials{}, errors.New("context-free refresh called")
}
func (p *contextRefreshOAuthProbe) RefreshTokenContext(ctx context.Context, _ OAuthCredentials) (OAuthCredentials, error) {
	p.called <- ctx
	<-ctx.Done()
	return OAuthCredentials{}, ctx.Err()
}
func (p *contextRefreshOAuthProbe) GetAPIKey(credentials OAuthCredentials) string {
	return credentials.Access
}

func TestOAuthRefreshUsesProviderContext(t *testing.T) {
	var _ oauthContextRefresh = (*RadiusOAuth)(nil)
	probe := &contextRefreshOAuthProbe{called: make(chan context.Context, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := oauthRefresh(probe)(ctx, Credential{Type: CredentialOAuth, Refresh: "r"})
		result <- err
	}()
	select {
	case calledWith := <-probe.called:
		if calledWith != ctx {
			t.Fatal("refresh did not receive the owning operation context")
		}
	case err := <-result:
		t.Fatalf("context refresh was not called: %v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh = %v", err)
	}
}
