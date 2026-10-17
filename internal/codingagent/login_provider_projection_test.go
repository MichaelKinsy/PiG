package codingagent

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/oauth-selector.test.ts:18-72. The real registry also contains builtins; compare the same two explicitly supplied provider definitions.
func TestLoginProviderOwnedAuthOptionsProjection(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir(), ModelRegistry: NewModelRegistry(t.TempDir())})
	login := func(context.Context, ai.AuthInteraction) (ai.Credential, error) { return ai.Credential{}, nil }
	oauthLogin := func(context.Context, ai.AuthInteraction, ai.LoginOptions) (ai.Credential, error) {
		return ai.Credential{}, nil
	}
	providers := []*ai.ModelsProvider{
		{ID: "anthropic", Name: "Anthropic", Auth: ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Anthropic (Claude Pro/Max)", Login: oauthLogin}, APIKey: &ai.APIKeyAuth{Name: "Anthropic API key", Login: login}}, GetModels: func() ([]*ai.Model, error) { return nil, nil }},
		{ID: "google-vertex", Name: "Google Vertex AI", Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Google Cloud credentials"}}, GetModels: func() ([]*ai.Model, error) { return nil, nil }},
	}
	for _, provider := range providers {
		if err := m.opts.ModelRegistry.RegisterNativeModelsProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	options := slices.DeleteFunc(m.getLoginProviderOptions(false), func(p tui.OAuthProvider) bool { return p.ID != "anthropic" && p.ID != "google-vertex" })
	// Pi 1.0.0 also sets subscription from provider.auth.oauth?.isSubscription === true (interactive-mode.ts:5722); neither provider here declares it.
	want := []tui.OAuthProvider{{ID: "anthropic", Name: "Anthropic", AuthType: "oauth", Subscription: new(false)}, {ID: "anthropic", Name: "Anthropic", AuthType: "api_key", Subscription: new(false)}, {ID: "google-vertex", Name: "Google Vertex AI", AuthType: "api_key", Subscription: new(false)}}
	wantMethods := []string{"Anthropic (Claude Pro/Max)", "Anthropic API key", "Google Cloud credentials"}
	var gotMethods []string
	for i := range options {
		// Pi's provider.method is the provider's own auth object (interactive-mode.ts:5788-5822); compare its name, not the object.
		if options[i].Method == nil {
			t.Fatalf("option %d has no auth method: %+v", i, options[i])
		}
		gotMethods = append(gotMethods, options[i].Method.AuthMethodName())
		options[i].Method = nil
	}
	if !reflect.DeepEqual(options, want) || !reflect.DeepEqual(gotMethods, wantMethods) {
		t.Fatalf("options=%+v methods=%q; want %+v methods=%q", options, gotMethods, want, wantMethods)
	}
	if m.providerAuth("google-vertex").APIKey.Login != nil {
		t.Fatal("ambient authentication acquired a fabricated login method")
	}
}

// interactive-mode.ts:5790-5796 getLoginProviderOptions: a provider without configured auth has no status (undefined, not a
// typed nil), a configured one is typed by whether the provider uses OAuth and takes its source from the status label, else
// from the status source.
func TestAuthSelectorStatusFollowsPiLoginProviderOptions(t *testing.T) {
	if status := authSelectorStatus(ai.AuthStatus{Source: ai.AuthSourceStored}, true); status != nil {
		t.Fatalf("unconfigured auth has status %#v, want an untyped nil", status)
	}
	for _, tc := range []struct {
		name       string
		status     ai.AuthStatus
		usingOAuth bool
		wantType   string
		wantSource string
	}{
		{"label wins over source", ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: "OPENAI_API_KEY"}, false, "api_key", "OPENAI_API_KEY"},
		{"runtime key", ai.AuthStatus{Configured: true, Source: ai.AuthSourceRuntime}, false, "api_key", "runtime"},
		{"stored oauth credential", ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored}, true, "oauth", "stored"},
		{"models.json key", ai.AuthStatus{Configured: true, Source: ai.AuthSourceModelsJSONKey}, false, "api_key", "models_json_key"},
	} {
		got := authSelectorStatus(tc.status, tc.usingOAuth)
		if got == nil || got.AuthCheckType() != tc.wantType || got.AuthCheckSource() != tc.wantSource {
			t.Errorf("%s: status = %#v, want %s/%s", tc.name, got, tc.wantType, tc.wantSource)
		}
	}
}
