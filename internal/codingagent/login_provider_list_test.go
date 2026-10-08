package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
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

// Pi 0.99.2 interactive-mode.ts:5668-5694 getLoginProviderOptions names every entry provider.name, never the OAuth
// flow's own name: openai.ts:11 "OpenAI" (flow "OpenAI (ChatGPT subscription)"), openai-codex.ts:10 "OpenAI Codex
// (legacy)" (flow "OpenAI (ChatGPT Plus/Pro)") and meta.ts "Meta" (flow "Meta (Muse subscription)"). The names come
// from the provider catalog for every built-in provider, not from a per-provider override.
func TestOAuthProviderListMatchesUpstreamAccountProviderNames(t *testing.T) {
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir()}}

	providers := m.oauthProviderList("login-oauth")
	for _, id := range ai.GeneratedProviders {
		got, ok := findOAuthProvider(providers, id)
		if !ok {
			continue
		}
		if want := ai.ProviderDisplayName(id); got.Name != want {
			t.Errorf("%s picker name = %q, want the catalog provider name %q", id, got.Name, want)
		}
	}
	for id, want := range map[string]string{"openai": "OpenAI", "openai-codex": "OpenAI Codex (legacy)", "meta": "Meta"} {
		if got, ok := findOAuthProvider(providers, id); !ok || got.Name != want {
			t.Errorf("%s picker entry = %+v (found %v), want name %q", id, got, ok, want)
		}
	}
}

// A provider outside the catalog (an extension's OAuth provider) keeps the name its OAuth flow declares, as Pi's
// provider.name is whatever the registering extension named it.
func TestOAuthProviderListKeepsTheFlowNameOfAProviderOutsideTheCatalog(t *testing.T) {
	ai.RegisterOAuthProvider("external-oauth-test", testExternalOAuthProvider{})
	defer ai.UnregisterOAuthProvider("external-oauth-test")

	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir()}}
	got, ok := findOAuthProvider(m.oauthProviderList("login-oauth"), "external-oauth-test")
	if !ok || got.Name != "ZZZ External OAuth" {
		t.Fatalf("external picker entry = %+v (found %v), want the flow's own name", got, ok)
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

// Pi 0.99.2 getLoginProviderOptions (interactive-mode.ts:5668-5694) lists the composed runtime provider's name, which a
// models.json `name` replaces over the catalog name (provider-composer.ts:588:
// `extension?.name ?? config?.name ?? base?.name ?? ...`). A provider the runtime composes keeps its catalog name only
// when nothing renames it.
func TestOAuthProviderListUsesTheComposedRuntimeProviderName(t *testing.T) {
	runtime := &RequestAuthRuntime{providers: []*RuntimeProvider{
		{ID: "anthropic", Name: "Corp Anthropic", Auth: ai.ProviderAuth{OAuth: &ai.OAuthAuth{}}},
		{ID: "github-copilot", Name: "GitHub Copilot", Auth: ai.ProviderAuth{OAuth: &ai.OAuthAuth{}}},
	}}
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir(), RequestAuthRuntime: runtime}}
	providers := m.oauthProviderList("login-oauth", false)
	for id, want := range map[string]string{"anthropic": "Corp Anthropic", "github-copilot": "GitHub Copilot", "meta": "Meta"} {
		if got, ok := findOAuthProvider(providers, id); !ok || got.Name != want {
			t.Errorf("%s picker entry = %+v (found %v), want name %q", id, got, ok, want)
		}
	}
}

// Pi composes the registering extension's oauth as the provider's account login (provider-composer.ts: `extension?.oauth ?? base.auth.oauth`)
// and gives an oauth-only registration no API-key method, so /login lists it under "Sign in with an account" only.
// An extension provider outside the catalog (pi-antigravity) registers this way.
func TestLoginProviderOptionsListAnExtensionOAuthProviderUnderAccountSignIn(t *testing.T) {
	ai.RegisterOAuthProvider("external-oauth-test", testExternalOAuthProvider{})
	defer ai.UnregisterOAuthProvider("external-oauth-test")
	dir := t.TempDir()
	registry := NewModelRegistry(dir)
	if err := registry.RegisterProvider("external-oauth-test", extension.ProviderConfig{
		API:     "external-api",
		BaseURL: "https://external.test",
		OAuth:   &extension.ProviderOAuth{Name: "ZZZ External OAuth"},
		Models:  []extension.ProviderModelConfig{{ID: "m1", Name: "M1"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: dir, ModelRegistry: registry}}

	var kinds []string
	for _, option := range m.getLoginProviderOptions(false) {
		if option.ID == "external-oauth-test" {
			kinds = append(kinds, option.AuthType)
		}
	}
	if len(kinds) != 1 || kinds[0] != "oauth" {
		t.Fatalf("external-oauth-test login options = %v, want [oauth]", kinds)
	}
}

type testSubscriptionOAuthProvider struct{ testExternalOAuthProvider }

func (testSubscriptionOAuthProvider) IsSubscription() bool { return true }

// Pi adapts the registering extension's oauth with its own isSubscription (provider-composer.ts:356 adaptOAuth
// `isSubscription: config.isSubscription`), which /login (interactive-mode.ts:5767) and isUsingSubscription
// (model-runtime.ts:540) read. An extension provider outside the catalog keeps that flag through the composed auth,
// on the subprocess registration path as on the native one (TestExtensionOAuthOnlyProviderHasNoAPIKeyMethodUpstream).
func TestExtensionOAuthProviderOutsideTheCatalogKeepsItsSubscriptionFlag(t *testing.T) {
	for _, subscription := range []bool{true, false} {
		ai.UnregisterOAuthProvider("external-oauth-test")
		if subscription {
			ai.RegisterOAuthProvider("external-oauth-test", testSubscriptionOAuthProvider{})
		} else {
			ai.RegisterOAuthProvider("external-oauth-test", testExternalOAuthProvider{})
		}
		dir := t.TempDir()
		registry := NewModelRegistry(dir)
		if err := registry.RegisterProvider("external-oauth-test", extension.ProviderConfig{
			Name:    "Extension OAuth",
			API:     "external-api",
			BaseURL: "https://external.test",
			OAuth:   &extension.ProviderOAuth{Name: "ZZZ External OAuth", IsSubscription: subscription},
			Models:  []extension.ProviderModelConfig{{ID: "m1", Name: "M1"}},
		}); err != nil {
			t.Fatal(err)
		}
		if got := registry.ProviderIsSubscription("external-oauth-test"); got != subscription {
			t.Errorf("isSubscription=%v: ProviderIsSubscription = %v", subscription, got)
		}
		// model-runtime-auth-options.test.ts:302: one oauth option, named for the provider, whose method is the extension's flow.
		m := &InteractiveMode{opts: InteractiveOptions{AgentDir: dir, ModelRegistry: registry}}
		var options []tui.OAuthProvider
		for _, option := range m.getLoginProviderOptions(false) {
			if option.ID == "external-oauth-test" {
				options = append(options, option)
			}
		}
		if len(options) != 1 {
			t.Fatalf("isSubscription=%v: login options = %+v, want one oauth option", subscription, options)
		}
		option := options[0]
		if option.AuthType != "oauth" || option.Name != "Extension OAuth" || option.MethodName != "ZZZ External OAuth" || option.Subscription == nil || *option.Subscription != subscription {
			t.Errorf("isSubscription=%v: login option = %+v", subscription, option)
		}
	}
	ai.UnregisterOAuthProvider("external-oauth-test")
}

// Only an extension registration's oauth composes into the provider (provider-composer.ts:489
// `extension?.oauth ? adaptOAuth(extension.oauth) : base?.auth.oauth`). A models.json provider outside the catalog
// gains no account login from an unrelated OAuth flow of the same id, so /login offers its API key only.
func TestModelsJSONProviderOutsideTheCatalogGainsNoOAuthFromTheFlowRegistry(t *testing.T) {
	ai.RegisterOAuthProvider("external-oauth-test", testExternalOAuthProvider{})
	defer ai.UnregisterOAuthProvider("external-oauth-test")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"external-oauth-test":{"baseUrl":"https://external.test","api":"openai-completions","apiKey":"literal-key","models":[{"id":"m1"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: dir, ModelRegistry: registry}}

	var kinds []string
	for _, option := range m.getLoginProviderOptions(false) {
		if option.ID == "external-oauth-test" {
			kinds = append(kinds, option.AuthType)
		}
	}
	if len(kinds) != 1 || kinds[0] != "api_key" {
		t.Fatalf("external-oauth-test login options = %v, want [api_key]", kinds)
	}
}
