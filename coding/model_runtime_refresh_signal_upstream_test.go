package coding

import (
	"context"
	"errors"
	"github.com/MichaelKinsy/PiG/ai"
	"testing"
	"time"
)

func refreshSignalModel(id string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: id, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
}
func registerRefreshSignalProvider(t *testing.T, services *AgentSessionServices, id string, config ProviderConfigInput) {
	t.Helper()
	if err := services.ModelRuntime().RegisterProvider(id, config); err != nil {
		t.Fatal(err)
	}
}

// .upstream/v1.0.3/packages/coding-agent/test/model-runtime-auth-options.test.ts:266 ("does not cancel an extension OAuth refresh with the request"; 1.0.2 forwarded the cancellation)
func TestRuntimeDoesNotCancelExtensionRefreshWithRequestUpstream(t *testing.T) {
	services := newRuntimeTestServices(t)
	if err := services.Auth().Set("extension-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "expired", Refresh: "refresh"}); err != nil {
		t.Fatal(err)
	}
	var signal context.Context
	registerRefreshSignalProvider(t, services, "extension-oauth", ProviderConfigInput{Name: "Extension OAuth", BaseURL: "https://example.test/v1", API: ai.APIOpenAICompletions, Models: []ai.AnyModel{refreshSignalModel("extension-model")}, OAuth: &ExtensionOAuthConfig{Name: "Extension subscription", Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
		return ai.Credential{Type: ai.CredentialOAuth, Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}, nil
	}, RefreshToken: func(ctx context.Context, credential ai.Credential) (ai.Credential, error) {
		signal = ctx
		credential.Expires = time.Now().Add(time.Minute).UnixMilli()
		return credential, nil
	}, GetAPIKey: func(value ai.Credential) string { return value.Access }}})
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	if _, err := services.Registry().NativeModels().GetAuth(ctx, "extension-oauth", ai.AuthResolutionOverrides{}); err != nil {
		t.Fatal(err)
	}
	reason := errors.New("cancelled")
	cancel(reason)
	// A started refresh may already have rotated the refresh token and must be persisted.
	if signal == nil || signal.Done() == nil || signal.Err() != nil {
		t.Fatalf("refresh signal=%v want a live signal after the request was cancelled", signal)
	}
}
