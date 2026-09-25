package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type testExternalOAuthProvider struct{}

func (testExternalOAuthProvider) ID() string               { return "external-oauth-test" }
func (testExternalOAuthProvider) Name() string             { return "ZZZ External OAuth" }
func (testExternalOAuthProvider) UsesCallbackServer() bool { return false }
func (testExternalOAuthProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, nil
}
func (testExternalOAuthProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, nil
}
func (testExternalOAuthProvider) GetAPIKey(ai.OAuthCredentials) string { return "" }
func (testExternalOAuthProvider) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	return ai.OAuthCredentialStatus{AuthType: "oauth", Source: "stored"}, true
}
func (testExternalOAuthProvider) StoreOAuthCredentials(ai.OAuthCredentials) (string, error) {
	return "", nil
}
func (testExternalOAuthProvider) DeleteOAuthCredentials() (bool, error) { return true, nil }

func TestOAuthProviderListMatchesUpstreamAccountProviderNames(t *testing.T) {
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir()}}

	providers := m.oauthProviderList("login-oauth")
	got, ok := findOAuthProvider(providers, "openai-codex")
	if !ok {
		t.Fatalf("openai-codex missing from account-login provider list: %+v", providers)
	}
	if got.Name != "OpenAI Codex" {
		t.Fatalf("openai-codex picker name = %q, want OpenAI Codex", got.Name)
	}
	// Pi 0.87.1 lists the provider name (providers/meta.ts), not the OAuth
	// flow name "Meta (Muse subscription)".
	if got, ok := findOAuthProvider(providers, "meta"); !ok || got.Name != "Meta" {
		t.Fatalf("meta picker entry = %+v (found %v), want name Meta", got, ok)
	}
}

func TestOAuthProviderListReadsExternalCredentialStatus(t *testing.T) {
	ai.RegisterOAuthProvider("external-oauth-test", testExternalOAuthProvider{})
	defer ai.UnregisterOAuthProvider("external-oauth-test")

	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir()}}
	providers := m.oauthProviderList("login-oauth")
	got, ok := findOAuthProvider(providers, "external-oauth-test")
	if !ok {
		t.Fatalf("external provider missing from account-login provider list: %+v", providers)
	}
	if !got.Stored || got.StoredType != "oauth" || got.AuthStatusSource != "stored" {
		t.Fatalf("external status = stored:%v type:%q source:%q, want stored oauth/stored", got.Stored, got.StoredType, got.AuthStatusSource)
	}
}

func findOAuthProvider(providers []tui.OAuthProvider, id string) (tui.OAuthProvider, bool) {
	for _, provider := range providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return tui.OAuthProvider{}, false
}
