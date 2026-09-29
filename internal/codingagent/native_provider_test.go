package codingagent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi publishes provider updates before a later refresh error; the final return
// is not a transaction that discards an already accepted publication.
func TestNativeProviderPublicationSurvivesLaterRefreshError(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	store, err := ai.NewAuthStorage(r.agentDir + "/auth.json")
	if err != nil {
		t.Fatal(err)
	}
	r.SetAuthStorage(store)
	original := extension.ProviderModelConfig{ID: "old", API: ai.APIOpenAICompletions, BaseURL: "https://native.invalid"}
	p := &extension.NativeProvider{ID: "native", Name: "Native", Models: []extension.ProviderModelConfig{original}, CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
		return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
	}, ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}}, nil, nil
	}, Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions, bool) (*ai.AssistantMessageEventStream, error) {
		return nil, errors.New("unused")
	}}
	p.ResolveRefreshCredential = func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialAPIKey, Key: "key"}, nil, nil
	}
	p.RefreshModels = func(_ context.Context, _ *ai.Credential, _ *ai.ModelsStoreEntry, network bool, force *bool, publish func(extension.NativeProviderPublication) error) ([]extension.ProviderModelConfig, error) {
		if !network {
			return p.Models, nil
		}
		if force == nil || !*force {
			t.Fatal("force was not delivered")
		}
		next := []extension.ProviderModelConfig{{ID: "published", API: original.API, BaseURL: original.BaseURL}}
		if err := publish(extension.NativeProviderPublication{Models: &next}); err != nil {
			return nil, err
		}
		return nil, errors.New("failure after publication")
	}
	if err := r.RegisterNativeProvider(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	result := r.ExtensionRefresh(t.Context(), new(true), []string{"native"}, new(true))
	if result.Errors["native"] == nil {
		t.Fatalf("refresh error was lost: %+v", result)
	}
	if !r.HasModelDefinition("native", "published") || r.HasModelDefinition("native", "old") {
		t.Fatal("accepted publication was rolled back on callback error")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := r.ExtensionRefresh(ctx, new(true), []string{"native"}, nil); !result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("cancelled refresh: %+v", result)
	}
}
