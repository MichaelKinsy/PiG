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
	}, Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return nil, errors.New("unused")
	}}
	p.ResolveRefreshCredential = func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialAPIKey, Key: "key"}, nil, nil
	}
	// The provider object owns its model list; a publication's update changes it and the registry publishes the list it then reports.
	current := []*ai.Model{{ID: "old", ProviderMeta: ai.ProviderMetadata{ProviderID: "native", API: original.API, BaseURL: original.BaseURL}}}
	p.GetModels = func(context.Context) ([]*ai.Model, error) { return current, nil }
	p.RefreshModels = func(refresh ai.RefreshModelsContext) error {
		if !refresh.AllowNetwork {
			return nil
		}
		if refresh.Force == nil || !*refresh.Force {
			t.Fatal("force was not delivered")
		}
		next := []*ai.Model{{ID: "published", ProviderMeta: ai.ProviderMetadata{ProviderID: "native", API: original.API, BaseURL: original.BaseURL}}}
		if _, err := refresh.Publish(ai.ModelsPublication{Update: func() { current = next }}); err != nil {
			return err
		}
		return errors.New("failure after publication")
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
