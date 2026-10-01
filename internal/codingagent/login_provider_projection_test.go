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
	m := NewInteractiveMode(InteractiveOptions{AgentDir: t.TempDir(), ModelRegistry: NewModelRegistry(t.TempDir())})
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
	want := []tui.OAuthProvider{{ID: "anthropic", Name: "Anthropic", AuthType: "oauth", MethodName: "Anthropic (Claude Pro/Max)"}, {ID: "anthropic", Name: "Anthropic", AuthType: "api_key", MethodName: "Anthropic API key"}, {ID: "google-vertex", Name: "Google Vertex AI", AuthType: "api_key", MethodName: "Google Cloud credentials"}}
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("options=%+v; want %+v", options, want)
	}
	if m.providerAuth("google-vertex").APIKey.Login != nil {
		t.Fatal("ambient authentication acquired a fabricated login method")
	}
}
